package web

import "net/http"

// The console has two faces: "exec" (Vietnamese executive home for a CEO)
// and "tech" (the existing 13 operator pages). The mode is a cookie label
// for rendering — NOT an auth boundary (single-principal; RBAC is [Sau]).
const modeCookie = "dandori_mode"

// modeFrom reads the mode cookie, defaulting to exec (the CEO is the primary
// audience of v4).
func modeFrom(r *http.Request) string {
	if c, err := r.Cookie(modeCookie); err == nil && c.Value == "tech" {
		return "tech"
	}
	return "exec"
}

// handleSetMode flips the mode cookie and returns to where the user was.
func (s *Server) handleSetMode(w http.ResponseWriter, r *http.Request) {
	to := r.URL.Query().Get("to")
	if to != "tech" {
		to = "exec"
	}
	http.SetCookie(w, &http.Cookie{
		Name: modeCookie, Value: to, Path: "/",
		SameSite: http.SameSiteLaxMode, MaxAge: 60 * 60 * 24 * 365,
	})
	dest := "/"
	if to == "tech" {
		dest = "/standup"
	}
	http.Redirect(w, r, dest, http.StatusSeeOther)
}
