package learn

import (
	"fmt"
	"testing"

	"github.com/phuc-nt/dandori/internal/store"
	"github.com/phuc-nt/dandori/internal/store/testseed"
)

// Fleet-scale guard: 50 agents × 20 runs each must render the org dashboard
// comfortably (<300ms per Leaderboard call on dev hardware).
func BenchmarkLeaderboard50Agents(b *testing.B) {
	t := &testing.T{}
	st, err := store.Open(b.TempDir() + "/bench.db")
	if err != nil {
		b.Fatal(err)
	}
	defer st.Close()
	for a := 0; a < 50; a++ {
		agent := fmt.Sprintf("agent-%02d", a)
		testseed.Agent(t, st, agent)
		for r := 0; r < 20; r++ {
			runID := fmt.Sprintf("%s-r%02d", agent, r)
			testseed.Run(t, st, runID, agent, "done", r%14, 0.5)
			testseed.Event(t, st, runID, "tool_use", "Edit", -1, "")
			testseed.Event(t, st, runID, "tool_result", "Edit", 1, "")
		}
	}
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		rows, err := Leaderboard(st, 30)
		if err != nil || len(rows) != 50 {
			b.Fatalf("rows=%d err=%v", len(rows), err)
		}
	}
}
