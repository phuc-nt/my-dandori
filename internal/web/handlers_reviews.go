package web

import (
	"log"
	"net/http"
	"strconv"

	"github.com/go-chi/chi/v5"

	"github.com/phuc-nt/dandori/internal/govern"
	"github.com/phuc-nt/dandori/internal/observer"
)

// handleReviews renders the review queue (UB1): pending approvals live-polled
// every 3s, decision history below. For observer:knowledge-* approvals, F1
// CRITICAL requires the FULL pinned body+content_hash to render right here at
// the decide surface (queryApprovals → loadKnowledgeEvidence in viewdata.go),
// not just on the /knowledge detail page — the human approving from this
// inbox must see the exact bytes, never a truncated summary.
func (s *Server) handleReviews(w http.ResponseWriter, r *http.Request) {
	pending, err := s.queryApprovals("pending")
	if err != nil {
		http.Error(w, err.Error(), 500)
		return
	}
	history, _ := s.queryApprovals("")
	data := map[string]any{"Page": "reviews", "Pending": pending, "History": history}
	if isHTMX(r) {
		s.renderFragment(w, r, "reviews", "reviews_pending", data)
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
	won, err := govern.Decide(s.Store, id, approve, s.actor(r), note)
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
	if approve {
		// Observer/chat requests take effect on the click, not on the next
		// worker tick. Consume-once makes the extra run harmless.
		if _, err := observer.RunObserverApplier(s.Store); err != nil {
			log.Println("observer applier after decide:", err)
		}
	}
	redirectBack(w, r, "/reviews")
}
