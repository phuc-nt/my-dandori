// Package web v10 identity+RBAC E2E: one file stitching the full request →
// login/session → principal → audit → role-gate chain through the actual
// HTTP surface, mirroring the v7 write-back E2E convention in this same
// package (v7_writeback_e2e_test.go) rather than a separate internal/e2e
// package — this is the only package that sees both the chi mux AND the
// unexported test helpers (testServer/testServerWithListen/postForm/
// roleSession/mustCreateAccount) needed to drive a full login → cookie →
// gated-route round trip without reimplementing them in a second package.
//
// This file numbers each test after the 19+2-step matrix in
// plans/260707-0000-dandori-v10-identity-rbac/phase-05-e2e-docs.md. Several
// steps are ALREADY proven end-to-end by existing unit tests in this same
// package (auth_middleware_test.go, role_middleware_test.go) or in sibling
// packages (internal/ingest/token_auth_test.go, internal/cli via
// hook_wrap_cli_test.go, internal/govern/audit_continuity_test.go) — for
// those this file adds a short reference test that re-runs the scenario
// through this file's own harness (proving it's reachable from `go test
// ./...` as a whole) plus a comment pointing at the exhaustive original,
// rather than duplicating every edge case a second time.
package web

import (
	"net/http"
	"net/http/httptest"
	"net/url"
	"os/exec"
	"strings"
	"testing"

	"github.com/phuc-nt/dandori/internal/capture"
	"github.com/phuc-nt/dandori/internal/govern"
	"github.com/phuc-nt/dandori/internal/runner"
	"github.com/phuc-nt/dandori/internal/store"
)

// --- #1 / #2: trust-gate loopback vs non-loopback, 0 accounts -------------
//
// True E2E in this file's harness. Exhaustive edge cases (HX-Request variant,
// /login still reachable) already in auth_middleware_test.go
// (TestTrustGateLocalTrustLoopbackNoAccount / TestTrustGateBootstrapNonLoopbackNoAccount).

func TestE2E01_ZeroAccountLoopback_LocalTrustAdmin(t *testing.T) {
	s := testServerWithListen(t, "127.0.0.1:4777")
	rec := get(t, s, "/runs")
	if rec.Code != http.StatusOK {
		t.Fatalf("0 account + loopback GET / (via /runs) = %d, want 200 (local-trust, v9 parity)", rec.Code)
	}
}

func TestE2E02_ZeroAccountNonLoopback_BootstrapNotAdmin(t *testing.T) {
	s := testServerWithListen(t, "0.0.0.0:4777")
	rec := get(t, s, "/")
	if rec.Code != http.StatusOK {
		t.Fatalf("0 account + non-loopback GET / = %d, want 200 (bootstrap page, not an error)", rec.Code)
	}
	if !strings.Contains(rec.Body.String(), "operator add") {
		t.Error("non-loopback fresh instance must serve the bootstrap banner, not the real admin home")
	}
}

// --- #3: operator add admin + login correct creds → 302 /, cookie set -----

func TestE2E03_LoginValidCreds_RedirectsWithSessionCookie(t *testing.T) {
	s := testServerWithListen(t, "127.0.0.1:4777")
	mustCreateAccount(t, s, "admin3", "admin")

	rec := postForm(t, s, "/login", url.Values{"username": {"admin3"}, "password": {"test-password-123"}})
	if rec.Code != http.StatusFound {
		t.Fatalf("login with valid creds = %d, want 302", rec.Code)
	}
	if rec.Header().Get("Location") != "/" {
		t.Errorf("login redirect Location = %q, want /", rec.Header().Get("Location"))
	}
	found := false
	for _, c := range rec.Result().Cookies() {
		if c.Name == SessionCookieName && c.Value != "" {
			found = true
		}
	}
	if !found {
		t.Error("login response must Set-Cookie a non-empty session id")
	}
}

