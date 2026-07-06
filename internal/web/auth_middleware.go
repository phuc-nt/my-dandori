package web

import (
	"context"
	"net"
	"net/http"
	"strings"

	"github.com/phuc-nt/dandori/internal/auth"
)

// SessionCookieName is the cookie carrying the session id. Non-persistent
// (no Max-Age/Expires) so the browser drops it on exit; HttpOnly+Secure+
// SameSite=Strict set wherever it's written (handlers_auth.go).
const SessionCookieName = "dandori_session"

type ctxKey int

const (
	principalKey ctxKey = iota
	roleKey
	localTrustKey
)

// principalFrom / roleFrom / localTrustFrom read the values sessionMiddleware
// stashes on the request context. Handlers use these instead of Cfg.UserName
// so audit entries carry the real logged-in operator (P2 wires this up).
func principalFrom(r *http.Request) string {
	v, _ := r.Context().Value(principalKey).(string)
	return v
}

func roleFrom(r *http.Request) string {
	v, _ := r.Context().Value(roleKey).(string)
	return v
}

func localTrustFrom(r *http.Request) bool {
	v, _ := r.Context().Value(localTrustKey).(bool)
	return v
}

// publicPaths bypass the trust-gate entirely: static assets, health check,
// and the login/bootstrap routes themselves (else nobody could ever log in).
func isPublicPath(path string) bool {
	switch {
	case path == "/login", path == "/healthz":
		return true
	case strings.HasPrefix(path, "/static/"):
		return true
	}
	return false
}

// isLoopback reports whether the configured bind address is loopback
// (127.0.0.1/::1/localhost). C2: local-trust mode is only safe when nobody
// off-box can reach the console.
func isLoopback(listen string) bool {
	host, _, err := net.SplitHostPort(listen)
	if err != nil {
		host = listen
	}
	if host == "" || host == "localhost" {
		return true
	}
	ip := net.ParseIP(host)
	return ip != nil && ip.IsLoopback()
}

// sessionMiddleware implements the C1+C2 trust-gate verbatim (see
// plan phase-01-auth-foundation.md § Architecture — this switch is the single
// source of truth referenced by phase 4's role gates; do not re-derive it
// elsewhere).
func (s *Server) sessionMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if isPublicPath(r.URL.Path) {
			next.ServeHTTP(w, r)
			return
		}

		// everHad keys on "has an account EVER been created" (including
		// disabled ones), not "is any account currently enabled"
		// (HasAnyAccount). Once identity is bootstrapped it stays
		// bootstrapped: disabling the last admin must lock the console via
		// the default/session-mandatory branch below, never fall back to
		// no-auth local-trust.
		everHad, err := s.Store.HasEverHadAccount()
		if err != nil {
			http.Error(w, "internal error", http.StatusInternalServerError)
			return
		}
		loopback := isLoopback(s.Cfg.Listen)

		switch {
		case !everHad && loopback:
			// LOCAL-TRUST: no login, principal = fallback (v9 behavior).
			ctx := context.WithValue(r.Context(), localTrustKey, true)
			ctx = context.WithValue(ctx, principalKey, s.Cfg.UserName+"@console")
			ctx = context.WithValue(ctx, roleKey, "admin")
			next.ServeHTTP(w, r.WithContext(ctx))
			return

		case !everHad && !loopback:
			// FRESH NON-LOOPBACK: no admin, ever. Serve only the bootstrap
			// banner for any path that isn't already public.
			s.handleBootstrapPage(w, r)
			return

		default:
			// everHad == true: session mandatory on every non-public route.
			// If every account is now disabled, no valid session can load
			// (sessions.Load excludes disabled operators, see role_middleware
			// comment), so this branch correctly redirects to /login instead
			// of ever re-opening local-trust.
			cookie, cookieErr := r.Cookie(SessionCookieName)
			var sess *auth.Session
			if cookieErr == nil {
				sess, err = s.sessions.Load(cookie.Value)
				if err != nil {
					http.Error(w, "internal error", http.StatusInternalServerError)
					return
				}
			}
			if sess == nil {
				// Never IsAdmin=true here: no session means redirect, not admin.
				if isHTMX(r) {
					w.Header().Set("HX-Redirect", "/login")
					w.WriteHeader(http.StatusOK)
					return
				}
				http.Redirect(w, r, "/login", http.StatusFound)
				return
			}
			ctx := context.WithValue(r.Context(), principalKey, sess.OperatorID)
			ctx = context.WithValue(ctx, roleKey, sess.Role)
			ctx = context.WithValue(ctx, localTrustKey, false)
			next.ServeHTTP(w, r.WithContext(ctx))
		}
	})
}
