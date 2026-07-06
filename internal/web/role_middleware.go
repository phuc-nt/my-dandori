package web

import (
	"database/sql"
	"net/http"
)

// IsAdmin reports whether the request's principal has admin privileges,
// reading the single source of truth sessionMiddleware already computed
// (P1 C1 trust-gate) — this file does NOT re-derive the rule:
//
//	local-trust (HasEverHadAccount()==false && loopback) → true (v9 behavior)
//	otherwise                                    → session role == "admin"
//
// There is no "no-session ⇒ admin" path once an account exists; P1's
// sessionMiddleware already redirects unauthenticated requests to /login
// before a handler (or this helper) ever runs.
func IsAdmin(r *http.Request) bool {
	if localTrustFrom(r) {
		return true
	}
	return roleFrom(r) == "admin"
}

// requireAdmin gates a write route to admin-role operators only. [H4]: role
// is re-read from the database on every gated request (not the session's
// cached role) so a demote or disable takes effect on the very next request
// instead of waiting out the session's idle/absolute expiry. Cheap: PK
// lookup on operators.id with an index.
//
// Local-trust requests (no accounts yet, loopback bind) skip the DB check —
// there is no operator row to check against and IsAdmin is definitionally
// true in that mode (P1 C1/C2).
func (s *Server) requireAdmin(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if localTrustFrom(r) {
			next.ServeHTTP(w, r)
			return
		}

		operatorID := principalFrom(r)
		role, err := freshAdminRole(s.Store.DB, operatorID)
		if err != nil {
			http.Error(w, "internal error", http.StatusInternalServerError)
			return
		}
		if role != "admin" {
			http.Error(w, "admin role required", http.StatusForbidden)
			return
		}
		next.ServeHTTP(w, r)
	})
}

// freshAdminRole re-reads an operator's role directly from the database,
// scoped to enabled accounts only, so a `set-password`/`operator disable`/
// role-demote takes effect on the very next gated request instead of
// waiting out the session's cached role until expiry ([H4]). A disabled
// or unknown operator id reads back as "" (never "admin").
func freshAdminRole(db *sql.DB, operatorID string) (string, error) {
	var role string
	err := db.QueryRow(`SELECT role FROM operators WHERE id = ? AND disabled_at IS NULL`, operatorID).Scan(&role)
	if err == sql.ErrNoRows {
		return "", nil
	}
	if err != nil {
		return "", err
	}
	return role, nil
}
