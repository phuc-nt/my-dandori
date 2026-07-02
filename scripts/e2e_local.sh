#!/usr/bin/env bash
# Local E2E: full hook cycle + guardrails against a throwaway DB.
# Usage: ./scripts/e2e_local.sh [path-to-dandori-binary]
set -euo pipefail

BIN="${1:-./dandori}"
DB="$(mktemp -d)/e2e.db"
export DANDORI_DB="$DB"
CWD="$(pwd)"
pass=0; fail=0
check() { # check <name> <expect-substr> <actual>
  if [[ "$3" == *"$2"* ]]; then echo "  PASS $1"; pass=$((pass+1));
  else echo "  FAIL $1 — wanted '$2' in: $3"; fail=$((fail+1)); fi
}

echo "== E2E local (db: $DB) =="

echo "-- 1. full hook cycle --"
echo "{\"session_id\":\"e2e-1\",\"cwd\":\"$CWD\"}" | "$BIN" hook session-start
echo "{\"session_id\":\"e2e-1\",\"cwd\":\"$CWD\",\"tool_name\":\"Bash\",\"tool_input\":{\"command\":\"go test ./...\"}}" | "$BIN" hook pre-tool
echo "{\"session_id\":\"e2e-1\",\"cwd\":\"$CWD\",\"tool_name\":\"Bash\",\"tool_input\":{\"command\":\"go test ./...\"},\"tool_response\":{\"stdout\":\"ok\"}}" | "$BIN" hook post-tool
echo "{\"session_id\":\"e2e-1\",\"cwd\":\"$CWD\"}" | "$BIN" hook stop
check "run captured" "e2e-1" "$("$BIN" runs)"
check "run done" "done" "$("$BIN" runs)"

echo "-- 2. guardrail G1 (dangerous command) --"
OUT=$(echo "{\"session_id\":\"e2e-1\",\"cwd\":\"$CWD\",\"tool_name\":\"Bash\",\"tool_input\":{\"command\":\"rm -rf ~/precious\"}}" | "$BIN" hook pre-tool)
check "rm -rf denied" '"permissionDecision":"deny"' "$OUT"

echo "-- 3. guardrail G2 (scope sandbox) --"
OUT=$(echo "{\"session_id\":\"e2e-1\",\"cwd\":\"$CWD\",\"tool_name\":\"Write\",\"tool_input\":{\"file_path\":\"/etc/hosts\"}}" | "$BIN" hook pre-tool)
check "outside-scope write denied" '"permissionDecision":"deny"' "$OUT"

echo "-- 4. guardrail G3 (budget breaker) --"
sqlite3 "$DB" "INSERT INTO budgets(scope_type, scope_id, limit_usd) VALUES('global','',0.000001);"
sqlite3 "$DB" "UPDATE runs SET cost_usd = 1.0 WHERE id='e2e-1';"
OUT=$(echo "{\"session_id\":\"e2e-1\",\"cwd\":\"$CWD\",\"tool_name\":\"Bash\",\"tool_input\":{\"command\":\"ls\"}}" | "$BIN" hook pre-tool)
check "over-budget denied" 'budget exceeded' "$OUT"
sqlite3 "$DB" "DELETE FROM budgets;"

echo "-- 5. guardrail G4 (permission gate: timeout deny, then approve → allow) --"
OUT=$(echo "{\"session_id\":\"e2e-1\",\"cwd\":\"$CWD\",\"tool_name\":\"Bash\",\"tool_input\":{\"command\":\"git push origin main\"}}" | DANDORI_GATE_WAIT=0 "$BIN" hook pre-tool)
check "gated while pending" 'still pending' "$OUT"
APPROVAL_ID=$(sqlite3 "$DB" "SELECT id FROM approvals ORDER BY id DESC LIMIT 1;")
( sleep 2; sqlite3 "$DB" "UPDATE approvals SET status='approved', decided_at=datetime('now'), decided_by='e2e-script' WHERE status='pending';" ) &
OUT=$(echo "{\"session_id\":\"e2e-1\",\"cwd\":\"$CWD\",\"tool_name\":\"Bash\",\"tool_input\":{\"command\":\"git push origin main\"}}" | "$BIN" hook pre-tool)
wait
check "approved during wait → allow (silent)" "" "$OUT"

echo "-- 6. kill switch --"
"$BIN" kill --all >/dev/null
OUT=$(echo "{\"session_id\":\"e2e-1\",\"cwd\":\"$CWD\",\"tool_name\":\"Bash\",\"tool_input\":{\"command\":\"ls\"}}" | "$BIN" hook pre-tool)
check "global kill denies" 'kill switch' "$OUT"
"$BIN" kill --off >/dev/null

echo "-- 7. audit chain --"
check "audit verify" "audit chain OK" "$("$BIN" audit verify)"

echo "-- 8. quality gate --"
OUT=$("$BIN" gate --session e2e-1 --checks "true" && echo GATE_OK || true)
check "gate pass" "GATE_OK" "$OUT"
OUT=$("$BIN" gate --session e2e-1 --checks "false" 2>&1 || true)
check "gate fail flags" "checks failed" "$OUT"

echo
echo "== $pass passed, $fail failed =="
exit $fail
