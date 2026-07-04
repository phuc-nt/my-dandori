package integrations

import (
	"fmt"
	"html"
	"strings"
	"time"

	"github.com/phuc-nt/dandori/internal/store"
)

// pageAlreadyExists reports whether a CreatePage error means the page is
// already there (title unique per space). Matched by message rather than a
// typed sentinel because ConfluencePoster is a narrow interface and the
// confluence package imports this one (importing it back would cycle).
func pageAlreadyExists(err error) bool {
	return err != nil && strings.Contains(err.Error(), "already exists")
}

// FlywheelPublisher distributes a playbook card to the shared channels.
// The card describes a PATTERN (what made the run work) attributed to an
// AGENT — it never contains operator names, rankings, or comparisons:
// publishing human scores teaches people to game the proxies.
//
// Clients are narrow interfaces so the publisher tests without the network;
// the guard runs FIRST, so with DRY_RUN=true nil clients are never touched.
type SlackPoster interface {
	PostMessage(channel, text string) (string, error)
}

type ConfluencePoster interface {
	CreatePage(spaceID, parentID, title, storageHTML string) (string, error)
}

type FlywheelPublisher struct {
	St           *store.Store
	Guard        *Guard
	Slack        SlackPoster
	SlackChannel string
	Confluence   ConfluencePoster
	SpaceID      string
}

// PlaybookCard is the published content, built from playbook data only.
type PlaybookCard struct {
	PlaybookID int64
	Name       string
	AgentName  string
	TaskKey    string
	Model      string
	CostUSD    float64
	Why        string
}

// BuildCard loads a playbook into card form. Errors when it doesn't exist.
func (p *FlywheelPublisher) BuildCard(playbookID int64) (*PlaybookCard, error) {
	c := &PlaybookCard{PlaybookID: playbookID}
	err := p.St.Read().QueryRow(`SELECT pb.name, COALESCE(a.name, pb.agent_id, ''), COALESCE(pb.task_key,''),
		COALESCE(pb.model,''), COALESCE(pb.cost_usd,0), COALESCE(pb.notes,'')
		FROM playbooks pb LEFT JOIN agents a ON a.id = pb.agent_id
		WHERE pb.id = ?`, playbookID).
		Scan(&c.Name, &c.AgentName, &c.TaskKey, &c.Model, &c.CostUSD, &c.Why)
	if err != nil {
		return nil, fmt.Errorf("playbook %d: %w", playbookID, err)
	}
	return c, nil
}

func (c *PlaybookCard) slackText() string {
	msg := fmt.Sprintf("📘 *Playbook mới: %s*\nAgent: %s", c.Name, c.AgentName)
	if c.TaskKey != "" {
		msg += " · Task: " + c.TaskKey
	}
	msg += fmt.Sprintf("\nVì sao đáng dùng: %s\nChi phí tham chiếu: $%.2f (%s)\nMở Dandori console → Playbooks để dùng mẫu này.",
		c.Why, c.CostUSD, c.Model)
	return msg
}

func (c *PlaybookCard) confluenceHTML() string {
	return fmt.Sprintf(`<h2>%s</h2><p><b>Agent:</b> %s · <b>Task:</b> %s · <b>Model:</b> %s · <b>Chi phí tham chiếu:</b> $%.2f</p><p><b>Vì sao đáng dùng:</b> %s</p><p>Mở Dandori console → Playbooks để tạo run từ mẫu này.</p>`,
		html.EscapeString(c.Name), html.EscapeString(c.AgentName), html.EscapeString(c.TaskKey),
		html.EscapeString(c.Model), c.CostUSD, html.EscapeString(c.Why))
}

// Publish posts the card to Slack + Confluence, once per playbook per day.
// Returns what happened per leg: "sent", "dry-run", or "deduped".
func (p *FlywheelPublisher) Publish(playbookID int64, actor string) (slackRes, confRes string, err error) {
	card, err := p.BuildCard(playbookID)
	if err != nil {
		return "", "", err
	}
	date := time.Now().UTC().Format("2006-01-02")

	slackRes = p.leg(fmt.Sprintf("flywheel:slack:%d:%s", playbookID, date),
		"post playbook card to slack", card.Name, func() error {
			_, err := p.Slack.PostMessage(p.SlackChannel, card.slackText())
			return err
		})
	confRes = p.leg(fmt.Sprintf("flywheel:confluence:%d:%s", playbookID, date),
		"create playbook card page", card.Name, func() error {
			_, err := p.Confluence.CreatePage(p.SpaceID, "", "Playbook: "+card.Name+" ("+date+")", card.confluenceHTML())
			// Today's card already existing means it's already published —
			// idempotent success, not a failure (e.g. local dedup record lost
			// but the remote page persists).
			if pageAlreadyExists(err) {
				return nil
			}
			return err
		})
	_, _ = p.St.DB.Exec(`INSERT INTO events(run_id, ts, kind, tool_name, ok, payload)
		VALUES(NULL, ?, 'playbook_published', ?, 1, ?)`,
		store.Now(), actor, fmt.Sprintf("playbook %d slack=%s confluence=%s", playbookID, slackRes, confRes))
	return slackRes, confRes, nil
}

// leg runs one publish leg with dedup + guard. Guard runs BEFORE the client.
func (p *FlywheelPublisher) leg(dedup, action, detail string, send func() error) string {
	var n int
	p.St.DB.QueryRow(`SELECT count(*) FROM notifications WHERE dedup = ?`, dedup).Scan(&n)
	if n > 0 {
		return "deduped"
	}
	if !p.Guard.Allow(action, detail) {
		return "dry-run"
	}
	if err := send(); err != nil {
		return "error: " + err.Error()
	}
	p.St.DB.Exec(`INSERT INTO notifications(kind, dedup, sent_at, detail)
		VALUES('flywheel', ?, ?, ?) ON CONFLICT(dedup) DO NOTHING`, dedup, store.Now(), detail)
	return "sent"
}