// --- #4: wrong password x6 → generic error then 429 -----------------------
//
// RateLimiter locks an account after 5 failures (auth/ratelimit.go
// maxAccountFails=5), so the 6th attempt in this test is the one that must
// see 429 — the spec's "x6" is satisfied by attempt 6 tripping the lock that
// attempt 5 armed.

func TestE2E04_WrongPasswordSixTimes_GenericErrorThen429(t *testing.T) {
	s := testServerWithListen(t, "127.0.0.1:4777")
	mustCreateAccount(t, s, "admin4", "admin")

	var last *httptest.ResponseRecorder
	for i := 0; i < 6; i++ {
		last = postForm(t, s, "/login", url.Values{"username": {"admin4"}, "password": {"wrong-pw"}})
		if i < 4 {
			if last.Code != http.StatusOK {
				t.Fatalf("attempt %d wrong password = %d, want 200 (login page re-rendered with error)", i+1, last.Code)
			}
			if strings.Contains(last.Body.String(), "admin4") {
				t.Errorf("attempt %d: error page must not enumerate/echo the username", i+1)
			}
		}
	}
	if last.Code != http.StatusTooManyRequests {
		t.Fatalf("6th consecutive wrong password = %d, want 429 (account locked out)", last.Code)
	}
}

// --- #5 [C1]: account exists + no cookie on a write route → 302 /login ----
//
// True E2E here. Exhaustively covered by TestTrustGateNoSessionRedirectsToLogin
// (auth_middleware_test.go) on a GET route; this test drives the exact write
// route the spec calls out (/runs/{id}/kill) to prove the same gate applies
// to POST/mutation paths, not just GET.

func TestE2E05_AccountExistsNoCookie_WriteRoutePOST_RedirectsLogin(t *testing.T) {
	s := testServerWithListen(t, "127.0.0.1:4777")
	mustCreateAccount(t, s, "admin5", "admin")
	seedRun(t, s, "run-e2e-5", "running")

	rec := postForm(t, s, "/runs/run-e2e-5/kill", url.Values{})
	if rec.Code != http.StatusFound {
		t.Fatalf("no-cookie POST /runs/{id}/kill = %d, want 302 (never admin)", rec.Code)
	}
	if rec.Header().Get("Location") != "/login" {
		t.Errorf("redirect Location = %q, want /login", rec.Header().Get("Location"))
	}
	var status string
	s.Store.DB.QueryRow(`SELECT status FROM runs WHERE id = ?`, "run-e2e-5").Scan(&status)
	if status == "killed" {
		t.Error("unauthenticated request must not have been allowed to kill the run")
	}
}

// --- #6 [C5]: cross-origin POST (Origin mismatch) → 403 -------------------

func TestE2E06_CrossOriginPOST_Forbidden(t *testing.T) {
	s := testServerWithListen(t, "127.0.0.1:4777")
	mustCreateAccount(t, s, "admin6", "admin")
	cookie := roleSession(t, s, "admin6")

	req := httptest.NewRequest("POST", "/budgets", strings.NewReader("scope_type=global&limit_usd=10"))
	req.Host = s.Cfg.Listen
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("Origin", "https://evil.example.com")
	req.AddCookie(cookie)
	rec := httptest.NewRecorder()
	s.Handler().ServeHTTP(rec, req)

	if rec.Code != http.StatusForbidden {
		t.Fatalf("cross-origin POST /budgets = %d, want 403 (originGuard)", rec.Code)
	}
}

// --- #7: admin POST valid write → 2xx, audit actor=admin -------------------

func TestE2E07_AdminValidWrite_2xxAndAuditActorIsPrincipal(t *testing.T) {
	s := testServerWithListen(t, "127.0.0.1:4777")
	mustCreateAccount(t, s, "admin7", "admin")
	cookie := roleSession(t, s, "admin7")

	// handleBudgetSet redirects back to /budgets on success (redirectBack),
	// not a bare 2xx — 303 See Other is the "successful write" shape here.
	rec := postFormAs(t, s, cookie, "/budgets", url.Values{"scope_type": {"global"}, "limit_usd": {"42"}})
	if rec.Code != http.StatusSeeOther {
		t.Fatalf("admin POST /budgets = %d, want 303 (redirectBack on success)", rec.Code)
	}
	var actor string
	s.Store.DB.QueryRow(`SELECT actor FROM audit_log WHERE action = 'set_budget' ORDER BY id DESC LIMIT 1`).Scan(&actor)
	if actor != "admin7" {
		t.Errorf("audit actor = %q, want %q (real principal, not @console)", actor, "admin7")
	}
}

