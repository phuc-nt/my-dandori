package web

import (
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	"github.com/phuc-nt/dandori/internal/auth"
	"github.com/phuc-nt/dandori/internal/config"
	"github.com/phuc-nt/dandori/internal/store"
)

// testServerWithListen builds a server bound to a specific Listen address,
// letting tests exercise the C2 loopback-gate both ways.
func testServerWithListen(t *testing.T, listen string) *Server {
	t.Helper()
	st, err := store.Open(filepath.Join(t.TempDir(), "w.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	cfg, _ := config.Load(filepath.Join(t.TempDir(), "no.yaml"))
	cfg.Listen = listen
	s, err := New(cfg, st)
	if err != nil {
		t.Fatal(err)
	}
	return s
}

func mustCreateAccount(t *testing.T, s *Server, username, role string) string {
	t.Helper()
	hash, err := auth.HashPassword("test-password-123")
	if err != nil {
		t.Fatalf("HashPassword: %v", err)
	}
	if err := s.Store.CreateOperatorAccount(username, username, hash, role); err != nil {
		t.Fatalf("CreateOperatorAccount: %v", err)
	}
	return username
}

// [C1] 0 accounts + loopback → straight through, admin, no login (v9 parity).
func TestTrustGateLocalTrustLoopbackNoAccount(t *testing.T) {
	s := testServerWithListen(t, "127.0.0.1:4777")
	rec := get(t, s, "/runs")
	if rec.Code != 200 {
		t.Fatalf("local-trust loopback + 0 account → /runs status = %d, want 200", rec.Code)
	}
}

// [C2] 0 accounts + non-loopback → bootstrap page for ANY path, never admin.
func TestTrustGateBootstrapNonLoopbackNoAccount(t *testing.T) {
	s := testServerWithListen(t, "0.0.0.0:4777")
	rec := get(t, s, "/runs")
	if rec.Code != 200 {
		t.Fatalf("bootstrap page status = %d, want 200", rec.Code)
	}
	if !strings.Contains(rec.Body.String(), "operator add") {
		t.Error("bootstrap page must instruct `dandori operator add`, not serve the real page")
	}
	if strings.Contains(rec.Body.String(), "Kill switch") {
		t.Error("bootstrap page must not render authenticated console chrome")
	}
}

// [C2] 0 accounts + non-loopback → /login itself still reachable (public path).
func TestTrustGateLoginReachableNonLoopbackNoAccount(t *testing.T) {
	s := testServerWithListen(t, "0.0.0.0:4777")
	rec := get(t, s, "/login")
	if rec.Code != 200 {
		t.Fatalf("/login status = %d, want 200", rec.Code)
	}
}

// [C1] has account + no cookie on a route → 302 /login, never admin.
func TestTrustGateNoSessionRedirectsToLogin(t *testing.T) {
	s := testServerWithListen(t, "127.0.0.1:4777")
	mustCreateAccount(t, s, "alice", "admin")

	rec := get(t, s, "/runs")
	if rec.Code != 302 {
		t.Fatalf("no-cookie request with an account present → status %d, want 302", rec.Code)
	}
	if loc := rec.Header().Get("Location"); loc != "/login" {
		t.Errorf("redirect Location = %q, want /login", loc)
	}
}

// [M1] session expired/absent + HX-Request → HX-Redirect header, not login HTML swapped in.
func TestTrustGateHXRedirectOnExpiry(t *testing.T) {
	s := testServerWithListen(t, "127.0.0.1:4777")
	mustCreateAccount(t, s, "alice", "admin")

	req := httptest.NewRequest("GET", "/runs", nil)
	req.Host = s.Cfg.Listen
	req.Header.Set("HX-Request", "true")
	rec := httptest.NewRecorder()
	s.Handler().ServeHTTP(rec, req)

	if rec.Code != 200 {
		t.Errorf("HX-Request with no session → status %d, want 200 (HX-Redirect, not a hard redirect)", rec.Code)
	}
	if got := rec.Header().Get("HX-Redirect"); got != "/login" {
		t.Errorf("HX-Redirect header = %q, want /login", got)
	}
	if strings.Contains(rec.Body.String(), "Đăng nhập") {
		t.Error("HX-Request expiry must not swap login HTML into the fragment body")
	}
}

// [H4/C1] Disabling the LAST enabled admin must lock the console, never
// re-open local-trust — the exact auth-bypass this test guards against: a
// naive gate keyed on "any ENABLED account" would flip back to 0-accounts
// and grant admin to every request with no session. Contrast case below
// proves a truly-fresh instance (0 accounts ever) still gets v9 local-trust.
func TestTrustGateDisablingLastAdminLocksConsole(t *testing.T) {
	t.Run("last enabled admin disabled -> locked, not local-trust", func(t *testing.T) {
		s := testServerWithListen(t, "127.0.0.1:4777")
		mustCreateAccount(t, s, "onlyadmin", "admin")

		if err := s.Store.DisableOperator("onlyadmin"); err != nil {
			t.Fatalf("DisableOperator: %v", err)
		}

		rec := get(t, s, "/runs")
		if rec.Code != http.StatusFound {
			t.Fatalf("disabling the last admin -> /runs status = %d, want 302 (locked, redirect to /login)", rec.Code)
		}
		if loc := rec.Header().Get("Location"); loc != "/login" {
			t.Errorf("redirect Location = %q, want /login", loc)
		}
	})

	t.Run("fresh instance, 0 accounts ever -> local-trust preserved", func(t *testing.T) {
		s := testServerWithListen(t, "127.0.0.1:4777")

		rec := get(t, s, "/runs")
		if rec.Code != http.StatusOK {
			t.Fatalf("fresh instance (0 accounts ever) + loopback -> /runs status = %d, want 200 (v9 local-trust)", rec.Code)
		}
	})
}

// Valid session cookie → request proceeds with correct principal/role.
func TestTrustGateValidSessionGrantsAccess(t *testing.T) {
	s := testServerWithListen(t, "127.0.0.1:4777")
	mustCreateAccount(t, s, "alice", "viewer")
	sessID, err := s.sessions.Create("alice")
	if err != nil {
		t.Fatalf("Create session: %v", err)
	}

	req := httptest.NewRequest("GET", "/runs", nil)
	req.Host = s.Cfg.Listen
	req.AddCookie(&http.Cookie{Name: SessionCookieName, Value: sessID})
	rec := httptest.NewRecorder()
	s.Handler().ServeHTTP(rec, req)

	if rec.Code != 200 {
		t.Fatalf("valid session → status %d, want 200", rec.Code)
	}
}
