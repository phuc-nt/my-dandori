package web

import (
	"net/http"
	"strconv"

	"github.com/go-chi/chi/v5"

	"github.com/phuc-nt/dandori/internal/govern"
)

// handleReviews renders the review queue (UB1): pending approvals live-polled
// every 3s, decision history below.
func (s *Server) handleReviews(w http.ResponseWriter, r *http.Request) {
	pending, err := s.queryApprovals("pending")
	if err != nil {
		http.Error(w, err.Error(), 500)
		return
	}
	history, _ := s.queryApprovals("")
	data := map[string]any{"Page": "reviews", "Pending": pending, "History": history}
	if isHTMX(r) {
		s.renderFragment(w, "reviews", "reviews_pending", data)
		return
	}
	s.render(w, r, "reviews", data)
}

// handleReviewDecide records approve/reject with a reason (UB2). Rejection
// requires a note; both land in the immutable audit trail. First writer wins
// against the Slack poller.
func (s *Server) handleReviewDecide(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseInt(chi.URLParam(r, "id"), 10, 64)
	if err != nil {
		http.Error(w, "bad id", 400)
		return
	}
	approve := r.FormValue("decision") == "approve"
	note := r.FormValue("note")
	if !approve && note == "" {
		http.Error(w, "a reason is required when rejecting", 400)
		return
	}
	won, err := govern.Decide(s.Store, id, approve, s.Cfg.UserName, note)
	if err != nil {
		http.Error(w, err.Error(), 500)
		return
	}
	if !won {
		// Already decided elsewhere (e.g. Slack) — just refresh the queue.
		w.Header().Set("HX-Refresh", "true")
		w.WriteHeader(http.StatusConflict)
		return
	}
	redirectBack(w, r, "/reviews")
}
