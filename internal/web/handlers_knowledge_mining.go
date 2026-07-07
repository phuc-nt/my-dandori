package web

import (
	"net/http"

	"github.com/go-chi/chi/v5"

	"github.com/phuc-nt/dandori/internal/learn"
)

// Mining Queue (v13 P1): "Mining" tab on the knowledge page surfacing runs
// đáng đọc via 4 on-demand SQL signals (learn.MineRuns) — read-only, viewer
// OK (mirrors handleKnowledgeNominate's own viewer-ok rationale: reading a
// suggestion list has no external side effect). Dismiss is the one write,
// and per M2 it is reading-list-only — it must never be routed through
// anything that touches the audit chain or run governance state.

// handleKnowledgeMining renders the mining tab (full page or HTMX fragment)
// with the current window's mined runs, ranked distinct-signal-count DESC
// then recency (learn.MineRuns already applies that rank — this handler
// does not re-sort or re-score).
func (s *Server) handleKnowledgeMining(w http.ResponseWriter, r *http.Request) {
	runs, err := learn.MineRuns(s.Store, s.Cfg.LearnWindowDays)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	data := map[string]any{
		"Page": "knowledge", "MiningRuns": runs, "Window": s.Cfg.LearnWindowDays,
		"IsMiningTab": true,
	}
	if isHTMX(r) {
		s.renderFragment(w, r, "knowledge", "knowledge_mining_tab", data)
		return
	}
	s.render(w, r, "knowledge", data)
}

// handleKnowledgeMiningDismiss is the one write on the mining tab (M2):
// INSERT into mining_dismissals only. This has ZERO governance-suppression
// power — the dismissed run stays fully visible in run detail, audit views,
// and every other surface; dismiss only declutters this one reading list. It
// is deliberately never written to the append-only audit chain.
func (s *Server) handleKnowledgeMiningDismiss(w http.ResponseWriter, r *http.Request) {
	runID := chi.URLParam(r, "runID")
	if runID == "" {
		http.Error(w, "missing run id", http.StatusBadRequest)
		return
	}
	reason := r.FormValue("reason")
	if err := learn.DismissMiningRun(s.Store, runID, s.actor(r), reason); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	w.Header().Set("HX-Refresh", "true")
}
