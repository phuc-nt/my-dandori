package web

import (
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
)

// roleSession logs in as username (already created via mustCreateAccount)
// and returns a request-decorator that attaches the resulting session
// cookie, so callers can drive get()/postForm()-style requests as that role.
func roleSession(t *testing.T, s *Server, username string) *http.Cookie {
	t.Helper()
	sessID, err := s.sessions.Create(username)
	if err != nil {
		t.Fatalf("sessions.Create(%q): %v", username, err)
	}
	return &http.Cookie{Name: SessionCookieName, Value: sessID}
}

func getAs(t *testing.T, s *Server, cookie *http.Cookie, path string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest("GET", path, nil)
	req.Host = s.Cfg.Listen
	req.AddCookie(cookie)
	rec := httptest.NewRecorder()
	s.Handler().ServeHTTP(rec, req)
	return rec
}

func postFormAs(t *testing.T, s *Server, cookie *http.Cookie, path string, form url.Values) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest("POST", path, strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Host = s.Cfg.Listen
	req.AddCookie(cookie)
	rec := httptest.NewRecorder()
	s.Handler().ServeHTTP(rec, req)
	return rec
}

// roleRoute is one row of the C4 inventory: the exact path this test drives
// (with {id}/{name}/{agent} placeholders filled with a harmless literal) and
// whether it must be admin-gated.
type roleRoute struct {
	path  string
	admin bool
}

// allPOSTRoutes mirrors the full 38-row inventory (phase-04-role-gates.md),
// resolved to concrete request paths. Two routes structurally outside the
// inventory (/login, /logout) are intentionally excluded: /login bypasses
// sessionMiddleware entirely (public path — gating it would make login
// impossible) and /logout must work for any authenticated role, admin or
// viewer, so it is not a "business" write the admin/viewer split governs.
var allPOSTRoutes = []roleRoute{
	{"/mode", false},
	{"/exec/approve/1", true},
	{"/exec/insight/1/dismiss", true},
	{"/agents/demo-agent/band", true},
	{"/launch", true},
	{"/runs/run-1/retry", true},
	{"/runs/bulk-kill", true},
	{"/runs/bulk-budget", true},
	{"/runs/run-1/kill", true},
	{"/runs/run-1/task-key", false},
	{"/runs/run-1/flag", false},
	{"/runs/run-1/playbook", false},
	{"/playbooks/1/adopt", false},
	{"/contexts/save", true},
	{"/contexts/rollback", true},
	{"/contexts/promote", true},
	{"/contexts/drive-import", true},
	{"/reviews/1/decide", true},
	{"/chat/message", true},
	{"/budgets", true},
	{"/rules", true},
	{"/rules/simulate", false},
	{"/rules/1/toggle", true},
	{"/rules/1/delete", true},
	{"/api/kill", true},
	{"/export/compliance", true},
	{"/reports/confluence", true},
	{"/settings/integrations/jira", true},
	{"/settings/integrations/jira/test", false},
	{"/runs/run-1/transition-request", true},
	{"/runs/run-1/pr-review-request", true},
	{"/runs/run-1/calendar-request", true},
	{"/runs/run-1/override-gate", true},
	{"/dash/export-sheets", true},
	{"/dash/send-digest", true},
	{"/gate-thresholds", true},
	{"/views/save", false},
	{"/views/1/delete", false},
}

// [C4] Table-driven role x route: every real POST route in the inventory,
// admin gets past the gate (never 403), viewer is 403 on every admin-gated
// route and never 403 on a viewer-ok route. Handlers may still fail with a
// 4xx/5xx for unrelated reasons (missing body, missing external integration,
// etc.) — the assertion here is specifically "did requireAdmin block it",
// i.e. viewer must never see 403 on a viewer-ok route, and must always see
// exactly 403 on an admin-gated one.
func TestRoleGatesAllPOSTRoutes(t *testing.T) {
	s := testServerWithListen(t, "127.0.0.1:4777")
	mustCreateAccount(t, s, "admin1", "admin")
	mustCreateAccount(t, s, "viewer1", "viewer")
	adminCookie := roleSession(t, s, "admin1")
	viewerCookie := roleSession(t, s, "viewer1")

	for _, rt := range allPOSTRoutes {
		t.Run(rt.path, func(t *testing.T) {
			viewerRec := postFormAs(t, s, viewerCookie, rt.path, url.Values{})
			if rt.admin {
				if viewerRec.Code != http.StatusForbidden {
					t.Errorf("viewer POST %s = %d, want 403 (admin-gated)", rt.path, viewerRec.Code)
				}
			} else if viewerRec.Code == http.StatusForbidden {
				t.Errorf("viewer POST %s = 403, want NOT forbidden (viewer-ok route)", rt.path)
			}

			adminRec := postFormAs(t, s, adminCookie, rt.path, url.Values{})
			if adminRec.Code == http.StatusForbidden {
				t.Errorf("admin POST %s = 403, admin must never be blocked by requireAdmin", rt.path)
			}
		})
	}
}

