package web

import (
	"net/http"

	"github.com/phuc-nt/dandori/internal/learn"
)

// assignmentSuggestN is how many ranked candidates the fragment shows —
// small on purpose so a human can actually read and judge the list (L7 is
// suggest-only; a human assigns externally in Jira).
const assignmentSuggestN = 5

// suggestionView adapts learn.AgentSuggestion for the template: the success
// rate is pre-scaled to a 0-100 percentage so the shared `pct` template func
// (which expects an already-scaled value) can render it directly.
type suggestionView struct {
	AgentName      string
	Score          float64
	SuccessRatePct float64
	AvgCostUSD     float64
	Grade          string
	Samples        int
}

// handleAssignmentSuggest is L7: given a free-text task description (or a
// pasted Jira key/summary), return a ranked, read-only fragment of agent
// candidates by historical success/cost on keyword-overlapping past runs.
// It never writes to Jira and never assigns anything itself.
func (s *Server) handleAssignmentSuggest(w http.ResponseWriter, r *http.Request) {
	task := r.URL.Query().Get("task")
	data := map[string]any{"Page": "assign_suggest", "Task": task}
	if task == "" {
		s.renderFragment(w, "launch", "assignment_suggest", data)
		return
	}
	suggestions, err := learn.SuggestAgents(s.Store, task, assignmentSuggestN)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	views := make([]suggestionView, len(suggestions))
	for i, sug := range suggestions {
		views[i] = suggestionView{
			AgentName: sug.AgentName, Score: sug.Score,
			SuccessRatePct: sug.SuccessRate * 100,
			AvgCostUSD:     sug.AvgCostUSD, Grade: sug.Grade, Samples: sug.Samples,
		}
	}
	data["Suggestions"] = views
	s.renderFragment(w, "launch", "assignment_suggest", data)
}