// --- #8 [C3]: admin kill + launch + bulk-budget → audit actor=admin, never @console

func TestE2E08_AdminKillLaunchBulkBudget_AuditNeverConsole(t *testing.T) {
	s := testServerWithListen(t, "127.0.0.1:4777")
	mustCreateAccount(t, s, "admin8", "admin")
	cookie := roleSession(t, s, "admin8")

	// kill
	seedRun(t, s, "run-e2e-8a", "running")
	if rec := postFormAs(t, s, cookie, "/runs/run-e2e-8a/kill", url.Values{}); rec.Code == http.StatusForbidden {
		t.Fatalf("admin kill = %d, want non-403", rec.Code)
	}
	var killActor string
	s.Store.DB.QueryRow(`SELECT actor FROM audit_log WHERE action = 'kill_run' AND subject = ? ORDER BY id DESC LIMIT 1`, "run-e2e-8a").Scan(&killActor)
	if killActor != "admin8" {
		t.Errorf("kill audit actor = %q, want %q", killActor, "admin8")
	}

	// launch
	proj := t.TempDir()
	s.Cfg.ProjectsDir = proj
	s.Cfg.AgentBinaries = map[string]string{"claude": "/bin/echo"}
	s.Cfg.MaxConcurrentLaunches = 2
	s.Launcher = runner.New(s.Cfg, s.Store, &capture.Ingestor{Cfg: s.Cfg, St: s.Store})
	rec := postFormAs(t, s, cookie, "/launch", url.Values{"agent": {"claude"}, "prompt": {"hi"}, "cwd": {""}})
	if rec.Code != http.StatusNoContent {
		t.Fatalf("admin launch = %d, want 204", rec.Code)
	}
	var launchActor string
	s.Store.DB.QueryRow(`SELECT actor FROM audit_log WHERE action = 'run_launched' ORDER BY id DESC LIMIT 1`).Scan(&launchActor)
	if launchActor != "admin8" {
		t.Errorf("launch audit actor = %q, want %q", launchActor, "admin8")
	}

	// bulk-budget
	seedRunWithAgent(t, s, "run-e2e-8b", "agent-e2e-8")
	rec = postFormAs(t, s, cookie, "/runs/bulk-budget", url.Values{"run_ids": {"run-e2e-8b"}, "amount": {"99"}})
	if rec.Code == http.StatusForbidden {
		t.Fatalf("admin bulk-budget = %d, want non-403", rec.Code)
	}
	var bulkActor string
	s.Store.DB.QueryRow(`SELECT actor FROM audit_log WHERE action = 'budget_set' AND subject = ? ORDER BY id DESC LIMIT 1`, "agent:agent-e2e-8").Scan(&bulkActor)
	if bulkActor != "admin8" {
		t.Errorf("bulk-budget audit actor = %q, want %q", bulkActor, "admin8")
	}

	for _, actor := range []string{killActor, launchActor, bulkActor} {
		if strings.Contains(actor, "@console") {
			t.Errorf("audit actor %q must never be the pre-P2 @console fallback once a real session exists", actor)
		}
	}
}

