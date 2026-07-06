package learn

import (
	"fmt"

	"github.com/phuc-nt/dandori/internal/store"
)

// MinSampleForInsight is the run count below which a model/cache row is shown
// but flagged "insufficient" rather than compared as if reliable. Printed on
// the UI so the threshold is visible, not a hidden cliff.
const MinSampleForInsight = 3

// ModelStat is per-model efficiency over the window. Ratios always ship with
// N so the reader can judge whether the number means anything.
type ModelStat struct {
	Model         string
	Runs          int
	DoneRuns      int
	TotalCost     float64
	CostPerDone   float64 // TotalCost / DoneRuns; 0 when no done runs
	InputTokens   int64
	CacheRead     int64
	CacheHitRatio float64 // cache_read / (input + cache_read); 0 when both 0
}

// Insufficient reports whether this row has too few runs to compare.
func (m ModelStat) Insufficient() bool { return m.Runs < MinSampleForInsight }

// insightWindowClause is a full " AND started_at >= …" predicate (unlike
// windowClause in metrics.go, which returns only the datetime expression).
// days<=0 means all-time and yields an empty clause.
func insightWindowClause(days int) string {
	if days <= 0 {
		return ""
	}
	return fmt.Sprintf(" AND started_at >= datetime('now', '-%d day')", days)
}

// ModelStats aggregates cost, outcome and cache efficiency per model over the
// window. A run with no model is grouped under "unknown" rather than dropped,
// so the table reconciles against the fleet total. Ordered by run count so the
// models the fleet actually leans on lead.
func ModelStats(st *store.Store, days int) ([]ModelStat, error) {
	rows, err := st.DB.Query(`
		SELECT COALESCE(NULLIF(model,''),'unknown') AS m,
		       count(*),
		       sum(CASE WHEN status='done' THEN 1 ELSE 0 END),
		       COALESCE(sum(cost_usd),0),
		       COALESCE(sum(input_tokens),0),
		       COALESCE(sum(cache_read_tokens),0)
		FROM runs
		WHERE 1=1` + insightWindowClause(days) + `
		GROUP BY m
		ORDER BY count(*) DESC, m`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []ModelStat
	for rows.Next() {
		var s ModelStat
		if err := rows.Scan(&s.Model, &s.Runs, &s.DoneRuns, &s.TotalCost, &s.InputTokens, &s.CacheRead); err != nil {
			return nil, err
		}
		if s.DoneRuns > 0 {
			s.CostPerDone = s.TotalCost / float64(s.DoneRuns)
		}
		if denom := s.InputTokens + s.CacheRead; denom > 0 {
			s.CacheHitRatio = float64(s.CacheRead) / float64(denom)
		}
		out = append(out, s)
	}
	return out, rows.Err()
}

// CacheRunStat is one run's cache utilization — for spotting sessions that
// burned fresh input instead of reusing cached context.
type CacheRunStat struct {
	RunID         string
	AgentID       string
	Model         string
	InputTokens   int64
	CacheRead     int64
	CacheHitRatio float64
}

// TopCacheRuns returns the runs with the highest and lowest cache-hit ratios
// over the window (limit each), skipping runs with no token activity. Useful
// to see which sessions reused context well and which paid full price.
func TopCacheRuns(st *store.Store, days, limit int) (best, worst []CacheRunStat, err error) {
	all, err := cacheRuns(st, days)
	if err != nil {
		return nil, nil, err
	}
	// all is ordered by ratio DESC (see query). Best = head, worst = tail.
	best = headN(all, limit)
	// Keep the two lists disjoint: on a small fleet (fewer than 2×limit runs)
	// a naive tail would re-list the same rows the head already shows. Drop
	// the rows best already claimed before taking the worst.
	worst = reverse(tailN(all[len(best):], limit))
	return best, worst, nil
}

func cacheRuns(st *store.Store, days int) ([]CacheRunStat, error) {
	rows, err := st.DB.Query(`
		SELECT id, COALESCE(agent_id,''), COALESCE(NULLIF(model,''),'unknown'),
		       input_tokens, cache_read_tokens
		FROM runs
		WHERE (input_tokens + cache_read_tokens) > 0` + insightWindowClause(days) + `
		ORDER BY CAST(cache_read_tokens AS REAL) / (input_tokens + cache_read_tokens) DESC`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []CacheRunStat
	for rows.Next() {
		var c CacheRunStat
		if err := rows.Scan(&c.RunID, &c.AgentID, &c.Model, &c.InputTokens, &c.CacheRead); err != nil {
			return nil, err
		}
		if denom := c.InputTokens + c.CacheRead; denom > 0 {
			c.CacheHitRatio = float64(c.CacheRead) / float64(denom)
		}
		out = append(out, c)
	}
	return out, rows.Err()
}

func headN(s []CacheRunStat, n int) []CacheRunStat {
	if n > len(s) {
		n = len(s)
	}
	return s[:n]
}

func tailN(s []CacheRunStat, n int) []CacheRunStat {
	if n > len(s) {
		n = len(s)
	}
	return s[len(s)-n:]
}

func reverse(s []CacheRunStat) []CacheRunStat {
	out := make([]CacheRunStat, len(s))
	for i, v := range s {
		out[len(s)-1-i] = v
	}
	return out
}
