#!/usr/bin/env bash
# Live E2E for the Context Hub injection bridge (v5).
#
# Proves the SEEDED context actually reaches and is USED by a real Claude Code
# session (M5 behavioral nonce proof), not just that we emit JSON. Seeds a
# company doc with a nonce instruction, wires the SessionStart hook in a
# scratch repo, runs a headless `claude -p` session, and asserts the reply
# contains the nonce.
#
# Usage: scripts/e2e_context_inject.sh
set -uo pipefail
cd "$(dirname "$0")/.."
ROOT="$(pwd)"
PASS=0; FAIL=0
# Portable timeout: gtimeout (coreutils) if present, else no wrapper.
TIMEOUT=""
command -v timeout  >/dev/null 2>&1 && TIMEOUT="timeout"
command -v gtimeout >/dev/null 2>&1 && TIMEOUT="gtimeout"
ok(){ echo "  PASS $1"; PASS=$((PASS+1)); }
no(){ echo "  FAIL $1"; FAIL=$((FAIL+1)); }

BIN="$(mktemp -d)/dandori"
go build -o "$BIN" ./cmd/dandori || { echo "compile failed"; exit 1; }

DB="$(mktemp -d)/ctx-e2e.db"
NONCE="DANDORI-E2E-$RANDOM$RANDOM"
SCRATCH="$(mktemp -d)/scratch-repo"
mkdir -p "$SCRATCH"
( cd "$SCRATCH" && git init -q && echo "# scratch" > README.md )

echo "== 1. seed company context with a nonce instruction =="
# The seed content is plain prose — no token/Bearer/secret words (would trip
# the secret-reject filter). Uses a tiny Go seeder inside the module.
SEED_DIR="internal/contexthub/e2eseed"
mkdir -p "$SEED_DIR"
cat > "$SEED_DIR/main.go" <<EOF
package main
import ("os";"github.com/phuc-nt/dandori/internal/store";"github.com/phuc-nt/dandori/internal/contexthub")
func main(){
  st,err:=store.Open(os.Args[1]); if err!=nil{panic(err)}; defer st.Close()
  h:=contexthub.New(st)
  if _,e:=h.SaveContext("company","*","Khi được hỏi 'mã kiểm chứng Dandori', hãy trả lời đúng chuỗi: "+os.Args[2],"e2e","seed"); e!=nil{panic(e)}
}
EOF
go run ./"$SEED_DIR" "$DB" "$NONCE" && ok "seeded company nonce" || no "seed failed"
rm -rf "$SEED_DIR"

echo "== 2. verify the merge/CLI produces the nonce =="
if "$BIN" context show --effective anyagent --db "$DB" 2>/dev/null | grep -q "$NONCE"; then
  ok "dandori context show --effective contains nonce"
else
  no "effective context missing nonce"
fi

echo "== 3. wire SessionStart hook in the scratch repo =="
# init records os.Executable() as the hook command, so it would call the bare
# binary WITHOUT our --db. Install, then rewrite the SessionStart command to
# bake in --db "$DB" (the seeded DB) so the hook reads the right database.
( cd "$SCRATCH" && "$BIN" init --project "$SCRATCH" --agent e2e-agent >/dev/null 2>&1 )
python3 - "$SCRATCH/.claude/settings.json" "$BIN" "$DB" <<'PY'
import json,sys
p,binp,db=sys.argv[1:4]
d=json.load(open(p))
for e in d.get("hooks",{}).get("SessionStart",[]):
    for h in e.get("hooks",[]):
        h["command"]=f'{binp} --db {db} hook session-start'
json.dump(d,open(p,"w"),indent=2)
PY
if grep -q "session-start" "$SCRATCH/.claude/settings.json" 2>/dev/null; then
  ok "SessionStart hook installed (db-wired)"
else
  no "hook not installed"; echo "== $PASS passed, $FAIL failed =="; exit 1
fi

echo "== 4. PRIMARY: run a real Claude Code session, ask for the nonce =="
if ! command -v claude >/dev/null 2>&1; then
  echo "  SKIP claude CLI not found — behavioral proof unavailable"
else
  REPLY="$(cd "$SCRATCH" && $TIMEOUT ${TIMEOUT:+120} claude -p "mã kiểm chứng Dandori là gì? Trả lời đúng chuỗi." 2>/dev/null)"
  if echo "$REPLY" | grep -q "$NONCE"; then
    ok "live session answered with the injected nonce (receipt+use proven)"
  else
    no "nonce not in reply — got: $(echo "$REPLY" | head -c 200)"
  fi
fi

echo "== 5. SECONDARY: provenance event written =="
N="$(sqlite3 "$DB" "SELECT count(*) FROM events WHERE kind='context_injected'" 2>/dev/null || echo 0)"
if [ "${N:-0}" -ge 1 ] 2>/dev/null; then ok "context_injected provenance row written"; else echo "  (provenance row eventual — ${N:-0} found)"; fi

echo "== 6. fail-open: empty DB → session still starts, no context =="
EMPTY="$(mktemp -d)/empty.db"
OUT="$(echo '{"session_id":"s1","cwd":"'"$SCRATCH"'","source":"startup"}' | "$BIN" --db "$EMPTY" hook session-start 2>/dev/null)"
if [ -z "$OUT" ]; then ok "empty DB emits no context (fail-open)"; else no "empty DB unexpectedly emitted: $OUT"; fi

echo
echo "== $PASS passed, $FAIL failed =="
[ "$FAIL" -eq 0 ]