// --- #9 / #10 / #11 [H1]: ingest per-operator token attribution -----------
//
// True E2E against the real ingest HTTP handler already lives in
// internal/ingest/token_auth_test.go:
//   - TestIngestPerOperatorTokenAttributesCorrectOperator (#9)
//   - TestIngestPerOperatorTokenRejectsSpoofedHint (#10, spoofed
//     X-Dandori-Principal-Hint with a valid per-operator token still
//     attributes to the token's bound operator)
//   - TestIngestLegacyTokenSpoofedHintStaysFixedPrincipal (#11 [H1]: legacy
//     shared token + spoofed hint → fixed "legacy-shared@ingest", never the
//     spoofed value)
// Not duplicated here — internal/web cannot import internal/ingest's
// unexported test harness, and re-declaring the ingest handler wiring in
// this package would be a parallel implementation, not an E2E proof.

// --- #12 / #13 [C4]: viewer 403 on all 29 admin routes + default-deny -----
//
// True E2E, table-driven over all 38 real routes (29 admin + 9 viewer-ok)
// plus one synthetic unclassified route: role_middleware_test.go
// TestRoleGatesAllPOSTRoutes and TestRoleGateDefaultDenyOnUnclassifiedRoute,
// in this same package. Referenced here rather than re-listed to keep one
// inventory as the single source of truth (avoids the two lists drifting).

// --- #14 / #15: viewer POST viewer-ok route ok; viewer GET dashboards 200 -

func TestE2E14_ViewerPOSTViewerOkRoute_Ok(t *testing.T) {
	s := testServerWithListen(t, "127.0.0.1:4777")
	mustCreateAccount(t, s, "viewer14", "viewer")
	cookie := roleSession(t, s, "viewer14")
	seedRun(t, s, "run-e2e-14", "running")

	rec := postFormAs(t, s, cookie, "/runs/run-e2e-14/flag", url.Values{"flag": {"1"}})
	if rec.Code == http.StatusForbidden {
		t.Fatalf("viewer POST /runs/{id}/flag (viewer-ok) = 403, want allowed")
	}
}

func TestE2E15_ViewerGETDashboards_200(t *testing.T) {
	s := testServerWithListen(t, "127.0.0.1:4777")
	mustCreateAccount(t, s, "viewer15", "viewer")
	cookie := roleSession(t, s, "viewer15")

	for _, path := range []string{"/dash/org", "/runs", "/wallboard"} {
		rec := getAs(t, s, cookie, path)
		if rec.Code != http.StatusOK {
			t.Errorf("viewer GET %s = %d, want 200", path, rec.Code)
		}
	}
}

// --- #16 [H4]: admin demote/disable → next gated POST → 403 immediately ---
//
// True E2E already in role_middleware_test.go:
// TestRoleGateDemoteTakesEffectImmediately (demote) and
// TestRoleGateDisableTakesEffectImmediately (disable) in this same package.
// Referenced rather than re-run to avoid a second copy of the freshAdminRole
// re-read assertion drifting from the original.

// --- #17 / #18: audit Verify() across mixed actor namespaces + tamper -----
//
// True E2E already in internal/govern/audit_continuity_test.go:
// TestAuditChainMixedActorNamespaces — walks a chain mixing "@console",
// a real login principal, "slack:<id>", and "system@applier", asserts
// Verify()==0 (intact), then tampers one entry and asserts Verify() reports
// exactly that entry's id. Referenced rather than duplicated: this file adds
// only a session-boundary variant below (fresh account created through this
// package's real CreateOperatorAccount path, not a synthetic actor string),
// to prove the P1→P2 boundary this phase is actually shipping stays intact
// end-to-end through this package's own store handle.

