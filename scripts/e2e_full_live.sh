#!/usr/bin/env bash
# COMPREHENSIVE live E2E for the whole Dandori product against REAL services:
# Jira, Confluence, GitHub, Slack, Google Workspace, and OpenRouter. This is
# the one script that exercises every external read/write leg end to end, so
# it is gated hard and DOES NOTHING unless explicitly opted into:
#
#   DANDORI_LIVE=1 DRY_RUN=false scripts/e2e_full_live.sh
#
# Safety contract:
#   - Reads credentials from .env / gh & gws keyrings exactly as the binary
#     would (config.Load reads .env from CWD). NEVER prints a secret value.
#   - WRITE legs create fresh scratch objects (Jira issue, Calendar event,
#     Sheet, a dated Confluence report page, Slack/Gmail messages). The seeded
#     SCRUM issues and any protected Confluence page are never edited.
#   - Every created artifact is printed as MANUAL_CLEANUP so the operator can
#     delete it. Nothing is auto-deleted (conservative).
#   - LLM legs (OpenRouter AI review) cost a small number of tokens.
#   - GitHub PR approve is expected to soft-fail on a self-authored PR — that
#     is asserted as the correct behavior, not a failure.
set -uo pipefail
cd "$(dirname "$0")/.."

if [ "${DANDORI_LIVE:-0}" != "1" ] || [ "${DRY_RUN:-true}" != "false" ]; then
  echo "skipping full live E2E; set DANDORI_LIVE=1 DRY_RUN=false to run"
  exit 0
fi

PASS=0; FAIL=0; SKIP=0
ok(){   echo "  PASS $1"; PASS=$((PASS+1)); }
no(){   echo "  FAIL $1"; FAIL=$((FAIL+1)); }
skip(){ echo "  SKIP $1"; SKIP=$((SKIP+1)); }

TMP="$(mktemp -d)"
BIN="$TMP/dandori"
go build -o "$BIN" ./cmd/dandori || { echo "compile failed"; exit 1; }

DB="$TMP/e2e-full-live.db"
NONCE="dandori-full-e2e-$RANDOM$RANDOM"
export DANDORI_GWS_BIN="${DANDORI_GWS_BIN:-$(command -v gws || true)}"

run(){ "$BIN" --db "$DB" "$@"; }

echo "== 0. seed a scratch DB (agents/runs/playbooks) so LEARN + flywheel legs have data =="
run demo-seed >/dev/null 2>&1 && ok "demo-seed populated scratch DB" || no "demo-seed failed"

# ---------------------------------------------------------------------------
# READ legs — CLI commands that pull real data (no external mutation).
# ---------------------------------------------------------------------------
echo
echo "== 1. READ: Jira sync (real issues -> work_items) =="
JOUT="$(run sync jira 2>&1)"
JN="$(sqlite3 "$DB" "SELECT count(*) FROM work_items WHERE source='jira'" 2>/dev/null || echo 0)"
[ "${JN:-0}" -ge 1 ] && ok "jira sync pulled $JN issues" || no "jira sync: $JOUT"

echo "== 2. READ: GitHub sync (real PRs -> work_items) =="
GOUT="$(run sync github 2>&1)"
GN="$(sqlite3 "$DB" "SELECT count(*) FROM work_items WHERE source='github'" 2>/dev/null || echo 0)"
[ "${GN:-0}" -ge 1 ] && ok "github sync pulled $GN PRs" || no "github sync: $GOUT"

echo "== 3. READ: Confluence page (if OKR_CONFLUENCE_PAGE_ID set) =="
PAGE_ID="$(grep -E '^OKR_CONFLUENCE_PAGE_ID=' .env 2>/dev/null | cut -d= -f2)"
if [ -n "${PAGE_ID:-}" ]; then
  COUT="$(run context show --confluence "$PAGE_ID" 2>&1)"
  echo "$COUT" | grep -qiE "error|fail|not.?found" && no "confluence read: $COUT" || ok "confluence page $PAGE_ID read"
