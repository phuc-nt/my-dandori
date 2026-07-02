package cli

import (
	"context"
	"fmt"
	"log"
	"strings"
	"time"

	"github.com/phuc-nt/dandori/internal/config"
	"github.com/phuc-nt/dandori/internal/govern"
	"github.com/phuc-nt/dandori/internal/integrations"
	"github.com/phuc-nt/dandori/internal/integrations/confluence"
	"github.com/phuc-nt/dandori/internal/integrations/jira"
	"github.com/phuc-nt/dandori/internal/integrations/slack"
	"github.com/phuc-nt/dandori/internal/learn"
	"github.com/phuc-nt/dandori/internal/redact"
	"github.com/phuc-nt/dandori/internal/store"
	"github.com/phuc-nt/dandori/internal/web"
)

// wireIntegrations connects the external legs to the console: flag → Jira
// ticket sink, plus (via workers below) Slack alerts and approval bridging.
func wireIntegrations(cfg *config.Config, st *store.Store, srv *web.Server) {
	srv.FlagSink = func(flagID int64) {
		defer recoverLog("flag sink")
		if err := flagToJira(cfg, st, flagID); err != nil {
			log.Println("flag→jira:", err)
		}
	}
	if i := cfg.Integrations; i.AtlassianSite != "" && i.ConfluenceSpaceID != "" {
		reporter := &confluence.Reporter{
			St:      st,
			Client:  confluence.New(i.AtlassianSite, i.AtlassianEmail, i.AtlassianToken),
			Guard:   &integrations.Guard{Cfg: cfg, St: st},
			SpaceID: i.ConfluenceSpaceID, Window: cfg.LearnWindowDays,
		}
		srv.ReportSink = func() (string, error) { return reporter.Post(cfg.UserName) }
	}
}

// flagToJira creates a Jira ticket for a flag (UC1) and stores the key back.
func flagToJira(cfg *config.Config, st *store.Store, flagID int64) error {
	i := cfg.Integrations
	var runID, reason string
	if err := st.DB.QueryRow(`SELECT COALESCE(run_id,''), COALESCE(reason,'') FROM flags WHERE id = ?`, flagID).
		Scan(&runID, &reason); err != nil {
		return err
	}
	summary := fmt.Sprintf("[dandori] flagged run %.12s: %.60s", runID, reason)
	description := fmt.Sprintf("Dandori flagged run %s.\nReason: %s\nConsole: http://%s/runs/%s",
		runID, reason, cfg.Listen, runID)
	guard := &integrations.Guard{Cfg: cfg, St: st}
	if !guard.Allow("create jira issue", summary) {
		return nil
	}
	if i.AtlassianSite == "" || i.AtlassianToken == "" {
		return fmt.Errorf("atlassian credentials not configured")
	}
	labels := []string{"dandori"}
	if strings.HasPrefix(reason, "low grade") {
		labels = append(labels, "dandori-governance")
	}
	c := jira.New(i.AtlassianSite, i.AtlassianEmail, i.AtlassianToken)
	key, err := c.CreateIssue(i.JiraProject, summary, description, labels)
	if err != nil {
		return err
	}
	if _, err := st.DB.Exec(`UPDATE flags SET jira_key = ? WHERE id = ?`, key, flagID); err != nil {
		return err
	}
	a := &govern.Audit{St: st, Actor: cfg.UserName}
	_, err = a.Append("flag_to_jira", fmt.Sprintf("flag:%d", flagID), key)
	return err
}

// Background workers for serve. Each recovers from panics so one integration
// can never take the console down.

func jiraSyncWorker(ctx context.Context, cfg *config.Config, st *store.Store) {
	i := cfg.Integrations
	if i.AtlassianSite == "" || i.AtlassianToken == "" {
		return
	}
	c := jira.New(i.AtlassianSite, i.AtlassianEmail, i.AtlassianToken)
	run := func() {
		defer recoverLog("jira sync")
		if n, err := jira.SyncIssues(st, c, i.JiraProject); err != nil {
			log.Println("jira sync:", err)
		} else {
			log.Printf("jira sync: %d issues", n)
		}
	}
	run()
	t := time.NewTicker(15 * time.Minute)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			run()
		}
	}
}

