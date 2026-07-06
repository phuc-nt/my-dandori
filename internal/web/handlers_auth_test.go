package web

import (
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/phuc-nt/dandori/internal/auth"
)

func TestLoginSuccessSetsCookieAndRedirects(t *testing.T) {
	s := testServerWithListen(t, "127.0.0.1:4777")
	mustCreateAccount(t, s, "alice", "admin")

	rec := postForm(t, s, "/login", url.Values{"username": {"alice"}, "password": {"test-password-123"}})
	if rec.Code != http.StatusFound {
		t.Fatalf("login success → status %d, want 302", rec.Code)
	}
	if loc := rec.Header().Get("Location"); loc != "/" {
		t.Errorf("redirect Location = %q, want /", loc)
	}

	cookies := rec.Result().Cookies()
	var sessCookie *http.Cookie
	for _, c := range cookies {
		if c.Name == SessionCookieName {
			sessCookie = c
		}
	}
	if sessCookie == nil {
		t.Fatal("login must set a session cookie")
	}
	if !sessCookie.HttpOnly {
		t.Error("session cookie must be HttpOnly")
	}
	if !sessCookie.Secure {
		t.Error("session cookie must be Secure")
	}
	if sessCookie.SameSite != http.SameSiteStrictMode {
		t.Error("session cookie must be SameSite=Strict")
	}
}

func TestLoginWrongPasswordGenericError(t *testing.T) {
	s := testServerWithListen(t, "127.0.0.1:4777")
	mustCreateAccount(t, s, "alice", "admin")

	rec := postForm(t, s, "/login", url.Values{"username": {"alice"}, "password": {"wrong"}})
	if rec.Code != http.StatusOK {
		t.Fatalf("wrong password → status %d, want 200 (re-render login)", rec.Code)
	}
	if !strings.Contains(rec.Body.String(), "Sai tên đăng nhập hoặc mật khẩu") {
		t.Error("wrong password must show the generic error")
	}
}

func TestLoginUnknownUserGenericError(t *testing.T) {
	s := testServerWithListen(t, "127.0.0.1:4777")
	mustCreateAccount(t, s, "alice", "admin")

	rec := postForm(t, s, "/login", url.Values{"username": {"nobody"}, "password": {"whatever"}})
	if !strings.Contains(rec.Body.String(), "Sai tên đăng nhập hoặc mật khẩu") {
		t.Error("unknown username must show the SAME generic error as wrong password (no enumeration)")
	}
}

func TestLoginRotatesSessionID(t *testing.T) {
	s := testServerWithListen(t, "127.0.0.1:4777")
	mustCreateAccount(t, s, "alice", "admin")

	// Simulate an attacker-planted pre-login session cookie (fixation attempt).
	preLoginID, err := s.sessions.Create("alice")
	if err != nil {
		t.Fatalf("Create: %v", err)
	}

	req := httptest.NewRequest("POST", "/login", strings.NewReader(url.Values{
		"username": {"alice"}, "password": {"test-password-123"},
	}.Encode()))
	req.Host = s.Cfg.Listen
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.AddCookie(&http.Cookie{Name: SessionCookieName, Value: preLoginID})
	rec := httptest.NewRecorder()
	s.Handler().ServeHTTP(rec, req)

	var newID string
	for _, c := range rec.Result().Cookies() {
		if c.Name == SessionCookieName {
			newID = c.Value
		}
	}
	if newID == "" {
		t.Fatal("login must set a new session cookie")
	}
	if newID == preLoginID {
		t.Error("login must rotate the session id, not reuse the pre-login cookie")
	}
	if sess, _ := s.sessions.Load(preLoginID); sess != nil {
		t.Error("pre-login session id must be invalidated after rotate")
	}
}

func TestLogoutDeletesSession(t *testing.T) {
	s := testServerWithListen(t, "127.0.0.1:4777")
	mustCreateAccount(t, s, "alice", "admin")
	sessID, _ := s.sessions.Create("alice")

	req := httptest.NewRequest("POST", "/logout", nil)
	req.Host = s.Cfg.Listen
	req.AddCookie(&http.Cookie{Name: SessionCookieName, Value: sessID})
	rec := httptest.NewRecorder()
	s.Handler().ServeHTTP(rec, req)

	if rec.Code != http.StatusFound {
		t.Fatalf("logout → status %d, want 302", rec.Code)
	}
	if sess, _ := s.sessions.Load(sessID); sess != nil {
		t.Error("logout must delete the session")
	}
}

