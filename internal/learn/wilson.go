package learn

import (
	"fmt"
	"math"
)

// zWilson95 is the z-score for a 95% confidence interval, the default used
// across insight tables (done-rate, acceptance, cache-hit, salvage-rate).
const zWilson95 = 1.96

// WilsonInterval returns the Wilson score interval [lo, hi] for a proportion
// of successes/n, pure closed-form arithmetic (no stats lib, keeps the
// pure-Go/no-CGO build). n<=0 is undefined for a proportion — callers show
// their own "no data" state instead of dividing by zero.
func WilsonInterval(successes, n int, z float64) (lo, hi float64) {
	if n <= 0 {
		return 0, 0
	}
	p := float64(successes) / float64(n)
	nf := float64(n)
	z2 := z * z
	center := (p + z2/(2*nf)) / (1 + z2/nf)
	half := z * math.Sqrt(p*(1-p)/nf+z2/(4*nf*nf)) / (1 + z2/nf)
	lo = clamp01(center - half)
	hi = clamp01(center + half)
	return lo, hi
}

func clamp01(v float64) float64 {
	if v < 0 {
		return 0
	}
	if v > 1 {
		return 1
	}
	return v
}

// WilsonPct is WilsonInterval at the 95% default, rounded to whole percent
// for direct use in templates and summary rows.
func WilsonPct(successes, n int) (lo, hi int) {
	loF, hiF := WilsonInterval(successes, n, zWilson95)
	return int(math.Round(loF * 100)), int(math.Round(hiF * 100))
}

// FormatWilson renders a ratio with its 95% Wilson CI, e.g.
// "75% (CI 30–95%, n=4)". n==0 has no ratio to show at all.
func FormatWilson(successes, n int) string {
	if n == 0 {
		return "— (chưa có mẫu)"
	}
	lo, hi := WilsonPct(successes, n)
	pct := int(math.Round(float64(successes) / float64(n) * 100))
	return fmt.Sprintf("%d%% (CI %d–%d%%, n=%d)", pct, lo, hi, n)
}

// insightWindowClauseCol is the alias-qualifiable, column-parameterized
// sibling of insightWindowClause (model_efficiency.go): callers that need a
// column other than the bare "started_at" (e.g. "r.started_at" behind a
// join, or "requested_at" for approvals) use this instead. days<=0 means
// all-time and yields an empty clause.
func insightWindowClauseCol(col string, days int) string {
	if days <= 0 {
		return ""
	}
	return fmt.Sprintf(" AND %s >= datetime('now','-%d day')", col, days)
}
