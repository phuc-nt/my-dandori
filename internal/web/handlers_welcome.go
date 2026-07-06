package web

import "net/http"

// Onboarding wizard. Three steps, each derived from health (no stored wizard
// state): hook a project, connect an integration, run once. The page polls its
// own fragment so a step ticks green the moment it completes, and stops polling
// (HTTP 286) once all three are done.

type wizardStep struct {
	Done bool
}

type wizardView struct {
	HookProject wizardStep
	Connect     wizardStep
	FirstRun    wizardStep
	AllDone     bool
}

func (s *Server) wizard() wizardView {
	h := Collect(s.Cfg, s.Store)
	connected := false
	for _, i := range h.Integrations {
		// A connection counts only if configured AND its last test is recent
		// (or untested-but-configured); a stale failing test is not "done".
		if i.Configured && (i.LastTest == nil || i.LastTest.OK) {
			connected = true
			break
		}
	}
	v := wizardView{
		HookProject: wizardStep{Done: h.HookedProjects > 0},
		Connect:     wizardStep{Done: connected},
		FirstRun:    wizardStep{Done: h.RunsCaptured > 0},
	}
	v.AllDone = v.HookProject.Done && v.Connect.Done && v.FirstRun.Done
	return v
}

func (s *Server) handleWelcome(w http.ResponseWriter, r *http.Request) {
	s.render(w, r, "welcome", map[string]any{
		"Page": "welcome", "Mode": "exec", "W": s.wizard(),
	})
}

// handleWelcomeFragment powers the poll: it re-renders the step list and
// returns 286 once everything is done so HTMX stops polling.
func (s *Server) handleWelcomeFragment(w http.ResponseWriter, r *http.Request) {
	v := s.wizard()
	if v.AllDone {
		w.WriteHeader(286)
	}
	s.renderFragment(w, r, "welcome", "wizard_steps", map[string]any{"W": v})
}