func slackWorker(ctx context.Context, cfg *config.Config, st *store.Store) {
	i := cfg.Integrations
	if i.SlackXoxc == "" || i.SlackChannel == "" {
		return
	}
	client := slack.New(i.SlackXoxc, i.SlackXoxd)
	guard := &integrations.Guard{Cfg: cfg, St: st}
	alerter := &slack.Alerter{St: st, Client: client, Guard: guard, Channel: i.SlackChannel}
	bridge := &slack.ApprovalBridge{St: st, Client: client, Guard: guard,
		Channel: i.SlackChannel, ConsoleURL: "http://" + cfg.Listen, Approvers: cfg.Approvers}
	t := time.NewTicker(5 * time.Second)
	defer t.Stop()
	lastAlert := time.Time{}
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			func() {
				defer recoverLog("slack worker")
				bridge.Tick()
				if time.Since(lastAlert) >= 30*time.Second {
					lastAlert = time.Now()
					if err := alerter.Dispatch(); err != nil {
						log.Println("slack alerts:", err)
					}
				}
			}()
		}
	}
}

func recoverLog(name string) {
	if r := recover(); r != nil {
		log.Printf("%s: recovered from panic: %v", name, r)
	}
}

// spikeWorker looks for per-agent daily cost anomalies every 10 minutes;
// detected spikes become events the Slack alerter picks up.
func spikeWorker(ctx context.Context, cfg *config.Config, st *store.Store) {
	t := time.NewTicker(10 * time.Minute)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			func() {
				defer recoverLog("spike detector")
				if spiked, err := learn.DetectSpikes(st); err != nil {
					log.Println("spike detector:", err)
				} else if len(spiked) > 0 {
					log.Printf("cost spikes detected: %v", spiked)
				}
			}()
		}
	}
}

// approvalExpiryWorker flips stale pending approvals to expired every minute
// and escalates ones sitting unanswered past 4× the gate wait (an alert
// BEFORE the TTL silently expires them).
func approvalExpiryWorker(ctx context.Context, cfg *config.Config, st *store.Store) {
	eng := govern.NewEngine(cfg, st)
	t := time.NewTicker(time.Minute)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			func() {
				defer recoverLog("approval expiry")
				eng.ExpireStale()
				escalateStalePending(cfg, st)
			}()
		}
	}
}

// escalateStalePending emits one approval_escalation event per stuck
// approval (the Slack alerter delivers it).
func escalateStalePending(cfg *config.Config, st *store.Store) {
	slaSeconds := cfg.GateWaitSeconds * 4
	if slaSeconds <= 0 {
		return
	}
	cutoff := time.Now().UTC().Add(-time.Duration(slaSeconds) * time.Second).Format(time.RFC3339)
	rows, err := st.DB.Query(`SELECT id, action FROM approvals WHERE status = 'pending' AND requested_at < ?`, cutoff)
	if err != nil {
		return
	}
	defer rows.Close()
	type stale struct {
		id     int64
		action string
	}
	var stales []stale
	for rows.Next() {
		var s stale
		if rows.Scan(&s.id, &s.action) == nil {
			stales = append(stales, s)
		}
	}
	for _, s := range stales {
		key := fmt.Sprintf("escalated:%d", s.id)
		if st.Setting(key) != "" {
			continue
		}
		_ = st.SetSetting(key, store.Now())
		_, _ = st.DB.Exec(`INSERT INTO events(run_id, ts, kind, tool_name, ok, payload)
			VALUES(NULL, ?, 'approval_escalation', '', 0, ?)`, store.Now(),
			redact.String(fmt.Sprintf("approval #%d unanswered past SLA: %.80s — decide at http://%s/reviews", s.id, s.action, cfg.Listen)))
	}
}

func init() {
	registerWorker(jiraSyncWorker)
	registerWorker(slackWorker)
	registerWorker(approvalExpiryWorker)
	registerWorker(spikeWorker)
}
