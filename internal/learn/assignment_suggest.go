package learn

import (
	"regexp"
	"sort"
	"strings"

	"github.com/phuc-nt/dandori/internal/store"
)

// AgentSuggestion is one ranked candidate for L7's "suggest an agent" UI.
// Suggest-only: nothing here writes to Jira or assigns anything — a human
// reads this list and assigns externally. Agents with zero matching history
// are excluded entirely rather than included with a "no data" flag — an
// empty result slice IS the no-data signal, which the caller renders as
// such (see handleAssignmentSuggest).
type AgentSuggestion struct {
	AgentID     string
	AgentName   string
	Score       float64 // higher is better; see scoreAgent
	Samples     int     // how many historical runs the score is based on
	SuccessRate float64 // 0..1, fraction of sampled runs that finished "done"
	AvgCostUSD  float64
	Grade       string // most recent composite grade letter, "" if unscored
}

// suggestStopwords are dropped from keyword extraction — too common to
// discriminate between tasks.
var suggestStopwords = map[string]bool{
	"the": true, "and": true, "for": true, "with": true, "from": true,
	"this": true, "that": true, "into": true, "add": true, "fix": true,
	"cho": true, "va": true, "cua": true, "voi": true, "mot": true,
}

var nonAlnumRe = regexp.MustCompile(`[^a-zA-Z0-9]+`)

// extractKeywords lowercases, splits on non-alphanumeric runs, and drops
// stopwords + tokens shorter than 3 chars (too noisy to match on, e.g. "id").
func extractKeywords(taskText string) []string {
	tokens := nonAlnumRe.Split(strings.ToLower(taskText), -1)
	seen := map[string]bool{}
	var out []string
	for _, tok := range tokens {
		if len(tok) < 3 || suggestStopwords[tok] || seen[tok] {
			continue
		}
		seen[tok] = true
		out = append(out, tok)
	}
	return out
}

// SuggestAgents ranks up to n agents for a task by historical performance on
// runs whose work-item title overlaps the task's keywords (pure SQL + Go
// ranking — no ML, KISS per L7). Agents with zero matching history are
// excluded from the ranked slice unless every agent has no data, in which
// case an empty slice is returned (the caller renders a "no data" state).
func SuggestAgents(st *store.Store, taskText string, n int) ([]AgentSuggestion, error) {
	if n <= 0 {
		n = 5
	}
	keywords := extractKeywords(taskText)
	if len(keywords) == 0 {
		return nil, nil
	}

	likeClauses := make([]string, 0, len(keywords))
	args := make([]any, 0, len(keywords))
	for _, kw := range keywords {
		likeClauses = append(likeClauses, "lower(wi.title) LIKE ?")
		args = append(args, "%"+kw+"%")
	}
	// work_items is UNIQUE on (source, key), so a task_key can appear under
	// several sources (e.g. the same key in github and jira). Joining
	// directly would multiply a run's contribution by the number of
	// matching source rows and skew the ranking. Collapse to the set of
	// DISTINCT keys whose title matched, then join once per run.
	query := `SELECT r.agent_id, a.name, r.status, r.cost_usd
		FROM runs r
		JOIN (SELECT DISTINCT key FROM work_items wi
		      WHERE ` + strings.Join(likeClauses, " OR ") + `) mk ON mk.key = r.task_key
		JOIN agents a ON a.id = r.agent_id
		WHERE r.agent_id IS NOT NULL`
	rows, err := st.Read().Query(query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	type agg struct {
		name      string
		samples   int
		successes int
		totalCost float64
	}
	byAgent := map[string]*agg{}
	for rows.Next() {
		var agentID, name, status string
		var cost float64
		if err := rows.Scan(&agentID, &name, &status, &cost); err != nil {
			return nil, err
		}
		a, ok := byAgent[agentID]
		if !ok {
			a = &agg{name: name}
			byAgent[agentID] = a
		}
		a.samples++
		a.totalCost += cost
		if status == "done" {
			a.successes++
		}
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	if len(byAgent) == 0 {
		return nil, nil
	}

	// normCost needs the fleet's max avg cost to scale into [0,1].
	maxAvgCost := 0.0
	avgCost := make(map[string]float64, len(byAgent))
	for id, a := range byAgent {
		avg := a.totalCost / float64(a.samples)
		avgCost[id] = avg
		if avg > maxAvgCost {
			maxAvgCost = avg
		}
	}

	out := make([]AgentSuggestion, 0, len(byAgent))
	for id, a := range byAgent {
		successRate := float64(a.successes) / float64(a.samples)
		normCost := 0.0
		if maxAvgCost > 0 {
			normCost = avgCost[id] / maxAvgCost
		}
		out = append(out, AgentSuggestion{
			AgentID:     id,
			AgentName:   a.name,
			Score:       scoreAgent(successRate, normCost),
			Samples:     a.samples,
			SuccessRate: successRate,
			AvgCostUSD:  avgCost[id],
			Grade:       latestGradeLetter(st, id),
		})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Score > out[j].Score })
	if len(out) > n {
		out = out[:n]
	}
	return out, nil
}

// scoreAgent scales success rate to 0..100 and subtracts a cost penalty
// (normCost already 0..1) so a cheaper agent with equal success ranks
// higher — the exact weighting the phase spec calls for: successRate scaled
// minus normalized cost.
func scoreAgent(successRate, normCost float64) float64 {
	return successRate*100 - normCost*20
}

// latestGradeLetter reads the agent's current fleet-calibrated grade for
// display; errors or unscored agents degrade to "" (rendered as "no data").
func latestGradeLetter(st *store.Store, agentID string) string {
	fleet, err := FleetComposites(st, 30)
	if err != nil {
		return ""
	}
	composite, ok := fleet[agentID]
	if !ok {
		return ""
	}
	values := make([]float64, 0, len(fleet))
	for _, v := range fleet {
		values = append(values, v)
	}
	return GradeFor(composite, values).Letter
}
