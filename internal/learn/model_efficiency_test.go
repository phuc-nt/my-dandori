package learn

import (
	"fmt"
	"path/filepath"
	"testing"

	"github.com/phuc-nt/dandori/internal/store"
)

func insightTestStore(t *testing.T) *store.Store {
	t.Helper()
	st, err := store.Open(filepath.Join(t.TempDir(), "ins.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	return st
}

// seedInsightRun inserts a run with the fields the insight aggregates read.
func seedInsightRun(t *testing.T, st *store.Store, id, project, agent, model, status string, cost float64, input, cacheRead int64) {
	t.Helper()
	if _, err := st.DB.Exec(`INSERT INTO agents(id, name, created_at) VALUES(?,?,datetime('now'))
		ON CONFLICT(id) DO NOTHING`, agent, agent); err != nil {
		t.Fatal(err)
	}
	_, err := st.DB.Exec(`INSERT INTO runs
		(id, session_id, project, agent_id, model, status, started_at, cost_usd, input_tokens, cache_read_tokens)
		VALUES(?,?,?,?,?,?,datetime('now'),?,?,?)`,
		id, id, project, agent, model, status, cost, input, cacheRead)
	if err != nil {
		t.Fatal(err)
	}
}

func TestModelStats(t *testing.T) {
	st := insightTestStore(t)
	// sonnet: 3 runs (2 done), cost 6 total → $3/done; cache 900/(100+900)=90%
	for i := 0; i < 3; i++ {
		status := "done"
		if i == 2 {
			status = "running"
		}
		seedInsightRun(t, st, fmt.Sprintf("s%d", i), "p", "a", "claude-sonnet-5", status, 2, 100, 900)
	}
	// haiku: 1 run → insufficient sample
	seedInsightRun(t, st, "h0", "p", "a", "claude-haiku", "done", 1, 500, 0)

	stats, err := ModelStats(st, 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(stats) != 2 {
		t.Fatalf("want 2 models, got %d", len(stats))
	}
	// Ordered by run count DESC → sonnet first.
	son := stats[0]
	if son.Model != "claude-sonnet-5" || son.Runs != 3 || son.DoneRuns != 2 {
		t.Errorf("sonnet row wrong: %+v", son)
	}
	if son.CostPerDone != 3.0 {
		t.Errorf("cost/done = %v, want 3.0", son.CostPerDone)
	}
	if son.CacheHitRatio < 0.89 || son.CacheHitRatio > 0.91 {
		t.Errorf("cache ratio = %v, want ~0.9", son.CacheHitRatio)
	}
	if son.Insufficient() {
		t.Error("sonnet n=3 should be sufficient")
	}
	if !stats[1].Insufficient() {
		t.Error("haiku n=1 should be insufficient")
	}
}

func TestModelStatsEmpty(t *testing.T) {
	st := insightTestStore(t)
	stats, err := ModelStats(st, 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(stats) != 0 {
		t.Errorf("empty store should yield no rows, got %d", len(stats))
	}
}

func TestModelStatsUnknownModel(t *testing.T) {
	st := insightTestStore(t)
	seedInsightRun(t, st, "u0", "p", "a", "", "done", 1, 10, 0)
	stats, _ := ModelStats(st, 0)
	if len(stats) != 1 || stats[0].Model != "unknown" {
		t.Errorf("empty model should group as 'unknown': %+v", stats)
	}
}

func TestTopCacheRuns(t *testing.T) {
	st := insightTestStore(t)
	seedInsightRun(t, st, "hi", "p", "a", "m", "done", 1, 100, 900) // 90%
	seedInsightRun(t, st, "lo", "p", "a", "m", "done", 1, 900, 100) // 10%
	seedInsightRun(t, st, "zero", "p", "a", "m", "done", 1, 0, 0)   // no tokens → skipped

	// limit=1 so best/worst are disjoint singletons over the 2 token-bearing
	// runs (the zero-token run is skipped entirely).
	best, worst, err := TopCacheRuns(st, 0, 1)
	if err != nil {
		t.Fatal(err)
	}
	if len(best) != 1 || best[0].RunID != "hi" {
		t.Errorf("best should be the highest-ratio run: %+v", best)
	}
	if len(worst) != 1 || worst[0].RunID != "lo" {
		t.Errorf("worst should be the lowest-ratio run: %+v", worst)
	}
	// Small-fleet disjointness: with limit >= run count, best takes all and
	// worst is empty rather than re-listing the same rows.
	bAll, wAll, _ := TopCacheRuns(st, 0, 5)
	if len(bAll) != 2 || len(wAll) != 0 {
		t.Errorf("limit>=count: best=%d worst=%d, want 2 and 0 (disjoint)", len(bAll), len(wAll))
	}
}
