package learn

import (
	"encoding/json"
	"fmt"
	"sort"
	"time"

	"github.com/phuc-nt/dandori/internal/store"
)

// prPayload mirrors ghub.PR without importing the integrations package
// (learn stays a pure read layer over the store).
type prPayload struct {
	Number     int    `json:"number"`
	Author     string `json:"author"`
	CreatedAt  string `json:"createdAt"`
	MergedAt   string `json:"mergedAt"`
	RevertOf   int    `json:"revert_of"`
	RevertedBy int    `json:"reverted_by"`
}

// loadPRs reads github work_items whose merge/update falls in the window.
func loadPRs(st *store.Store, windowDays int) (keys []string, prs []prPayload, err error) {
	rows, err := st.DB.Query(`SELECT key, COALESCE(payload,'') FROM work_items
		WHERE source = 'github' AND updated_at >= ` + windowClause(windowDays))
	if err != nil {
		return nil, nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var key, payload string
		if err := rows.Scan(&key, &payload); err != nil {
			return nil, nil, err
		}
		var p prPayload
		if json.Unmarshal([]byte(payload), &p) != nil {
			continue
		}
		keys = append(keys, key)
		prs = append(prs, p)
	}
	return keys, prs, rows.Err()
}

// CFR is the AI-era change failure rate: merged PRs later reverted / merged
// PRs, computed from synced GitHub data. Provenance = the PR keys counted.
func CFR(st *store.Store, windowDays int) (Metric, error) {
	keys, prs, err := loadPRs(st, windowDays)
	if err != nil {
		return Metric{}, err
	}
	var merged, reverted int
	var mergedKeys []string
	for i, p := range prs {
		if p.MergedAt == "" || p.RevertOf != 0 { // revert PRs themselves don't count as changes
			continue
		}
		merged++
		mergedKeys = append(mergedKeys, keys[i])
		if p.RevertedBy != 0 {
			reverted++
		}
	}
	m := Metric{Name: "cfr", Value: 0, RunIDs: mergedKeys,
		Formula: "no merged PRs in window (0%)"}
	if merged > 0 {
		m.Value = 100 * float64(reverted) / float64(merged)
		m.Formula = fmt.Sprintf("100 × %d reverted / %d merged PRs (revert PRs excluded from denominator)", reverted, merged)
	}
	return m, nil
}

// PRCycleResult carries p50/p75 open→merge latency.
type PRCycleResult struct {
	P50, P75 time.Duration
	Count    int
	Formula  string
	Keys     []string
}

// PRCycle computes merge latency percentiles over merged PRs in the window.
func PRCycle(st *store.Store, windowDays int) (*PRCycleResult, error) {
	keys, prs, err := loadPRs(st, windowDays)
	if err != nil {
		return nil, err
	}
	var durations []time.Duration
	var used []string
	for i, p := range prs {
		if p.MergedAt == "" || p.CreatedAt == "" {
			continue
		}
		c, err1 := time.Parse(time.RFC3339, p.CreatedAt)
		m, err2 := time.Parse(time.RFC3339, p.MergedAt)
		if err1 != nil || err2 != nil || m.Before(c) {
			continue
		}
		durations = append(durations, m.Sub(c))
		used = append(used, keys[i])
	}
	r := &PRCycleResult{Count: len(durations), Keys: used}
	if len(durations) == 0 {
		r.Formula = "no merged PRs with timestamps in window"
		return r, nil
	}
	sort.Slice(durations, func(i, j int) bool { return durations[i] < durations[j] })
	r.P50 = percentileDur(durations, 50)
	r.P75 = percentileDur(durations, 75)
	r.Formula = fmt.Sprintf("open→merge latency over %d merged PRs", len(durations))
	return r, nil
}

// percentileDur uses nearest-rank: ceil(p/100·n)−1 (floor-index would return
// the minimum for small n).
func percentileDur(sorted []time.Duration, p int) time.Duration {
	n := len(sorted)
	if n == 0 {
		return 0
	}
	idx := (p*n + 99) / 100
	if idx < 1 {
		idx = 1
	}
	return sorted[idx-1]
}
