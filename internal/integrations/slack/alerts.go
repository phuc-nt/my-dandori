package slack

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"log"
	"time"

	"github.com/phuc-nt/dandori/internal/integrations"
	"github.com/phuc-nt/dandori/internal/store"
)

// Alerter turns governance events into Slack channel alerts (UG2), with
// day-level dedup via the notifications table.
type Alerter struct {
	St      *store.Store
	Client  *Client
	Guard   *integrations.Guard
	Channel string
	// BaseURL is the console origin used to build deep links in messages
	// (e.g. https://dandori.acme.com). Empty → messages omit links.
	BaseURL string
}

// Dispatch scans un-notified alertable events and posts them. Safe to call
// repeatedly (ticker); dedup makes it idempotent.
func (a *Alerter) Dispatch() error {
	rows, err := a.St.DB.Query(`SELECT e.id, e.kind, COALESCE(e.payload,''), COALESCE(e.run_id,'')
		FROM events e
		WHERE e.kind IN ('budget_warn','guardrail_block','kill','flag','cost_spike','closed_loop','approval_escalation','flag_stale')
		  AND e.ts >= datetime('now','-1 day')
		  AND NOT EXISTS (SELECT 1 FROM notifications n WHERE n.dedup = 'event:' || e.id)
		ORDER BY e.id LIMIT 20`)
	if err != nil {
		return err
	}
	defer rows.Close()
	type alert struct {
		id            int64
		kind, payload string
		runID         string
	}
	var alerts []alert
	for rows.Next() {
		var al alert
		if err := rows.Scan(&al.id, &al.kind, &al.payload, &al.runID); err != nil {
			return err
		}
		alerts = append(alerts, al)
	}
	if err := rows.Err(); err != nil {
		return err
	}
	for _, al := range alerts {
		// Day-level topic dedup: same kind+payload-prefix per day → one alert.
		topic := dayDedup(al.kind, al.payload)
		if a.alreadySent(topic) {
			a.markSent("event:"+fmt.Sprint(al.id), "suppressed:"+topic)
			continue
		}
		text := a.formatAlert(al.kind, al.payload, al.runID)
		if a.Guard.Allow("post slack alert", text) {
			if _, err := a.Client.PostMessage(a.Channel, text); err != nil {
				log.Println("slack alert:", err)
				continue // retry next tick, do not mark sent
			}
		}
		a.markSent("event:"+fmt.Sprint(al.id), text)
		a.markSent(topic, text)
	}
	return nil
}

// formatAlert renders a governance event as a business-language Vietnamese
// Slack message with a console deep link, replacing the old technical
// ":rotating_light: Dandori <kind>" form. The payload already carries the
// human-readable detail (e.g. "budget global at 95% ($4750/$5000)"); this adds
// an icon, a plain-language lead, and a link.
func (a *Alerter) formatAlert(kind, payload, runID string) string {
	var icon, lead, link string
	switch kind {
	case "closed_loop":
		icon, lead, link = "⛔", "Agent bị hạ quyền tự chủ", a.deepLink("/dash/org")
	case "budget_warn":
		icon, lead, link = "💸", "Ngân sách chạm ngưỡng", a.deepLink("/budgets")
	case "flag_stale":
		icon, lead, link = "🚩", "Có cảnh báo mở đã lâu chưa xử lý", a.deepLink("/reviews")
	case "kill":
		icon, lead, link = "🛑", "Một run đã bị dừng", a.runLink(runID)
	case "guardrail_block":
		icon, lead, link = "🛡", "Guardrail chặn một hành động", a.runLink(runID)
	case "approval_escalation":
		icon, lead, link = "⏳", "Có phê duyệt chờ quá lâu", a.deepLink("/reviews")
	case "cost_spike":
		icon, lead, link = "📈", "Chi phí tăng đột biến", a.runLink(runID)
	default: // flag and any future kind
		icon, lead, link = "🔔", "Cảnh báo quản trị", a.runLink(runID)
	}
	text := fmt.Sprintf("%s *%s*\n%s", icon, lead, payload)
	if link != "" {
		text += "\n" + link
	}
	return text
}

func (a *Alerter) deepLink(path string) string {
	if a.BaseURL == "" {
		return ""
	}
	return "Xem: " + a.BaseURL + path
}

func (a *Alerter) runLink(runID string) string {
	if runID == "" {
		return ""
	}
	if a.BaseURL == "" {
		return "run `" + runID + "`"
	}
	return "Xem: " + a.BaseURL + "/runs/" + runID
}

func dayDedup(kind, payload string) string {
	h := sha256.Sum256([]byte(payload))
	return fmt.Sprintf("topic:%s:%s:%s", kind, hex.EncodeToString(h[:6]), time.Now().UTC().Format("2006-01-02"))
}

func (a *Alerter) alreadySent(dedup string) bool {
	var n int
	_ = a.St.DB.QueryRow(`SELECT count(*) FROM notifications WHERE dedup = ?`, dedup).Scan(&n)
	return n > 0
}

func (a *Alerter) markSent(dedup, detail string) {
	_, _ = a.St.DB.Exec(`INSERT INTO notifications(kind, dedup, sent_at, detail)
		VALUES('slack', ?, ?, ?) ON CONFLICT(dedup) DO NOTHING`, dedup, store.Now(), detail)
}
