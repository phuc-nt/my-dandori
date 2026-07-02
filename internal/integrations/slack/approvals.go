package slack

import (
	"fmt"
	"log"

	"github.com/phuc-nt/dandori/internal/govern"
	"github.com/phuc-nt/dandori/internal/integrations"
	"github.com/phuc-nt/dandori/internal/store"
)

// ApprovalBridge mirrors the review queue into Slack (UC7): pending approvals
// are posted once, then reactions are polled — ✅ approves, ❌ rejects. The web
// console can still decide first; first writer wins.
type ApprovalBridge struct {
	St         *store.Store
	Client     *Client
	Guard      *integrations.Guard
	Channel    string
	ConsoleURL string
}

// Tick posts new pending approvals and polls reactions on posted ones.
func (b *ApprovalBridge) Tick() {
	if err := b.postNew(); err != nil {
		log.Println("slack approvals post:", err)
	}
	if err := b.pollReactions(); err != nil {
		log.Println("slack approvals poll:", err)
	}
}

func (b *ApprovalBridge) postNew() error {
	rows, err := b.St.DB.Query(`SELECT id, COALESCE(run_id,''), action, COALESCE(reason,'')
		FROM approvals WHERE status = 'pending' AND slack_ts IS NULL ORDER BY id LIMIT 10`)
	if err != nil {
		return err
	}
	defer rows.Close()
	type row struct {
		id                    int64
		runID, action, reason string
	}
	var pend []row
	for rows.Next() {
		var r row
		if err := rows.Scan(&r.id, &r.runID, &r.action, &r.reason); err != nil {
			return err
		}
		pend = append(pend, r)
	}
	if err := rows.Err(); err != nil {
		return err
	}
	for _, r := range pend {
		text := fmt.Sprintf(":lock: *Dandori approval #%d*\nrun `%s` wants to run:\n> `%s`\n_%s_\nReact :white_check_mark: to approve · :x: to reject · or decide at %s/reviews",
			r.id, r.runID, r.action, r.reason, b.ConsoleURL)
		if !b.Guard.Allow("post slack approval", text) {
			// Dry run: mark so we don't re-log every tick; poller skips dry markers.
			b.St.DB.Exec(`UPDATE approvals SET slack_ts = 'dry-run' WHERE id = ?`, r.id)
			continue
		}
		ts, err := b.Client.PostMessage(b.Channel, text)
		if err != nil {
			return err
		}
		if _, err := b.St.DB.Exec(`UPDATE approvals SET slack_ts = ?, channel = 'slack' WHERE id = ?`, ts, r.id); err != nil {
			return err
		}
	}
	return nil
}

func (b *ApprovalBridge) pollReactions() error {
	rows, err := b.St.DB.Query(`SELECT id, slack_ts FROM approvals
		WHERE status = 'pending' AND slack_ts IS NOT NULL AND slack_ts != 'dry-run' LIMIT 20`)
	if err != nil {
		return err
	}
	defer rows.Close()
	type row struct {
		id int64
		ts string
	}
	var open []row
	for rows.Next() {
		var r row
		if err := rows.Scan(&r.id, &r.ts); err != nil {
			return err
		}
		open = append(open, r)
	}
	if err := rows.Err(); err != nil {
		return err
	}
	for _, r := range open {
		reactions, err := b.Client.GetReactions(b.Channel, r.ts)
		if err != nil {
			return err
		}
		if verdict, user, ok := firstVerdict(reactions); ok {
			actor := b.Client.UserName(user)
			won, err := govern.Decide(b.St, r.id, verdict, actor, "via slack reaction")
			if err != nil {
				return err
			}
			if won {
				log.Printf("approval #%d %s by %s via slack", r.id, map[bool]string{true: "approved", false: "rejected"}[verdict], actor)
			}
		}
	}
	return nil
}

// firstVerdict finds the first ✅ (approve) or ❌ (reject) reaction.
func firstVerdict(reactions []Reaction) (approve bool, userID string, ok bool) {
	for _, re := range reactions {
		if len(re.Users) == 0 {
			continue
		}
		switch re.Name {
		case "white_check_mark", "heavy_check_mark", "+1":
			return true, re.Users[0], true
		case "x", "no_entry", "-1":
			return false, re.Users[0], true
		}
	}
	return false, "", false
}
