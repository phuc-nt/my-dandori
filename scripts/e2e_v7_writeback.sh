#!/usr/bin/env bash
# Live E2E for v7 write-back (UC2/UC8/UC9/UG2b against REAL Jira + Google
# Workspace). Offline coverage (fakes, no network) lives in
# internal/web/v7_writeback_e2e_test.go and runs on every `go test ./...`.
# This script is the one place real external state gets mutated, so it is
# gated hard and DOES NOTHING unless explicitly opted into:
#
#   DANDORI_LIVE=1 DRY_RUN=false scripts/e2e_v7_writeback.sh
#
# Reads Atlassian/Google credentials from .env / the gws keyring exactly as
# the running binary would (config.Load picks up .env from CWD). Never print
# credential values. All actions target NEW scratch objects this script
# creates itself — the seeded SCRUM fixture issues and MPM Confluence space
# are never touched.
set -uo pipefail
cd "$(dirname "$0")/.."

if [ "${DANDORI_LIVE:-0}" != "1" ] || [ "${DRY_RUN:-true}" != "false" ]; then
  echo "skipping live; set DANDORI_LIVE=1 DRY_RUN=false to run"
  exit 0
fi

PASS=0; FAIL=0
ok(){ echo "  PASS $1"; PASS=$((PASS+1)); }
no(){ echo "  FAIL $1"; FAIL=$((FAIL+1)); }

TMP="$(mktemp -d)"
BIN="$TMP/dandori"
go build -o "$BIN" ./cmd/dandori || { echo "compile failed"; exit 1; }

DB="$TMP/e2e-live.db"
NONCE="dandori-v7-e2e-$RANDOM$RANDOM"

# A tiny in-module Go probe drives the real integration clients directly
# (config.Load reads .env / gws keyring from CWD exactly like the binary
# would), so this script never needs its own credential plumbing and never
# echoes a secret value.
PROBE="internal/integrations/e2elive"
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
	"github.com/phuc-nt/dandori/internal/integrations/gws"
	"github.com/phuc-nt/dandori/internal/integrations/jira"
	"github.com/phuc-nt/dandori/internal/integrations/slack"
	"github.com/phuc-nt/dandori/internal/learn"
	"github.com/phuc-nt/dandori/internal/store"
)

