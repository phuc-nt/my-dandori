package web

import (
	"fmt"
	"html/template"
	"net/http"
	"strconv"

	"github.com/go-chi/chi/v5"

	"github.com/phuc-nt/dandori/internal/learn"
)

// Knowledge suggest surface (P4): the agent-detail card that shows published
// units this agent has not used yet, with LIVE-recomputed stats (F11) — kept
// in its own file (handlers_knowledge.go is already >200 lines) per the
// phase-04 split instruction.

// knowledgeSuggestN caps the agent-detail card to a small, readable list —
// same default SuggestUnitsForAgent falls back to when callers pass 0.
const knowledgeSuggestN = 5

// handleKnowledgeSuggestForAgent renders the HTMX fragment lazy-loaded on
// the agent-detail page (mirrors handleAgentAIReview's load pattern):
// GET /agents/{agent}/knowledge-suggest.
func (s *Server) handleKnowledgeSuggestForAgent(w http.ResponseWriter, r *http.Request) {
	agentID := chi.URLParam(r, "agent")
	suggestions, err := learn.SuggestUnitsForAgent(s.Store, agentID, knowledgeSuggestN)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	s.renderFragment(w, r, "dash_agent", "knowledge_suggest_card", map[string]any{
		"AgentID":     agentID,
		"Suggestions": suggestions,
	})
}

// handleKnowledgeAdoptSkill records "operator intends to use this skill" —
// F4: calls the NEW learn.RecordUnitAdoption (installed=false, a suggest-only
// intent click; installed only flips true from the actual `dandori skill
// pull` CLI path, skillreg/skill_cmd.go), NEVER the old learn.RecordAdoption
// (that stays wired to handlers_playbooks.go's own playbook-adopt flow only,
// per the F4 red-team fix — this handler must not touch it).
func (s *Server) handleKnowledgeAdoptSkill(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseInt(chi.URLParam(r, "id"), 10, 64)
	if err != nil {
		http.Error(w, "bad unit id", http.StatusBadRequest)
		return
	}
	u, err := learn.GetUnit(s.Store, id)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	if u == nil || u.Kind != learn.KindSkill {
		http.Error(w, "unit not found or not a skill", http.StatusNotFound)
		return
	}
	if _, err := learn.RecordUnitAdoption(s.Store, id, s.actor(r), "", false, s.Cfg.LearnWindowDays); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	fmt.Fprintf(w, `<span class="text-green-600 text-sm">Đã đánh dấu sẽ dùng — chạy <code>dandori skill pull %s</code> để cài.</span>`,
		template.HTMLEscapeString(u.Name))
}
