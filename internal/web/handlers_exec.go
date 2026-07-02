package web

import (
	"net/http"
	"strconv"

	"github.com/go-chi/chi/v5"

	"github.com/phuc-nt/dandori/internal/govern"
	"github.com/phuc-nt/dandori/internal/observer"
	"github.com/phuc-nt/dandori/internal/store"
)

// handleHome is mode-aware: exec renders the Vietnamese executive home,
// tech falls through to the existing standup.
func (s *Server) handleHome(w http.ResponseWriter, r *http.Request) {
	if modeFrom(r) == "tech" {
		s.handleStandup(w, r)
		return
	}
	s.handleExecHome(w, r)
}

func (s *Server) handleExecHome(w http.ResponseWriter, r *http.Request) {
	view, err := BuildExecView(s.Store, s.Cfg.LearnWindowDays)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	s.render(w, r, "exec_home", map[string]any{
		"Page": "exec_home", "Mode": "exec",
		"KillOn": s.Store.Setting("kill_switch_global") == "1",
		"View":   view,
	})
}

// handleExecApprove approves an inbox item through the SAME audited decide
// path as the technical review queue, then applies observer/chat requests.
func (s *Server) handleExecApprove(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseInt(chi.URLParam(r, "id"), 10, 64)
	if err != nil {
		http.Error(w, "bad id", http.StatusBadRequest)
		return
	}
	won, err := govern.Decide(s.Store, id, true, s.execActor(), "duyệt từ bảng điều hành")
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	if won {
		_, _ = observer.RunObserverApplier(s.Store)
	}
	s.renderInbox(w, r)
}

// handleExecDismiss resolves a surfaced insight (audited) without acting.
func (s *Server) handleExecDismiss(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseInt(chi.URLParam(r, "id"), 10, 64)
	if err != nil {
		http.Error(w, "bad id", http.StatusBadRequest)
		return
	}
	if _, err := s.Store.DB.Exec(`UPDATE insights SET status = 'dismissed', resolved_at = ?
		WHERE id = ? AND status IN ('open','surfaced')`, store.Now(), id); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	a := &govern.Audit{St: s.Store, Actor: s.execActor()}
	_, _ = a.Append("insight_dismissed", strconv.FormatInt(id, 10), "bỏ qua từ bảng điều hành")
	s.renderInbox(w, r)
}

// renderInbox returns the refreshed inbox fragment after an action.
func (s *Server) renderInbox(w http.ResponseWriter, r *http.Request) {
	view, err := BuildExecView(s.Store, s.Cfg.LearnWindowDays)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	s.renderFragment(w, "exec_home", "inbox", map[string]any{"View": view})
}

// execActor is the single-principal attribution (plan Trust model). The mode
// cookie is a label, not proof of who clicked — so no per-human claim.
func (s *Server) execActor() string { return s.Cfg.UserName + "@console" }
