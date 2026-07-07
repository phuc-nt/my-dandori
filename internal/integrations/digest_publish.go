package integrations

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/phuc-nt/dandori/internal/config"
	"github.com/phuc-nt/dandori/internal/learn"
	"github.com/phuc-nt/dandori/internal/store"
)

// GmailSender is the narrow surface DigestPublisher needs from gws.Runner —
// kept separate from the concrete type so tests substitute a fake without
// touching the network.
type GmailSender interface {
	GmailSendRaw(ctx context.Context, from, to, subject, body string) error
}

// DigestPublisher sends the UG2b fleet-health digest to Slack + Gmail.
// Recipients come ONLY from Cfg.DigestRecipients (C2 — config-pinned, never
// a param): there is no code path from a request to a chosen destination.
type DigestPublisher struct {
	St    *store.Store
	Guard *Guard
	Cfg   *config.Config
	Slack SlackPoster
	GWS   GmailSender
	From  string // Gmail "From" header; the authed account's own address
}

// Send builds the digest text/body from data and delivers it over both
// legs. Returns "no recipients configured" for both legs (no send) when
// Cfg.DigestRecipients is empty. Otherwise each leg is "sent", "dry-run", or
// "deduped", independent of the other leg's outcome.
func (p *DigestPublisher) Send(ctx context.Context, data *learn.DigestData) (slackRes, gmailRes string, err error) {
	recipients := p.Cfg.DigestRecipients
	if len(recipients) == 0 {
		return "no recipients configured", "no recipients configured", nil
	}
	date := time.Now().UTC().Format("2006-01-02")
	channel := p.Cfg.Integrations.SlackChannel

	slackRes = p.leg(fmt.Sprintf("digest-slack:%s:%s", date, channel),
		"post digest to slack", channel, func() error {
			_, err := p.Slack.PostMessage(channel, slackText(data))
			return err
		})

	// M4: the dedup key hashes the CURRENT recipient set (sorted, so order
	// doesn't matter) + date. A recipients change same-day yields a new
	// hash — a same-day resend after correcting a wrong address is never
	// silently swallowed by the previous (wrong) send's dedup row.
	to := strings.Join(sortedCopy(recipients), ",")
	hash := sha256.Sum256([]byte(to))
	dedup := fmt.Sprintf("digest-gmail:%s:%s", date, hex.EncodeToString(hash[:4]))
	subject := fmt.Sprintf("Dandori fleet digest %s", date)
	body := gmailText(data)
	gmailRes = p.leg(dedup, "send digest email", to, func() error {
		return p.GWS.GmailSendRaw(ctx, p.From, to, subject, body)
	})
	return slackRes, gmailRes, nil
}

// leg mirrors FlywheelPublisher.leg (dedup check → Guard.Allow → send →
// record notification). Guard runs BEFORE send so DRY_RUN never touches a
// client.
func (p *DigestPublisher) leg(dedup, action, detail string, send func() error) string {
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
		VALUES('digest', ?, ?, ?) ON CONFLICT(dedup) DO NOTHING`, dedup, store.Now(), detail)
	p.audit(action, detail)
	return "sent"
}

func (p *DigestPublisher) audit(action, destination string) {
	_, _ = p.St.DB.Exec(`INSERT INTO events(run_id, ts, kind, tool_name, ok, payload)
		VALUES(NULL, ?, 'digest_sent', ?, 1, ?)`, store.Now(), action, destination)
}

func sortedCopy(s []string) []string {
	out := make([]string, len(s))
	copy(out, s)
	sort.Strings(out)
	return out
}

func slackText(d *learn.DigestData) string {
	var b strings.Builder
	fmt.Fprintf(&b, "*Dandori fleet digest* (%dd window)\n", d.WindowDays)
	fmt.Fprintf(&b, "Runs: %d · Cost: $%.2f", d.TotalRuns, d.TotalCost)
	if d.FleetROI != nil {
		fmt.Fprintf(&b, " · Useful spend: %.0f%%", d.FleetROI.UsefulPct)
	}
	fmt.Fprintf(&b, "\nAI change-failure rate: %.0f%%", d.CFR.Value)
	if len(d.Spiked) > 0 {
		fmt.Fprintf(&b, "\n⚠️ Cost spikes today: %s", strings.Join(d.Spiked, ", "))
	}
	if d.KnowledgePublishedCount > 0 {
		fmt.Fprintf(&b, "\n📚 Tuần này team publish %d practice mới: %s",
			d.KnowledgePublishedCount, strings.Join(d.KnowledgePublishedTitles, "; "))
	}
	b.WriteString("\nTop agents:")
	for i, row := range d.Board {
		if i >= 5 {
			break
		}
		fmt.Fprintf(&b, "\n%d. %s — %s (%.0f%% useful, $%.2f)", i+1, row.AgentName, row.Grade.Letter, row.ROI.UsefulPct, row.CostUSD)
	}
	if len(d.Board) == 0 {
		b.WriteString(" none in window.")
	}
	return b.String()
}

func gmailText(d *learn.DigestData) string {
	var b strings.Builder
	fmt.Fprintf(&b, "Dandori fleet digest (%d-day window)\n\n", d.WindowDays)
	fmt.Fprintf(&b, "Total runs: %d\nTotal cost: $%.2f\n", d.TotalRuns, d.TotalCost)
	if d.FleetROI != nil {
		fmt.Fprintf(&b, "Useful spend: %.0f%% (%s)\n", d.FleetROI.UsefulPct, d.FleetROI.Formula)
	}
	fmt.Fprintf(&b, "AI change-failure rate: %.0f%% (%s)\n", d.CFR.Value, d.CFR.Formula)
	if len(d.Spiked) > 0 {
		fmt.Fprintf(&b, "\nCost spikes today: %s\n", strings.Join(d.Spiked, ", "))
	}
	if d.KnowledgePublishedCount > 0 {
		fmt.Fprintf(&b, "\nTuần này team publish %d practice mới:\n", d.KnowledgePublishedCount)
		for _, title := range d.KnowledgePublishedTitles {
			fmt.Fprintf(&b, "- %s\n", title)
		}
	}
	b.WriteString("\nLeaderboard:\n")
	for _, row := range d.Board {
		grade := row.Grade.Letter
		if row.Grade.Uncalibrated {
			grade += "*"
		}
		fmt.Fprintf(&b, "- %s: grade %s, %d runs, $%.2f, %.0f%% useful\n",
			row.AgentName, grade, row.Runs, row.CostUSD, row.ROI.UsefulPct)
	}
	if len(d.Board) == 0 {
		b.WriteString("(no agent activity in window)\n")
	}
	b.WriteString("\n— Dandori console\n")
	return b.String()
}