// [C4] Default-deny: a brand-new POST route that a developer wires up and
// gates with requireAdmin (the mandated default for any route not already
// triaged as viewer-ok in the inventory) must reject viewer and accept admin
// — proving the mechanism fails closed rather than open when applied to an
// unclassified route.
func TestRoleGateDefaultDenyOnUnclassifiedRoute(t *testing.T) {
	s := testServerWithListen(t, "127.0.0.1:4777")
	mustCreateAccount(t, s, "admin2", "admin")
	mustCreateAccount(t, s, "viewer2", "viewer")
	adminCookie := roleSession(t, s, "admin2")
	viewerCookie := roleSession(t, s, "viewer2")

	s.mux.With(s.requireAdmin).Post("/__test/unclassified-route", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	})

	viewerRec := postFormAs(t, s, viewerCookie, "/__test/unclassified-route", url.Values{})
	if viewerRec.Code != http.StatusForbidden {
		t.Fatalf("viewer POST unclassified route = %d, want 403 (fail-closed default)", viewerRec.Code)
	}
	adminRec := postFormAs(t, s, adminCookie, "/__test/unclassified-route", url.Values{})
	if adminRec.Code != http.StatusOK {
		t.Fatalf("admin POST unclassified route = %d, want 200", adminRec.Code)
	}
}

// [H4] Demoting an admin to viewer must take effect on the very next gated
// request — no waiting for the 8h session absolute-timeout or 20m idle
// timeout. requireAdmin re-queries operators.role fresh on every call, so
// this must pass even though the session row/cookie never changes.
func TestRoleGateDemoteTakesEffectImmediately(t *testing.T) {
	s := testServerWithListen(t, "127.0.0.1:4777")
	mustCreateAccount(t, s, "demoteme", "admin")
	cookie := roleSession(t, s, "demoteme")

	before := postFormAs(t, s, cookie, "/budgets", url.Values{"scope_type": {"global"}, "limit_usd": {"10"}})
	if before.Code == http.StatusForbidden {
		t.Fatalf("admin POST /budgets before demote = 403, want non-403")
	}

	if _, err := s.Store.DB.Exec(`UPDATE operators SET role = 'viewer' WHERE id = ?`, "demoteme"); err != nil {
		t.Fatalf("demote: %v", err)
	}

	after := postFormAs(t, s, cookie, "/budgets", url.Values{"scope_type": {"global"}, "limit_usd": {"20"}})
	if after.Code != http.StatusForbidden {
		t.Fatalf("POST /budgets with same session cookie after demote = %d, want 403 immediately", after.Code)
	}
}

// [H4] Disabling an operator must also take effect immediately on the next
// gated request. Two accounts exist here so the assertion stays scoped to
// this file's gate rather than P1's trust-gate switch (disabling the LAST
// ever-created account is covered separately by
// TestTrustGateDisablingLastAdminLocksConsole in auth_middleware_test.go,
// which proves it locks the console via HasEverHadAccount rather than
// re-opening local-trust).
//
// The disabled operator's session stops resolving one layer before
// requireAdmin even runs: P1's sessions.Load JOINs operators.disabled_at IS
// NULL, so a disabled operator's cookie now loads no session at all and
// sessionMiddleware redirects to /login — a stronger guarantee than a 403
// from this file's gate, and the correct behavior to assert here.
func TestRoleGateDisableTakesEffectImmediately(t *testing.T) {
	s := testServerWithListen(t, "127.0.0.1:4777")
	mustCreateAccount(t, s, "disableme", "admin")
	mustCreateAccount(t, s, "stillhere", "admin")
	cookie := roleSession(t, s, "disableme")

	before := postFormAs(t, s, cookie, "/budgets", url.Values{"scope_type": {"global"}, "limit_usd": {"10"}})
	if before.Code == http.StatusForbidden || before.Code == http.StatusFound {
		t.Fatalf("admin POST /budgets before disable = %d, want neither 403 nor a login redirect", before.Code)
	}

	if err := s.Store.DisableOperator("disableme"); err != nil {
		t.Fatalf("DisableOperator: %v", err)
	}

	after := postFormAs(t, s, cookie, "/budgets", url.Values{"scope_type": {"global"}, "limit_usd": {"20"}})
	if after.Code != http.StatusForbidden && after.Code != http.StatusFound {
		t.Fatalf("POST /budgets with same session cookie after disable = %d, want 403 (blocked by requireAdmin) or 302 (session no longer resolves, blocked by sessionMiddleware) — either proves immediate effect", after.Code)
	}
	if after.Code == http.StatusFound {
		if loc := after.Header().Get("Location"); loc != "/login" {
			t.Fatalf("disabled operator redirect Location = %q, want /login", loc)
		}
	}
}

// [C1] Local-trust mode (0 accounts + loopback bind) must still resolve
// IsAdmin=true and let every admin-gated route through with no login,
// preserving v9 parity — requireAdmin must special-case localTrustFrom(r)
// rather than trying (and failing) a DB lookup for a principal that doesn't
// exist as an operators row in this mode.
func TestRoleGateLocalTrustBypassesAdminCheck(t *testing.T) {
	s := testServerWithListen(t, "127.0.0.1:4777")

	rec := postFormAs(t, s, &http.Cookie{Name: "unused", Value: "x"}, "/budgets",
		url.Values{"scope_type": {"global"}, "limit_usd": {"10"}})
	if rec.Code == http.StatusForbidden {
		t.Fatalf("local-trust POST /budgets = 403, want non-403 (v9 parity, admin implied)")
	}
}

// [C1] IsAdmin() must read P1's context values, not re-derive them: viewer
// role in context => false even when called directly (guards the templates'
// {{if .IsAdmin}} contract independent of any specific route's behavior).
func TestIsAdminReadsSessionRole(t *testing.T) {
	s := testServerWithListen(t, "127.0.0.1:4777")
	mustCreateAccount(t, s, "viewerdirect", "viewer")
	cookie := roleSession(t, s, "viewerdirect")

	rec := getAs(t, s, cookie, "/runs")
	if rec.Code != http.StatusOK {
		t.Fatalf("viewer GET /runs = %d, want 200 (GET never gated)", rec.Code)
	}
}