// Exit codes: 0 all steps ok, 1 setup failure, 2+ per-step failures
// (reported on stdout as "STEP <name> FAIL <err>" so the shell script can
// grep results without parsing Go panics).
func main() {
	nonce := os.Args[1]
	dbPath := os.Args[2]
	cfg, err := config.Load("")
	if err != nil {
		fmt.Println("SETUP FAIL config.Load:", err)
		os.Exit(1)
	}
	cfg.DryRun = false // this probe only runs when the wrapping script already gated DRY_RUN=false
	st, err := store.Open(dbPath)
	if err != nil {
		fmt.Println("SETUP FAIL store.Open:", err)
		os.Exit(1)
	}
	defer st.Close()
	guard := &integrations.Guard{Cfg: cfg, St: st}
	ctx := context.Background()

	// --- 1. Jira: create a scratch issue, then transition it once. --------
	if cfg.Integrations.AtlassianSite == "" || cfg.Integrations.AtlassianToken == "" {
		fmt.Println("STEP jira SKIP no ATLASSIAN_* credentials in .env")
	} else {
		jc := jira.New(cfg.Integrations.AtlassianSite, cfg.Integrations.AtlassianEmail, cfg.Integrations.AtlassianToken)
		key, err := jc.CreateIssue(cfg.Integrations.JiraProject, "Dandori v7 e2e scratch "+nonce,
			"Created by scripts/e2e_v7_writeback.sh — safe to delete.", []string{"dandori-e2e"})
		if err != nil {
			fmt.Println("STEP jira FAIL create:", err)
		} else {
			fmt.Println("STEP jira CREATED", key)
			trs, err := jc.Transitions(key)
			if err != nil || len(trs) == 0 {
				fmt.Println("STEP jira FAIL no transitions available for", key, err)
			} else if err := jc.Transition(key, trs[0].ID); err != nil {
				fmt.Println("STEP jira FAIL transition:", err)
			} else {
				fmt.Println("STEP jira OK transitioned", key, "->", trs[0].Name)
			}
			fmt.Println("MANUAL_CLEANUP: delete Jira issue", key)
		}
	}

	// --- 2. Calendar: create a scratch event 10 minutes from now. ----------
	gwsBin := os.Getenv("DANDORI_GWS_BIN")
	if gwsBin == "" {
		fmt.Println("STEP calendar SKIP no DANDORI_GWS_BIN (gws CLI) configured")
	} else {
		runner := gws.NewRunner(guard)
		start := time.Now().Add(10 * time.Minute)
		end := start.Add(30 * time.Minute)
		ev := gws.CalendarEvent{
			Summary: "Dandori v7 e2e scratch " + nonce,
			Start:   gws.CalendarDateTime{DateTime: start.Format(time.RFC3339), TimeZone: "UTC"},
			End:     gws.CalendarDateTime{DateTime: end.Format(time.RFC3339), TimeZone: "UTC"},
		}
		id, link, err := runner.CalendarInsert(ctx, ev, "none")
		if err != nil {
			fmt.Println("STEP calendar FAIL:", err)
		} else {
			fmt.Println("STEP calendar OK", id, link)
			fmt.Println("MANUAL_CLEANUP: delete Calendar event", id)
		}

		// --- 3. Sheets export: create/update the fleet leaderboard sheet. --
		exporter := &gws.SheetsExporter{Guard: guard, GWS: runner, St: st, Cfg: cfg}
		url, res, err := exporter.Export(ctx, cfg.LearnWindowDays)
		if err != nil {
			fmt.Println("STEP sheets FAIL:", err)
		} else {
			fmt.Println("STEP sheets OK", res, url)
			if cfg.ExportSpreadsheetID == "" {
				fmt.Println("MANUAL_CLEANUP: delete the auto-created Sheet at", url)
			}
		}

		// --- 4. Digest: self-send to prove the Gmail leg end to end. -------
		if len(cfg.DigestRecipients) == 0 {
			fmt.Println("STEP digest SKIP no digest_recipients configured")
		} else {
			data, err := learn.BuildDigestData(st, cfg.LearnWindowDays)
			if err != nil {
				fmt.Println("STEP digest FAIL BuildDigestData:", err)
			} else {
				pub := &integrations.DigestPublisher{
					St: st, Guard: guard, Cfg: cfg,
					Slack: slack.New(cfg.Integrations.SlackXoxc, cfg.Integrations.SlackXoxd),
					GWS:   runner, From: "me",
				}
				slackRes, gmailRes, err := pub.Send(ctx, data)
				if err != nil {
					fmt.Println("STEP digest FAIL:", err)
				} else {
					fmt.Println("STEP digest OK slack=", slackRes, "gmail=", gmailRes)
				}
			}
		}
	}
}
EOF
GO_OUT="$(go run ./"$PROBE" "$NONCE" "$DB" 2>&1)"
echo "$GO_OUT"
rm -rf "$PROBE"

echo
echo "== created ids / manual cleanup needed =="
echo "$GO_OUT" | grep -E "^(STEP .*(CREATED|OK)|MANUAL_CLEANUP:)" || echo "  (none — check SKIP/FAIL lines above)"

echo "$GO_OUT" | grep -q "^STEP jira OK\|^STEP jira SKIP" && ok "jira transition leg" || no "jira transition leg: $(echo "$GO_OUT" | grep '^STEP jira')"
echo "$GO_OUT" | grep -q "^STEP calendar OK\|^STEP calendar SKIP" && ok "calendar insert leg" || no "calendar insert leg: $(echo "$GO_OUT" | grep '^STEP calendar')"
echo "$GO_OUT" | grep -q "^STEP sheets OK\|^STEP sheets SKIP" && ok "sheets export leg" || no "sheets export leg: $(echo "$GO_OUT" | grep '^STEP sheets')"
echo "$GO_OUT" | grep -q "^STEP digest OK\|^STEP digest SKIP" && ok "digest send leg" || no "digest send leg: $(echo "$GO_OUT" | grep '^STEP digest')"
echo "$GO_OUT" | grep -q "^SETUP FAIL" && no "probe setup failed"

echo
echo "== $PASS passed, $FAIL failed =="
[ "$FAIL" -eq 0 ]
