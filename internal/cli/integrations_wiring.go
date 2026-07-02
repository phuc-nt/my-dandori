package cli

import (
	"context"
	"fmt"
	"log"
	"time"

	"github.com/phuc-nt/dandori/internal/config"
	"github.com/phuc-nt/dandori/internal/govern"
	"github.com/phuc-nt/dandori/internal/integrations"
	"github.com/phuc-nt/dandori/internal/integrations/confluence"
	"github.com/phuc-nt/dandori/internal/integrations/jira"
	"github.com/phuc-nt/dandori/internal/integrations/slack"
	"github.com/phuc-nt/dandori/internal/learn"
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
	c := jira.New(i.AtlassianSite, i.AtlassianEmail, i.AtlassianToken)
	key, err := c.CreateIssue(i.JiraProject, summary, description, []string{"dandori"})
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

// approvalExpiryWorker flips stale pending approvals to expired every minute.
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
			}()
		}
	}
}

func init() {
	registerWorker(jiraSyncWorker)
	registerWorker(slackWorker)
	registerWorker(approvalExpiryWorker)
	registerWorker(spikeWorker)
}
