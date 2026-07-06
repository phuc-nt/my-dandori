package web

import (
	"net"
	"net/http"

	"github.com/phuc-nt/dandori/internal/auth"
)

// registerAuthRoutes wires /login and /logout. Called once from New() right
// after routes(), same convention as registerPhaseNNRoutes.
func (s *Server) registerAuthRoutes() {
	s.mux.Get("/login", s.handleLoginForm)
	s.mux.Post("/login", s.handleLogin)
	s.mux.Post("/logout", s.handleLogout)
}

// clientIP extracts the request's source IP for rate limiting. The console
// has no trusted reverse proxy in front by default, so X-Forwarded-For is
// deliberately NOT trusted here (trivially spoofable) — RemoteAddr is the
// TCP peer, which is what actually rate-limits a brute-force source.
func clientIP(r *http.Request) string {
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		return r.RemoteAddr
	}
	return host
}

func (s *Server) setSessionCookie(w http.ResponseWriter, id string) {
	http.SetCookie(w, &http.Cookie{
		Name:     SessionCookieName,
		Value:    id,
		Path:     "/",
		HttpOnly: true,
		Secure:   true,
		SameSite: http.SameSiteStrictMode,
		// No Max-Age/Expires: non-persistent, browser drops it on exit.
	})
}

func (s *Server) clearSessionCookie(w http.ResponseWriter) {
	http.SetCookie(w, &http.Cookie{
		Name:     SessionCookieName,
		Value:    "",
		Path:     "/",
		HttpOnly: true,
		Secure:   true,
		SameSite: http.SameSiteStrictMode,
		MaxAge:   -1,
	})
}

// handleBootstrapPage is the ONLY page a fresh (0-account) non-loopback
// console serves (C2). It never grants admin; it just tells the operator how
// to bootstrap via CLI. sessionMiddleware calls this directly for every path
// that isn't already public.
func (s *Server) handleBootstrapPage(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write([]byte(bootstrapPageHTML))
}

const bootstrapPageHTML = `<!DOCTYPE html>
<html lang="vi"><head><meta charset="utf-8"><title>Dandori — Chưa thiết lập</title>
<style>body{font-family:system-ui,sans-serif;max-width:640px;margin:80px auto;padding:0 20px;color:#1f2937}
code{background:#f3f4f6;padding:2px 6px;border-radius:4px}</style></head>
<body>
<h1>⛩ Dandori chưa có tài khoản</h1>
<p>Console này đang chạy trên một địa chỉ không phải localhost, nên chưa thể vào thẳng như trên máy cá nhân.</p>
<p>Người quản trị hạ tầng cần tạo tài khoản admin đầu tiên từ dòng lệnh, trên máy chạy server:</p>
<pre>dandori operator add --username admin --role admin</pre>
<p>Sau khi có tài khoản, tải lại trang này để đăng nhập.</p>
</body></html>`

// handleLoginForm renders the login page. Also reachable when a session has
// merely expired (redirected here by sessionMiddleware).
func (s *Server) handleLoginForm(w http.ResponseWriter, r *http.Request) {
	s.renderLogin(w, "")
}

// handleLogin verifies credentials and starts a session. Generic error
// message for both "unknown user" and "wrong password" (avoid enumeration).
func (s *Server) handleLogin(w http.ResponseWriter, r *http.Request) {
	const genericErr = "Sai tên đăng nhập hoặc mật khẩu."

	ip := clientIP(r)
	if !s.rateLimit.AllowIP(ip) {
		w.WriteHeader(http.StatusTooManyRequests)
		s.renderLogin(w, "Quá nhiều lần thử. Vui lòng đợi rồi thử lại.")
		return
	}

	username := r.FormValue("username")
	password := r.FormValue("password")

	if !s.rateLimit.AllowAccount(username) {
		w.WriteHeader(http.StatusTooManyRequests)
		s.renderLogin(w, "Tài khoản tạm khoá do đăng nhập sai nhiều lần. Vui lòng đợi rồi thử lại.")
		return
	}

	cred, err := s.Store.OperatorByUsername(username)
	if err != nil {
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	valid := cred != nil && cred.DisabledAt == "" && cred.PasswordHash != "" && auth.VerifyPassword(password, cred.PasswordHash)
	if !valid {
		s.rateLimit.RecordFailure(username)
		s.renderLogin(w, genericErr)
		return
	}
	s.rateLimit.ResetAccount(username)

	// Rotate: discard any pre-login cookie the browser may have sent
	// (session fixation defense) and issue a fresh session id.
	var oldID string
	if c, err := r.Cookie(SessionCookieName); err == nil {
		oldID = c.Value
	}
	newID, err := s.sessions.Rotate(oldID, cred.ID)
	if err != nil {
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	s.setSessionCookie(w, newID)
	http.Redirect(w, r, "/", http.StatusFound)
}

// handleLogout deletes the session and clears the cookie.
func (s *Server) handleLogout(w http.ResponseWriter, r *http.Request) {
	if c, err := r.Cookie(SessionCookieName); err == nil {
		_ = s.sessions.Delete(c.Value)
	}
	s.clearSessionCookie(w)
	http.Redirect(w, r, "/login", http.StatusFound)
}

func (s *Server) renderLogin(w http.ResponseWriter, errMsg string) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	t, ok := s.tmpl.pages["login"]
	if !ok {
		http.Error(w, "login template missing", http.StatusInternalServerError)
		return
	}
	if err := t.ExecuteTemplate(w, "login_page", map[string]any{"Error": errMsg}); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
	}
}
