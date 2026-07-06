package learn

import (
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/phuc-nt/dandori/internal/store"
)

// Impact is an advisory, best-effort estimate of what approving an action like
// this has cost before: the average run cost and files touched across the
// agent's recent similar approvals. It is never presented as certain — the UI
// labels it "ước lượng từ N lần trước".
type Impact struct {
	Samples  int     `json:"samples"`
	AvgCost  float64 `json:"avg_cost"`
	AvgFiles float64 `json:"avg_files"`
}

const minImpactSamples = 3

// EstimateImpact returns an advisory estimate for a pending approval of the
// given agent + action, or ok=false when there is not enough history (< 3
// similar approvals) or the action is synthetic (a band-demote / observer
// proposal has no meaningful run cost). Results are cached in the settings
// table (per agent + action-type + hour bucket), mirroring the ai_review cache
// — no bespoke in-memory map, no new schema.
func EstimateImpact(st *store.Store, agentID, action string) (*Impact, bool) {
	if isSyntheticAction(action) {
		return nil, false
	}
	typ := actionType(action)
	if typ == "" {
		return nil, false
	}

	// Hour-bucketed cache key: cheap reuse under the reviews page's 3s poll
	// without a mutex.
	hour := time.Now().UTC().Format("2006-01-02T15")
	cacheKey := fmt.Sprintf("impact:%s:%s:%s", agentID, typ, hour)
	if cached := st.Setting(cacheKey); cached != "" {
		var im Impact
		if json.Unmarshal([]byte(cached), &im) == nil {
			if im.Samples < minImpactSamples {
				return nil, false
			}
			return &im, true
		}
	}

	im, ok := computeImpact(st, agentID, typ)
	// Cache both hits and misses (misses as samples<3) so a cold action does
	// not re-scan on every poll.
	if b, err := json.Marshal(im); err == nil {
		_ = st.SetSetting(cacheKey, string(b))
	}
	if !ok {
		return nil, false
	}
	return im, true
}

// computeImpact scans the agent's recent approved approvals of this action type
// (runs-first via idx_runs_agent), averaging run cost and Edit/Write file
// touches. Bounded to the 10 most recent samples.
//
// The scan runs in two passes because Store.DB is a single-connection pool
// (SetMaxOpenConns(1)): a per-row fileTouches QueryRow while the outer rows
// cursor is still open would deadlock on the same connection. Pass 1 collects
// matching (runID, cost) with the cursor; pass 2 queries file touches after it
// is closed.
func computeImpact(st *store.Store, agentID, typ string) (*Impact, bool) {
	rows, err := st.DB.Query(`SELECT ap.action, r.id, r.cost_usd
		FROM runs r
		JOIN approvals ap ON ap.run_id = r.id
		WHERE r.agent_id = ? AND ap.status = 'approved'
		ORDER BY r.started_at DESC
		LIMIT 100`, agentID)
	if err != nil {
		return &Impact{}, false
	}
	type sample struct {
		runID string
		cost  float64
	}
	var samples []sample
	for rows.Next() {
		var action, runID string
		var cost float64
		if err := rows.Scan(&action, &runID, &cost); err != nil {
			rows.Close()
			return &Impact{}, false
		}
		if actionType(action) != typ {
			continue
		}
		samples = append(samples, sample{runID, cost})
		if len(samples) >= 10 {
			break
		}
	}
	rows.Close()

	im := &Impact{Samples: len(samples)}
	if len(samples) == 0 {
		return im, false
	}
	var totalCost, totalFiles float64
	for _, s := range samples {
		totalCost += s.cost
		totalFiles += float64(fileTouches(st, s.runID))
	}
	im.AvgCost = totalCost / float64(len(samples))
	im.AvgFiles = totalFiles / float64(len(samples))
	return im, len(samples) >= minImpactSamples
}

// fileTouches counts edit-style tool calls in a run (a proxy for files changed).
func fileTouches(st *store.Store, runID string) int {
	var n int
	_ = st.DB.QueryRow(`SELECT COUNT(*) FROM events
		WHERE run_id = ? AND kind = 'tool_use'
		  AND tool_name IN ('Edit','Write','NotebookEdit')`, runID).Scan(&n)
	return n
}

// isSyntheticAction reports whether an action is a governance-internal proposal
// (band-demote / observer) rather than a real agent tool call. Their "run cost"
// is unrelated to the decision, so estimating it would be misleading.
func isSyntheticAction(action string) bool {
	return strings.HasPrefix(action, "band-demote:") || strings.HasPrefix(action, "observer:")
}

// actionType classifies an approval action the way gate.go builds it: a
// tool-shaped action ("Edit /path", "Write /x") types as the tool name; a raw
// command types as its first token (the binary); anything else types as its
// first whitespace token. This matches real DB values, not imagined ones.
func actionType(action string) string {
	action = strings.TrimSpace(action)
	if action == "" {
		return ""
	}
	first := action
	if i := strings.IndexByte(action, ' '); i >= 0 {
		first = action[:i]
	}
	switch first {
	case "Edit", "Write", "NotebookEdit", "Read", "Bash":
		return first
	}
	return first // raw command → binary name (git, rm, curl, …)
}