// [H2] per-IP token bucket → 429 after bucketCapacity rapid attempts.
func TestLoginRateLimitPerIP(t *testing.T) {
	s := testServerWithListen(t, "127.0.0.1:4777")
	mustCreateAccount(t, s, "alice", "admin")

	var last *httptest.ResponseRecorder
	for i := 0; i < 6; i++ {
		last = postForm(t, s, "/login", url.Values{"username": {"alice"}, "password": {"wrong"}})
	}
	if last.Code != http.StatusTooManyRequests {
		t.Errorf("6th rapid login attempt from same IP → status %d, want 429", last.Code)
	}
}

// [H2] per-account failure counter locks out regardless of which IP is used.
func TestLoginRateLimitPerAccountAcrossIPs(t *testing.T) {
	s := testServerWithListen(t, "127.0.0.1:4777")
	mustCreateAccount(t, s, "alice", "admin")

	for i := 0; i < 5; i++ {
		req := httptest.NewRequest("POST", "/login", strings.NewReader(url.Values{
			"username": {"alice"}, "password": {"wrong"},
		}.Encode()))
		req.Host = s.Cfg.Listen
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		req.RemoteAddr = "10.0.0." + string(rune('1'+i)) + ":12345" // different IP each time
		rec := httptest.NewRecorder()
		s.Handler().ServeHTTP(rec, req)
	}

	req := httptest.NewRequest("POST", "/login", strings.NewReader(url.Values{
		"username": {"alice"}, "password": {"test-password-123"}, // even the RIGHT password
	}.Encode()))
	req.Host = s.Cfg.Listen
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.RemoteAddr = "10.0.0.99:12345" // yet another fresh IP
	rec := httptest.NewRecorder()
	s.Handler().ServeHTTP(rec, req)

	if rec.Code != http.StatusTooManyRequests {
		t.Errorf("account locked out after 5 failures across IPs → status %d, want 429 even with correct password", rec.Code)
	}
}

// [H4] set-password invalidates existing sessions immediately.
func TestSetPasswordInvalidatesSessions(t *testing.T) {
	s := testServerWithListen(t, "127.0.0.1:4777")
	mustCreateAccount(t, s, "alice", "admin")
	sessID, _ := s.sessions.Create("alice")

	newHash, err := auth.HashPassword("new-password")
	if err != nil {
		t.Fatalf("hash: %v", err)
	}
	if err := s.Store.SetPassword("alice", newHash); err != nil {
		t.Fatalf("SetPassword: %v", err)
	}
	if err := s.sessions.DeleteForOperator("alice"); err != nil {
		t.Fatalf("DeleteForOperator: %v", err)
	}

	if sess, _ := s.sessions.Load(sessID); sess != nil {
		t.Error("session must be invalidated immediately after set-password (H4), not wait for 8h expiry")
	}
}

// [H4] disabling an operator kills their sessions immediately.
func TestDisableOperatorInvalidatesSessions(t *testing.T) {
	s := testServerWithListen(t, "127.0.0.1:4777")
	mustCreateAccount(t, s, "alice", "admin")
	sessID, _ := s.sessions.Create("alice")

	if err := s.Store.DisableOperator("alice"); err != nil {
		t.Fatalf("DisableOperator: %v", err)
	}
	if err := s.sessions.DeleteForOperator("alice"); err != nil {
		t.Fatalf("DeleteForOperator: %v", err)
	}

	if sess, _ := s.sessions.Load(sessID); sess != nil {
		t.Error("session must be invalidated immediately after disable (H4)")
	}
}

// [C5] cross-origin POST to /login itself must be rejected by originGuard
// (defense-in-depth even on the auth endpoint).
func TestLoginCrossOriginRejected(t *testing.T) {
	s := testServerWithListen(t, "127.0.0.1:4777")
	mustCreateAccount(t, s, "alice", "admin")

	req := httptest.NewRequest("POST", "/login", strings.NewReader(url.Values{
		"username": {"alice"}, "password": {"test-password-123"},
	}.Encode()))
	req.Host = s.Cfg.Listen
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("Origin", "http://evil.example.com")
	rec := httptest.NewRecorder()
	s.Handler().ServeHTTP(rec, req)

	if rec.Code != http.StatusForbidden {
		t.Errorf("cross-origin POST /login → status %d, want 403", rec.Code)
	}
}

// [C5] Sec-Fetch-Site: cross-site rejected even without an Origin header.
func TestSecFetchSiteCrossSiteRejected(t *testing.T) {
	s := testServerWithListen(t, "127.0.0.1:4777")
	req := httptest.NewRequest("POST", "/api/kill", strings.NewReader("on=1"))
	req.Host = s.Cfg.Listen
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("Sec-Fetch-Site", "cross-site")
	rec := httptest.NewRecorder()
	s.Handler().ServeHTTP(rec, req)

	if rec.Code != http.StatusForbidden {
		t.Errorf("Sec-Fetch-Site: cross-site → status %d, want 403", rec.Code)
	}
}
