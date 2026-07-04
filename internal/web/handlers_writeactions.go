// Handlers for the v7 approval-gated write actions: UC2 Jira transition,
// UC4 PR review, and UB4 per-check gate override. UC9 calendar event lives
// in handlers_calendar_request.go (split out to stay under the 200-line
// cap). Every request handler here only PROPOSES via observer.RequestAction
// — the actual external write happens later, once approved, in engine.go's
// apply cases.
package web

import (
	"fmt"
	"net/http"
	"strconv"
	"strings"

	"github.com/go-chi/chi/v5"

	"github.com/phuc-nt/dandori/internal/config"
	"github.com/phuc-nt/dandori/internal/integrations/jira"
	"github.com/phuc-nt/dandori/internal/learn"
	"github.com/phuc-nt/dandori/internal/observer"
)

// handleRunTransitions lists the run's Jira issue's live transitions (UC2),
// for the dropdown. Read-only — no Guard needed (matches jira.Transitions).
func (s *Server) handleRunTransitions(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	var taskKey string
	s.Store.Read().QueryRow(`SELECT COALESCE(task_key,'') FROM runs WHERE id = ?`, id).Scan(&taskKey)
	if taskKey == "" {
		http.Error(w, "run has no linked task key", http.StatusBadRequest)
		return
	}
	cfg, err := config.Load("")
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	i := cfg.Integrations
	if i.AtlassianSite == "" || i.AtlassianToken == "" {
		http.Error(w, "atlassian credentials not configured", http.StatusServiceUnavailable)
		return
	}
	c := jira.New(i.AtlassianSite, i.AtlassianEmail, i.AtlassianToken)
	transitions, err := c.Transitions(taskKey)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadGateway)
		return
	}
	s.renderFragment(w, "run_detail", "run_transitions", map[string]any{
		"RunID": id, "TaskKey": taskKey, "Transitions": transitions,
	})
}

// handleTransitionRequest proposes a Jira transition (UC2). Pins the
// transition NAME (not id — ids are per-workflow and can be reassigned; H3).
func (s *Server) handleTransitionRequest(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	var taskKey string
	s.Store.Read().QueryRow(`SELECT COALESCE(task_key,'') FROM runs WHERE id = ?`, id).Scan(&taskKey)
	if taskKey == "" {
		http.Error(w, "run has no linked task key", http.StatusBadRequest)
		return
	}
	name := strings.TrimSpace(r.FormValue("transition_name"))
	if name == "" {
		http.Error(w, "transition_name is required", http.StatusBadRequest)
		return
	}
	summary := fmt.Sprintf("Chuyển %s sang trạng thái %q (chờ duyệt).", taskKey, name)
	params := map[string]any{"key": taskKey, "transition_name": name}
	if _, err := observer.RequestAction(s.Store, "jira-transition", taskKey, summary, params, s.execActor(), "operator"); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	redirectBack(w, r, "/runs/"+id)
}

// handlePRReviewRequest proposes a PR review/comment (UC4). Pins the PR's
// current head SHA at request time (H3 TOCTOU guard) via a live `gh pr view`
// read before requesting.
func (s *Server) handlePRReviewRequest(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	repo := strings.TrimSpace(r.FormValue("repo"))
	numStr := strings.TrimSpace(r.FormValue("num"))
	decision := r.FormValue("decision")
	body := r.FormValue("body")
	num, err := strconv.Atoi(numStr)
	if repo == "" || err != nil || num <= 0 || !validPRDecision(decision) {
		http.Error(w, "repo, a positive num, and a valid decision are required", http.StatusBadRequest)
		return
	}
	_, headSHA, err := observer.PRCurrentState(r.Context(), repo, num)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadGateway)
		return
	}
	subject := fmt.Sprintf("%s#%d", repo, num)
	summary := fmt.Sprintf("%s trên %s (chờ duyệt).", decisionLabel(decision), subject)
	params := map[string]any{
		"repo": repo, "num": num, "decision": decision, "body": body, "head_sha": headSHA,
	}
	if _, err := observer.RequestAction(s.Store, "pr-review", subject, summary, params, s.execActor(), "operator"); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	redirectBack(w, r, "/runs/"+id)
}

func validPRDecision(d string) bool {
	return d == "approve" || d == "request-changes" || d == "comment"
}

func decisionLabel(d string) string {
	switch d {
	case "approve":
		return "Duyệt PR"
	case "request-changes":
		return "Yêu cầu sửa PR"
	default:
		return "Bình luận PR"
	}
}

// handleOverrideGate records a per-check quality-gate override (UB4).
// Justification is mandatory — empty is a 400, never a silent bypass.
func (s *Server) handleOverrideGate(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	checkName := strings.TrimSpace(r.FormValue("check_name"))
	reason := strings.TrimSpace(r.FormValue("reason"))
	if checkName == "" {
		http.Error(w, "check_name is required", http.StatusBadRequest)
		return
	}
	if reason == "" {
		http.Error(w, "a justification is required to override a failed gate check", http.StatusBadRequest)
		return
	}
	auditor := &auditAppender{s: s}
	if err := learn.OverrideGate(s.Store, auditor, id, checkName, s.execActor(), reason); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	s.renderRunGateResults(w, id)
}
