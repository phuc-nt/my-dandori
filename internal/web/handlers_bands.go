package web

import (
	"net/http"

	"github.com/go-chi/chi/v5"

	"github.com/phuc-nt/dandori/internal/govern"
)

// handleSetBand changes an agent's autonomy band from the console (UA4).
func (s *Server) handleSetBand(w http.ResponseWriter, r *http.Request) {
	agent := chi.URLParam(r, "agent")
	band := r.FormValue("band")
	reason := r.FormValue("reason")
	if reason == "" {
		reason = "set from console"
	}
	if err := govern.SetBand(s.Store, agent, band, s.actor(r), reason); err != nil {
		http.Error(w, err.Error(), 400)
		return
	}
	redirectBack(w, r, "/dash/agent/"+agent)
}