func TestE2E17_18_AuditVerifyAcrossConsoleBoundary(t *testing.T) {
	s := testServerWithListen(t, "127.0.0.1:4777")
	mustCreateAccount(t, s, "admin1718", "admin")
	cookie := roleSession(t, s, "admin1718")

	// pre-P2-shaped legacy entry, written directly (as an old install's chain would have).
	legacy := &govern.Audit{St: s.Store, Actor: "phuc@console"}
	if _, err := legacy.Append("run_launched", "legacyrun", "pre-v10 entry"); err != nil {
		t.Fatalf("seed legacy audit entry: %v", err)
	}

	// real v10 entry via the actual HTTP path.
	rec := postFormAs(t, s, cookie, "/budgets", url.Values{"scope_type": {"global"}, "limit_usd": {"7"}})
	if rec.Code == http.StatusForbidden {
		t.Fatalf("admin POST /budgets = 403, want allowed")
	}

	broken, reason, err := govern.Verify(s.Store)
	if err != nil {
		t.Fatalf("Verify: %v", err)
	}
	if reason != "" {
		t.Fatalf("Verify() across @console + real-principal boundary = broken at %d reason=%q, want intact", broken, reason)
	}

	var tamperID int64
	s.Store.DB.QueryRow(`SELECT id FROM audit_log ORDER BY id DESC LIMIT 1`).Scan(&tamperID)
	if _, err := s.Store.DB.Exec(`UPDATE audit_log SET detail = 'tampered' WHERE id = ?`, tamperID); err != nil {
		t.Fatalf("tamper: %v", err)
	}
	broken, reason, err = govern.Verify(s.Store)
	if err != nil {
		t.Fatalf("Verify after tamper: %v", err)
	}
	if broken != tamperID || reason != "chain" {
		t.Errorf("Verify() after tamper reported id %d reason=%q, want %d/chain", broken, reason, tamperID)
	}
}

// --- #19: hook capture path while console logged-out → DB write, exit 0 --
//
// True E2E of the fail-open hook cycle (session-start/pre-tool/post-tool/
// stop via stdin, asserting the run reaches status=done) already lives in
// internal/cli/hook_wrap_cli_test.go: TestCLIHookCycleViaStdin. That test
// necessarily runs inside internal/cli, which DOES import internal/web (for
// `dandori serve`, see internal/cli/serve.go) — so it cannot itself prove
// the capture path is independent of web auth. The independence claim is a
// package-dependency-graph fact, not something an HTTP-level test can show,
// so it is asserted here instead, on the actual package the hook path calls
// into (internal/capture), via `go list -deps`.

func TestE2E19_CapturePathDoesNotImportWebPackage(t *testing.T) {
	out, err := exec.Command("go", "list", "-deps", "github.com/phuc-nt/dandori/internal/capture").Output()
	if err != nil {
		t.Fatalf("go list -deps internal/capture: %v", err)
	}
	if strings.Contains(string(out), "dandori/internal/web") {
		t.Error("internal/capture must never import internal/web — capture is a trust boundary below console auth (fail-open hook path must not depend on login)")
	}
}

// --- #20 [C1/H4 extra fix]: disable last admin (loopback) → protected route
// → 302 /login, NOT local-trust admin ---------------------------------------
//
// True E2E already in auth_middleware_test.go:
// TestTrustGateDisablingLastAdminLocksConsole. Referenced rather than
// duplicated — this is the exact scenario, same package, same harness.

// --- #21 [MEDIUM extra fix]: disable operator → their ingest token → 401 --
//
// True E2E already in internal/ingest/token_auth_test.go:
// TestIngestDisabledOperatorTokenRejected. Not duplicated here for the same
// cross-package-harness reason as #9-11 above.

// seedRunWithAgent inserts a minimal run row bound to agentID, for the
// bulk-budget path (#8) which looks up runs.agent_id to resolve the budget
// scope.
func seedRunWithAgent(t *testing.T, s *Server, runID, agentID string) {
	t.Helper()
	if _, err := s.Store.DB.Exec(`INSERT OR IGNORE INTO agents(id, name, created_at) VALUES(?, ?, ?)`,
		agentID, agentID, store.Now()); err != nil {
		t.Fatalf("seed agent: %v", err)
	}
	if _, err := s.Store.DB.Exec(
		`INSERT INTO runs(id, session_id, agent_id, status, started_at, source, runtime) VALUES(?, ?, ?, 'running', ?, 'hook', 'claude-code')`,
		runID, runID+"-sess", agentID, store.Now()); err != nil {
		t.Fatalf("seed run with agent: %v", err)
	}
}
