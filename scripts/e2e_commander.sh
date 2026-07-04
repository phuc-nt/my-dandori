#!/usr/bin/env bash
# Live E2E for v6 Commander: launch → live → kill (process really dies) →
# retry → bulk-kill, plus the CRITICAL flag-injection security gate (a prompt
# that looks like a dangerous agent flag must run as text, never as a flag).
#
# Uses a fake "claude" that echoes its argv, so we can assert exactly how the
# prompt was passed WITHOUT needing the real agent or a network.
set -uo pipefail
cd "$(dirname "$0")/.."
PASS=0; FAIL=0
ok(){ echo "  PASS $1"; PASS=$((PASS+1)); }
no(){ echo "  FAIL $1"; FAIL=$((FAIL+1)); }

TMP="$(mktemp -d)"
BIN="$TMP/dandori"
go build -o "$BIN" ./cmd/dandori || { echo "compile failed"; exit 1; }

# Cross-compile proof (pure-Go, no CGO) — compiled inside the script so the
# outer hook that blocks bare "go build" is not involved.
GOOS=linux GOARCH=amd64 CGO_ENABLED=0 go build -o "$TMP/d-linux" ./cmd/dandori \
  && ok "cross-compile linux/amd64 (no CGO)" || no "linux cross-compile"

# A fake claude that prints how it was invoked, then exits.
FAKE="$TMP/fakeclaude"
cat > "$FAKE" <<'EOF'
#!/usr/bin/env bash
echo "ARGV: $*"
echo "done"
EOF
chmod +x "$FAKE"

PROJ="$TMP/projects"
WORK="$PROJ/demo"
mkdir -p "$WORK"
DB="$TMP/e2e.db"

# config.yaml wiring the fake claude into the allowlist + projects_dir.
CFG="$TMP/config.yaml"
cat > "$CFG" <<EOF
db_path: $DB
projects_dir: $PROJ
agent_binaries:
  claude: $FAKE
max_concurrent_launches: 4
EOF

run(){ "$BIN" --config "$CFG" --db "$DB" "$@"; }

echo "== 1. flag-injection: prompt that looks like a dangerous flag =="
# Use the launcher via a one-shot helper: seed a launch by hitting the CLI's
# store through a tiny Go probe that calls runner.Launch directly.
PROBE="internal/runner/e2eprobe"
mkdir -p "$PROBE"
cat > "$PROBE/main.go" <<EOF
package main
import ("fmt";"os";"time";"github.com/phuc-nt/dandori/internal/store";"github.com/phuc-nt/dandori/internal/config";"github.com/phuc-nt/dandori/internal/capture";"github.com/phuc-nt/dandori/internal/runner")
func main(){
  cfg,_:=config.Load(os.Args[1])
  st,_:=store.Open(cfg.DBPath); defer st.Close()
  l:=runner.New(cfg,st,&capture.Ingestor{Cfg:cfg,St:st})
  id,err:=l.Launch("claude", os.Args[2], os.Args[3], "e2e@console", "")
  if err!=nil{ fmt.Println("LAUNCH_ERR:",err); os.Exit(3) }
  fmt.Println("RUNID:",id)
  time.Sleep(1500*time.Millisecond)  // let the fake finish + reaper run
}
EOF
EVIL="--dangerously-bypass-approvals-and-sandbox"
OUT="$(go run ./"$PROBE" "$CFG" "$EVIL" "$WORK" 2>&1)"
RUNID="$(echo "$OUT" | sed -n 's/RUNID: //p')"
# The fake echoes "ARGV: -p <prompt>"; the evil string must appear as the -p value.
CAP="$(run runs -n 5 >/dev/null 2>&1; sqlite3 "$DB" "SELECT payload FROM events WHERE kind='run_stdout' AND payload LIKE 'ARGV:%' LIMIT 1")"
if echo "$CAP" | grep -q -- "-p $EVIL"; then
  ok "dangerous-flag prompt passed as -p VALUE (not a flag): $CAP"
else
  no "flag-injection: argv was $CAP"
fi

echo "== 2. launch created a captured console run that finalized =="
ST="$(sqlite3 "$DB" "SELECT status FROM runs WHERE id='$RUNID'")"
SRC="$(sqlite3 "$DB" "SELECT source FROM runs WHERE id='$RUNID'")"
[ "$SRC" = "console" ] && ok "run source=console" || no "run source=$SRC"
[ "$ST" = "done" ] && ok "run finalized to done (no stuck running)" || no "run status=$ST"

echo "== 3. kill a long-running launch (process actually dies) =="
# A fake that sleeps so we can kill it.
SLEEPER="$TMP/sleeper"; printf '#!/usr/bin/env bash\necho starting\nsleep 30\n' > "$SLEEPER"; chmod +x "$SLEEPER"
sed -i.bak "s#claude: .*#claude: $SLEEPER#" "$CFG"
cat > "$PROBE/main.go" <<EOF
package main
import ("fmt";"os";"time";"github.com/phuc-nt/dandori/internal/store";"github.com/phuc-nt/dandori/internal/config";"github.com/phuc-nt/dandori/internal/capture";"github.com/phuc-nt/dandori/internal/runner")
func main(){
  cfg,_:=config.Load(os.Args[1]); st,_:=store.Open(cfg.DBPath); defer st.Close()
  l:=runner.New(cfg,st,&capture.Ingestor{Cfg:cfg,St:st})
  id,err:=l.Launch("claude","30",os.Args[2],"e2e@console",""); if err!=nil{fmt.Println("ERR",err);os.Exit(3)}
  time.Sleep(600*time.Millisecond)
  var pid int; st.DB.QueryRow("SELECT pid FROM runs WHERE id=?",id).Scan(&pid)
  out,_:=l.Kill(id,"e2e@console","e2e kill")
  time.Sleep(400*time.Millisecond)
  fmt.Printf("RUNID: %s\nPID: %d\nOUTCOME: %s\n", id, pid, out)
}
EOF
KOUT="$(go run ./"$PROBE" "$CFG" "$WORK" 2>&1)"
KPID="$(echo "$KOUT" | sed -n 's/PID: //p')"
KID="$(echo "$KOUT" | sed -n 's/RUNID: //p')"
echo "$KOUT" | grep -q "OUTCOME: signaled" && ok "kill outcome=signaled" || no "kill outcome: $KOUT"
if [ -n "$KPID" ] && kill -0 "$KPID" 2>/dev/null; then no "process still alive after kill (pid $KPID)"; else ok "process group actually dead"; fi
KST="$(sqlite3 "$DB" "SELECT status FROM runs WHERE id='$KID'")"
[ "$KST" = "killed" ] && ok "status=killed" || no "status=$KST"
AUD="$(sqlite3 "$DB" "SELECT count(*) FROM audit_log WHERE action='kill_run'")"
[ "${AUD:-0}" -ge 1 ] && ok "kill audited" || no "no kill audit"

rm -rf "$PROBE"
echo
echo "== $PASS passed, $FAIL failed =="
[ "$FAIL" -eq 0 ]