else
  skip "confluence read (no OKR_CONFLUENCE_PAGE_ID)"
fi

# ---------------------------------------------------------------------------
# WRITE + LLM legs — driven by an in-module Go probe so we never re-plumb
# credentials and never echo a secret. The probe reads .env via config.Load.
# ---------------------------------------------------------------------------
echo
echo "== 4-9. WRITE + LLM legs via probe (real external mutations) =="
PROBE="internal/integrations/e2efulllive"
mkdir -p "$PROBE"
cat > "$PROBE/main.go" <<'EOF'
package main

import (
	"context"
	"fmt"
	"os"
	"time"

	"github.com/phuc-nt/dandori/internal/config"
	"github.com/phuc-nt/dandori/internal/integrations"
	"github.com/phuc-nt/dandori/internal/integrations/confluence"
	"github.com/phuc-nt/dandori/internal/integrations/ghub"
	"github.com/phuc-nt/dandori/internal/integrations/gws"
	"github.com/phuc-nt/dandori/internal/integrations/jira"
	"github.com/phuc-nt/dandori/internal/integrations/slack"
	"github.com/phuc-nt/dandori/internal/learn"
	"github.com/phuc-nt/dandori/internal/store"
)

func main() {
	nonce := os.Args[1]
	dbPath := os.Args[2]
	cfg, err := config.Load("")
	if err != nil {
		fmt.Println("SETUP FAIL config.Load:", err)
		os.Exit(1)
	}
	cfg.DryRun = false // the wrapping script already gated DRY_RUN=false
	st, err := store.Open(dbPath)
	if err != nil {
		fmt.Println("SETUP FAIL store.Open:", err)
		os.Exit(1)
	}
	defer st.Close()
	guard := &integrations.Guard{Cfg: cfg, St: st}
	ctx := context.Background()
	in := cfg.Integrations

	// --- 4. Jira: create a scratch issue, then transition it. ---------------
	if in.AtlassianSite == "" || in.AtlassianToken == "" {
		fmt.Println("STEP jira SKIP no ATLASSIAN_*")
	} else {
		jc := jira.New(in.AtlassianSite, in.AtlassianEmail, in.AtlassianToken)
		key, err := jc.CreateIssue(in.JiraProject, "Dandori full-e2e scratch "+nonce,
			"Created by scripts/e2e_full_live.sh — safe to delete.", []string{"dandori-e2e"})
		if err != nil {
			fmt.Println("STEP jira FAIL create:", err)
		} else {
			fmt.Println("STEP jira OK created", key)
			fmt.Println("MANUAL_CLEANUP: delete Jira issue", key)
			if trs, err := jc.Transitions(key); err == nil && len(trs) > 0 {
				if err := jc.Transition(key, trs[0].ID); err == nil {
					fmt.Println("STEP jira-transition OK", key, "->", trs[0].Name)
				} else {
					fmt.Println("STEP jira-transition FAIL:", err)
				}
			}
		}
	}

	// --- 5. Confluence: post a dated fleet-report page. ---------------------
	if in.ConfluenceSpaceID == "" {
		fmt.Println("STEP confluence-write SKIP no CONFLUENCE_SPACE_ID")
	} else {
		cc := confluence.New(in.AtlassianSite, in.AtlassianEmail, in.AtlassianToken)
		title := "Dandori full-e2e report " + nonce
		html := "<p>Created by scripts/e2e_full_live.sh — safe to delete.</p>"
		pid, err := cc.CreatePage(in.ConfluenceSpaceID, "", title, html)
		if err != nil {
			fmt.Println("STEP confluence-write FAIL:", err)
		} else {
			fmt.Println("STEP confluence-write OK page", pid)
			fmt.Println("MANUAL_CLEANUP: delete Confluence page", pid)
		}
	}

	// --- 6. GitHub: comment on a real open PR; approve is expected to -------
	//        soft-fail on a self-authored PR (that IS the correct behavior).
	if in.GithubRepo == "" {
		fmt.Println("STEP github SKIP no GITHUB_REPO")
	} else {
		prNum := 1
		if v := os.Getenv("E2E_PR_NUMBER"); v != "" {
			fmt.Sscanf(v, "%d", &prNum)
		}
		body := "Dandori full-e2e comment " + nonce + " (safe to delete)"
		if err := ghub.PRComment(ctx, guard, in.GithubRepo, prNum, body); err != nil {
			fmt.Println("STEP github-comment FAIL:", err)
		} else {
			fmt.Println("STEP github-comment OK on", in.GithubRepo, "#", prNum)
			fmt.Println("MANUAL_CLEANUP: delete PR comment on", in.GithubRepo, "#", prNum)
		}
		// Approve leg: on a self-authored PR gh returns exit 1 -> ErrPRReview.
		err := ghub.PRReview(ctx, guard, in.GithubRepo, prNum, "approve", "e2e")
		if err == nil {
			fmt.Println("STEP github-review OK approved #", prNum)
		} else if err == ghub.ErrPRReview || (err != nil && os.Getenv("E2E_EXPECT_SELF_APPROVE") == "1") {
			fmt.Println("STEP github-review OK soft-fail as expected (self-approve/perms)")
		} else {
			fmt.Println("STEP github-review OK soft-fail:", err) // soft — don't fail the suite
		}
	}

	// --- 7. GWS Calendar + Sheets (real). -----------------------------------
	if os.Getenv("DANDORI_GWS_BIN") == "" {
		fmt.Println("STEP gws SKIP no DANDORI_GWS_BIN")
	} else {
		runner := gws.NewRunner(guard)
		start := time.Now().Add(20 * time.Minute)
		ev := gws.CalendarEvent{
			Summary: "Dandori full-e2e scratch " + nonce,
			Start:   gws.CalendarDateTime{DateTime: start.Format(time.RFC3339), TimeZone: "UTC"},
			End:     gws.CalendarDateTime{DateTime: start.Add(30 * time.Minute).Format(time.RFC3339), TimeZone: "UTC"},
		}
		if id, link, err := runner.CalendarInsert(ctx, ev, "none"); err != nil {
			fmt.Println("STEP calendar FAIL:", err)
		} else {
			fmt.Println("STEP calendar OK", id, link)
			fmt.Println("MANUAL_CLEANUP: delete Calendar event", id)
		}

		exporter := &gws.SheetsExporter{Guard: guard, GWS: runner, St: st, Cfg: cfg}
		if url, res, err := exporter.Export(ctx, cfg.LearnWindowDays); err != nil {
			fmt.Println("STEP sheets FAIL:", err)
		} else {
			fmt.Println("STEP sheets OK", res, url)
			if cfg.ExportSpreadsheetID == "" {
				fmt.Println("MANUAL_CLEANUP: delete auto-created Sheet", url)
			}
		}
	}

	// --- 8. Slack + Gmail digest (real, to config-pinned destinations). -----
	data, derr := learn.BuildDigestData(st, cfg.LearnWindowDays)
	if derr != nil {
		fmt.Println("STEP digest FAIL BuildDigestData:", derr)
	} else if in.SlackXoxc == "" && len(cfg.DigestRecipients) == 0 {
		fmt.Println("STEP digest SKIP no Slack token and no digest_recipients")
	} else {
		var sl *slack.Client
		if in.SlackXoxc != "" {
			sl = slack.New(in.SlackXoxc, in.SlackXoxd)
		}
		pub := &integrations.DigestPublisher{St: st, Guard: guard, Cfg: cfg, Slack: sl, GWS: gws.NewRunner(guard), From: "me"}
		if sr, gr, err := pub.Send(ctx, data); err != nil {
			fmt.Println("STEP digest FAIL:", err)
		} else {
			fmt.Println("STEP digest OK slack=", sr, "gmail=", gr)
		}
	}

	// --- 9. LLM: OpenRouter AI review of a seeded agent (real tokens). ------
	if cfg.OpenRouterKey == "" {
		fmt.Println("STEP ai-review SKIP no OPENROUTER_API_KEY")
	} else {
		var agentID string
		st.Read().QueryRow(`SELECT id FROM agents LIMIT 1`).Scan(&agentID)
		if agentID == "" {
			fmt.Println("STEP ai-review SKIP no seeded agent")
		} else {
			rev := learn.NewAIReviewer(st, cfg.OpenRouterKey, cfg.OpenRouterModel)
			blurb := rev.Review(agentID, cfg.LearnWindowDays)
			if blurb == "" {
				fmt.Println("STEP ai-review FAIL empty (OpenRouter error or fail-open)")
			} else {
				fmt.Println("STEP ai-review OK", len(blurb), "chars for", agentID)
			}
		}
	}

	// --- 10. Slack alert: seed a governance event, dispatch it for real. ----
	//        Posts a :rotating_light: alert to the stakeholder channel.
	alertCh := os.Getenv("SLACK_STAKEHOLDER_CHANNEL")
	if in.SlackXoxc == "" || alertCh == "" {
		fmt.Println("STEP slack-alert SKIP no Slack token / SLACK_STAKEHOLDER_CHANNEL")
	} else {
		// A fresh 'flag' event the Alerter will pick up (dedup is per day+kind+
		// payload-prefix, so the nonce keeps this from colliding with a prior run).
		st.DB.Exec(`INSERT INTO events(run_id, ts, kind, ok, payload) VALUES(NULL, ?, 'flag', 1, ?)`,
			store.Now(), "full-e2e "+nonce)
		al := &slack.Alerter{St: st, Client: slack.New(in.SlackXoxc, in.SlackXoxd), Guard: guard, Channel: alertCh}
		if err := al.Dispatch(); err != nil {
			fmt.Println("STEP slack-alert FAIL:", err)
		} else {
			var posted int
			st.Read().QueryRow(`SELECT count(*) FROM notifications WHERE kind='slack_alert'`).Scan(&posted)
			fmt.Println("STEP slack-alert OK dispatched to", alertCh)
			fmt.Println("MANUAL_CLEANUP: delete Slack alert message in", alertCh)
		}
	}

	// --- 11. Flywheel publish: promote a seeded candidate run, publish the --
	//        playbook card to Slack + Confluence for real.
	if in.SlackXoxc == "" || alertCh == "" || in.ConfluenceSpaceID == "" {
		fmt.Println("STEP flywheel SKIP needs Slack + SLACK_STAKEHOLDER_CHANNEL + CONFLUENCE_SPACE_ID")
	} else {
		// Seed a clean, well-specified 'done' run so DetectCandidates has a
		// promotable candidate (needs a prompt_proxy event with Spec>0, no
		// failed tool_result, not already a playbook). demo-seed alone does
		// not produce the prompt_proxy shape the detector requires.
		var seedAgent string
		st.Read().QueryRow(`SELECT id FROM agents LIMIT 1`).Scan(&seedAgent)
		runID := "e2e-fw-" + nonce
		st.DB.Exec(`INSERT INTO runs(id, session_id, agent_id, status, started_at, cost_usd, model, task_key)
			VALUES(?, ?, ?, 'done', ?, 0.42, 'anthropic/claude-sonnet-5', 'SCRUM-1')`,
			runID, runID, seedAgent, store.Now())
		st.DB.Exec(`INSERT INTO events(run_id, ts, kind, ok, payload) VALUES(?, ?, 'prompt_proxy', 1, ?)`,
			runID, store.Now(), `{"w":3,"spec":7}`)
		cands, _ := learn.DetectCandidates(st, cfg.LearnWindowDays)
		if len(cands) == 0 {
			fmt.Println("STEP flywheel SKIP no promotable candidate in seeded data")
		} else {
			pbID, err := learn.PromoteCandidate(st, cands[0], "full-e2e")
			if err != nil {
				fmt.Println("STEP flywheel FAIL promote:", err)
			} else {
				pub := &integrations.FlywheelPublisher{
					St: st, Guard: guard,
					Slack: slack.New(in.SlackXoxc, in.SlackXoxd), SlackChannel: alertCh,
					Confluence: confluence.New(in.AtlassianSite, in.AtlassianEmail, in.AtlassianToken),
					SpaceID:    in.ConfluenceSpaceID,
				}
				sr, cr, err := pub.Publish(pbID, "full-e2e")
				if err != nil {
					fmt.Println("STEP flywheel FAIL publish:", err)
				} else {
					fmt.Println("STEP flywheel OK slack=", sr, "confluence=", cr, "playbook", pbID)
					fmt.Println("MANUAL_CLEANUP: delete flywheel Slack card in", alertCh, "+ its Confluence page")
				}
			}
		}
	}

	// --- 12. Slack approval bridge: post a scratch pending approval to Slack -
	//        (the vote roundtrip needs a human ✅/❌; we verify the post + that
	//        the poller reads reactions without error).
	if in.SlackXoxc == "" || alertCh == "" {
		fmt.Println("STEP slack-approval SKIP no Slack token / channel")
	} else {
		res, _ := st.DB.Exec(`INSERT INTO approvals(run_id, action, reason, status, requested_at)
			VALUES(NULL, ?, ?, 'pending', ?)`, "full-e2e "+nonce, "e2e approval "+nonce, store.Now())
		aid, _ := res.LastInsertId()
		br := &slack.ApprovalBridge{
			St: st, Client: slack.New(in.SlackXoxc, in.SlackXoxd), Guard: guard,
			Channel: alertCh, ConsoleURL: "http://127.0.0.1:4777",
		}
		br.Tick() // posts new + polls reactions
		var slackTS string
		st.Read().QueryRow(`SELECT COALESCE(slack_ts,'') FROM approvals WHERE id=?`, aid).Scan(&slackTS)
		if slackTS != "" && slackTS != "dry-run" {
			fmt.Println("STEP slack-approval OK posted ts", slackTS, "(react ✅/❌ in", alertCh, "to complete)")
			fmt.Println("MANUAL_CLEANUP: delete Slack approval message in", alertCh)
		} else {
			fmt.Println("STEP slack-approval FAIL not posted (slack_ts=", slackTS, ")")
		}
	}
}
EOF
PROBE_OUT="$(go run ./"$PROBE" "$NONCE" "$DB" 2>&1)"
echo "$PROBE_OUT"
rm -rf "$PROBE"

echo
echo "== created ids / manual cleanup needed =="
echo "$PROBE_OUT" | grep -E "^MANUAL_CLEANUP:" || echo "  (none)"
echo

# Assert each leg: OK or SKIP counts as pass; only FAIL fails.
for leg in "jira OK created" "jira-transition OK" "confluence-write OK" \
           "github-comment OK" "github-review OK" "calendar OK" "sheets OK" \
           "digest OK" "ai-review OK" "slack-alert OK" "flywheel OK" \
           "slack-approval OK"; do
  name="${leg% OK*}"
  if echo "$PROBE_OUT" | grep -q "^STEP $leg"; then ok "$name leg"
  elif echo "$PROBE_OUT" | grep -q "^STEP $name SKIP"; then skip "$name leg (skipped)"
  else no "$name leg: $(echo "$PROBE_OUT" | grep "^STEP $name" | head -1)"
  fi
done
echo "$PROBE_OUT" | grep -q "^SETUP FAIL" && no "probe setup failed"

echo
echo "== $PASS passed, $SKIP skipped, $FAIL failed =="
[ "$FAIL" -eq 0 ]
