package govern

import (
	"context"
	"testing"
)

// The allow path runs on EVERY tool call of every agent session — it must
// stay well under 5ms even with rule loading and budget checks.
func BenchmarkEvaluateAllow(b *testing.B) {
	t := &testing.T{}
	e := testEngine(t)
	seedRun(t, e, "bench-run", 0)
	tc := bashCall("bench-run", "go test ./...")
	ctx := context.Background()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if d := e.Evaluate(ctx, tc); d.Verdict != Allow {
			b.Fatalf("unexpected verdict %s", d.Verdict)
		}
	}
}
