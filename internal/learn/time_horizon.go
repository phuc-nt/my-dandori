package learn

import (
	"github.com/phuc-nt/dandori/internal/store"
)

// horizonBuckets fixes the bucket labels and boundaries (minutes) in display
// order. GROUP BY does not guarantee row order, so callers must reindex query
// results against this slice rather than trust the order rows come back in.
var horizonBuckets = []struct {
	label    string
	min, max int // max<0 means unbounded
}{
	{"<30m", 0, 30},
	{"30-60m", 30, 60},
	{"60-120m", 60, 120},
	{">120m", 120, -1},
}

// HorizonBucket is one run-duration bucket's outcome rate — the METR-style
// time-horizon curve: does success rate hold up as tasks run longer.
type HorizonBucket struct {
	Label              string
	MinMin, MaxMin     int // MaxMin<0 means unbounded
	Runs, Done         int
	DoneRate           float64
	WilsonLo, WilsonHi float64
}

// Insufficient reports whether this bucket has too few runs to compare.
func (h HorizonBucket) Insufficient() bool { return h.Runs < MinSampleForInsight }

// NoContrast reports the F7 zero-contrast state: every run in the bucket
// finished 'done' (no failed/killed to compare against), so a 100% rate is a
// window-composition artifact, not evidence the bucket is more reliable.
// Callers must show "chưa có run fail để so" instead of selling the rate.
func (h HorizonBucket) NoContrast() bool { return h.Runs > 0 && h.Done == h.Runs }

// TimeHorizon returns fleet-wide success rate by run-duration bucket over the
// window (days<=0 = all-time). Bucket order is fixed by horizonBuckets, not
// by SQL GROUP BY order. Buckets with zero runs are still returned (Runs=0)
// so the curve has a stable shape across refreshes.
func TimeHorizon(st *store.Store, days int) ([]HorizonBucket, error) {
	counts, err := horizonCounts(st, days, "")
	if err != nil {
		return nil, err
	}
	out := make([]HorizonBucket, len(horizonBuckets))
	for i, b := range horizonBuckets {
		c := counts[b.label]
		hb := HorizonBucket{Label: b.label, MinMin: b.min, MaxMin: b.max, Runs: c.runs, Done: c.done}
		if hb.Runs > 0 {
			hb.DoneRate = float64(hb.Done) / float64(hb.Runs)
			hb.WilsonLo, hb.WilsonHi = WilsonInterval(hb.Done, hb.Runs, zWilson95)
		}
		out[i] = hb
	}
	return out, nil
}

// HorizonModelBucket is one bucket×model row — split fine enough to see if a
// given model's reliability degrades on longer tasks, but only shown when the
// slice has enough runs to mean anything.
type HorizonModelBucket struct {
	Label              string
	Model              string
	Runs, Done         int
	DoneRate           float64
	WilsonLo, WilsonHi float64
}

// TimeHorizonByModel splits the horizon curve by model, keeping only
// bucket×model combinations with at least 5 runs — a coarser split than
// MinSampleForInsight because per-model, per-bucket cells are sparser and a
// looser floor still leaves this too easy to over-read.
const minSampleHorizonByModel = 5

func TimeHorizonByModel(st *store.Store, days int) ([]HorizonModelBucket, error) {
	rows, err := st.DB.Query(`
		SELECT
			CASE
				WHEN d < 30  THEN '<30m'
				WHEN d < 60  THEN '30-60m'
				WHEN d < 120 THEN '60-120m'
				ELSE '>120m'
			END AS bucket,
			m,
			count(*),
			sum(CASE WHEN status='done' THEN 1 ELSE 0 END)
		FROM (
			SELECT status, COALESCE(NULLIF(model,''),'unknown') AS m,
			       (julianday(ended_at)-julianday(started_at))*1440.0 AS d
			FROM runs
			WHERE ended_at IS NOT NULL AND started_at IS NOT NULL` + insightWindowClauseCol("started_at", days) + `
		)
		GROUP BY bucket, m`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	order := map[string]int{}
	for i, b := range horizonBuckets {
		order[b.label] = i
	}

	var out []HorizonModelBucket
	for rows.Next() {
		var hb HorizonModelBucket
		if err := rows.Scan(&hb.Label, &hb.Model, &hb.Runs, &hb.Done); err != nil {
			return nil, err
		}
		if hb.Runs < minSampleHorizonByModel {
			continue
		}
		hb.DoneRate = float64(hb.Done) / float64(hb.Runs)
		hb.WilsonLo, hb.WilsonHi = WilsonInterval(hb.Done, hb.Runs, zWilson95)
		out = append(out, hb)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}

	sortHorizonModelBuckets(out, order)
	return out, nil
}

func sortHorizonModelBuckets(out []HorizonModelBucket, order map[string]int) {
	// Stable-ish insertion sort: dataset is small (buckets × models), and this
	// keeps ordering deterministic (bucket order fixed, then model name)
	// without pulling in sort.Slice closures for a handful of rows.
	for i := 1; i < len(out); i++ {
		for j := i; j > 0; j-- {
			a, b := out[j-1], out[j]
			if order[a.Label] < order[b.Label] || (order[a.Label] == order[b.Label] && a.Model <= b.Model) {
				break
			}
			out[j-1], out[j] = out[j], out[j-1]
		}
	}
}

type horizonCount struct{ runs, done int }

func horizonCounts(st *store.Store, days int, _ string) (map[string]horizonCount, error) {
	rows, err := st.DB.Query(`
		SELECT bucket, count(*), sum(done) FROM (
			SELECT
				CASE
					WHEN d < 30  THEN '<30m'
					WHEN d < 60  THEN '30-60m'
					WHEN d < 120 THEN '60-120m'
					ELSE '>120m'
				END AS bucket,
				CASE WHEN status='done' THEN 1 ELSE 0 END AS done
			FROM (
				SELECT status, (julianday(ended_at)-julianday(started_at))*1440.0 AS d
				FROM runs
				WHERE ended_at IS NOT NULL AND started_at IS NOT NULL` + insightWindowClauseCol("started_at", days) + `
			)
		)
		GROUP BY bucket`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	out := map[string]horizonCount{}
	for rows.Next() {
		var label string
		var c horizonCount
		if err := rows.Scan(&label, &c.runs, &c.done); err != nil {
			return nil, err
		}
		out[label] = c
	}
	return out, rows.Err()
}
