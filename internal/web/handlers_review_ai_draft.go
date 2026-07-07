package web

import (
	"net/http"

	"github.com/phuc-nt/dandori/internal/chat"
)

// LLM-Draft Assistant (v13 P3): "Soạn nháp practice (AI)" builds a redacted,
// DB-only evidence bundle for one run and one-shot calls OpenRouter, then
// returns an EDITABLE nominate-form fragment pre-filled with the draft —
// never auto-nominates. The returned form POSTs to the existing
// /knowledge/nominate handler (P2-owned, C2 fix) with hidden origin=ai-draft
// + origin_model + provenance_run_ids so that handler's own persistence
// picks up the provenance; this file never writes to knowledge_units itself.
//
// Role gate: any authenticated operator EXCEPT viewer (spends OpenRouter
// tokens on every call, so a read-only viewer must not reach it) — mirrors
// requireAdmin's local-trust bypass but only excludes the "viewer" role
// rather than requiring "admin", since there is no third role in the schema
// today (operators.role is admin|viewer, migration 015) and the spec calls
// for "member+" i.e. anything above viewer.
func (s *Server) requireNotViewer(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if localTrustFrom(r) {
			next.ServeHTTP(w, r)
			return
		}
		if roleFrom(r) == "viewer" {
			http.Error(w, "viewer không được dùng trợ lý soạn nháp (tốn token)", http.StatusForbidden)
			return
		}
		next.ServeHTTP(w, r)
	}
}

// handleKnowledgeDraft is POST /knowledge/draft {run_id}. On ANY failure
// (OpenRouter down/timeout, empty response, budget exceeded, model not
// configured, single-flight collision) it fails open: render the same
// fragment shape with an empty, still-submittable nominate form rather than
// an error page — a human can always write the practice by hand.
func (s *Server) handleKnowledgeDraft(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseForm(); err != nil {
		http.Error(w, "bad form", http.StatusBadRequest)
		return
	}
	runID := r.FormValue("run_id")
	if runID == "" {
		http.Error(w, "missing run_id", http.StatusBadRequest)
		return
	}

	actor := s.actor(r)
	title, body, model, err := chat.DraftPractice(s.Cfg, s.Store, actor, runID)
	data := map[string]any{
		"RunID": runID,
		"Title": title,
		"Body":  body,
		"Model": model,
	}
	if err != nil {
		// Hard DB failure gathering evidence (M4's one non-fail-open path at
		// the chat.DraftPractice layer) — still rendered as the same
		// fail-open fragment, never a 500, since the user's only recourse
		// either way is "write it by hand."
		data["Body"] = "không soạn được, viết tay"
		data["Title"] = ""
		data["Model"] = ""
	}
	s.renderFragment(w, r, "knowledge", "ai_draft_nominate_form", data)
}
