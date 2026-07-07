package learn

import (
	"sort"
	"strconv"

	"github.com/phuc-nt/dandori/internal/store"
)

// Context-Version ROI: does bumping a context layer (company/team/agent doc)
// actually change outcomes? Joins runs to their FIRST context_injected event
// (F5 — see contextROIQuery) and compares done-rate/cost/steering across
// adjacent versions of the same layer for the same target.
//
// Observational, not a controlled experiment: a version bump often
// correlates with a task-mix or time shift, so callers must present deltas
// as advisory signal, never an auto-act trigger.

// ContextVersionStat is one (layer, target, version) bucket's outcomes over
// the window. Target is a project name for the company layer (grain =
// whichever runs saw that company version) and an agent_id for the agent
// layer — the layer determines what "same context" means.
type ContextVersionStat struct {
	Layer       string // company | team | agent
	Target      string // project (company/team layer) or agent_id (agent layer)
	VersionN    int
	Runs        int
	Done        int
	TotalCost   float64
	Steering    int     // sum of per-run steering counts (F6: local=steering_msg, central=user_msg)
	CostPerDone float64 // TotalCost / Done; 0 when Done == 0
	RunIDs      []string
}

// Insufficient reports whether this bucket has too few runs to compare
// (reuses the fleet-wide MinSampleForInsight threshold from model_efficiency.go).
func (c ContextVersionStat) Insufficient() bool { return c.Runs < MinSampleForInsight }

// VersionDelta compares two adjacent versions of the same (layer, target)
// context. Wilson CIs are attached to both sides so the reader can judge
// whether an apparent improvement is inside the noise band.
type VersionDelta struct {
	Layer    string
	Target   string
	FromV    int
	ToV      int
	RunsFrom int
	RunsTo   int

	DoneRateFrom   float64
	DoneRateTo     float64
	DoneRateFromLo int // Wilson 95% CI, whole percent
	DoneRateFromHi int
	DoneRateToLo   int
	DoneRateToHi   int

	CostPerDoneFrom float64
	CostPerDoneTo   float64

	SteeringRateFrom float64 // steering msgs per run
	SteeringRateTo   float64

	NoContrast bool // one side has RunsX == 0 (F7): no bar to render
}

// contextLayerVer maps a layer name to its JSON key in the context_injected
// payload {"company":vN,"team":vM,"agent":vK} (contexthub.Provenance).
func contextLayerVer(layer string) string {
	switch layer {
	case "company":
		return "company"
	case "team":
		return "team"
	case "agent":
		return "agent"
	default:
		return ""
	}
}

// contextROIRow is one run's contribution to a layer's version buckets,
// scanned from the dedupe-first-injection query.
type contextROIRow struct {
	runID    string
	target   string
	status   string
	cost     float64
	version  int
	steering int
}

// contextROIQuery runs the F5 dedupe (CTE first_inj = MIN(id) per run_id for
// kind='context_injected' — a run fires this event on BOTH startup and
// resume, and a mid-run bump would otherwise place one run on both sides of
// its own comparison) joined to runs, filtered to the given layer's JSON key
// present and non-null, within the window (F8, alias-qualified).
//
// target is project for company/team layers, agent_id for the agent layer
// (Provenance grain: company/team context reflects the project's effective
// doc, agent context is per-agent).
//
// The central-mode (source='ingest') steering scalar subquery wraps its
// COUNT/CAST in MAX(...) rather than a bare scalar select: this relies on the
// one-user_msg-per-run UPSERT invariant (internal/ingest/apply.go), and
// MAX(...) keeps the query deterministic and single-valued even if that
// invariant were ever violated by a duplicate row, instead of depending on
// SQLite's unspecified row-selection order for a multi-row scalar subquery.
func contextROIQuery(st *store.Store, layer string, days int) ([]contextROIRow, error) {
	key := contextLayerVer(layer)
	if key == "" {
		return nil, nil
	}
	targetCol := "r.project"
	if layer == "agent" {
		targetCol = "COALESCE(r.agent_id,'')"
	}
	q := `
		WITH first_inj AS (
			SELECT run_id, MIN(id) AS eid
			FROM events WHERE kind = 'context_injected' GROUP BY run_id
		)
		SELECT r.id, ` + targetCol + ` AS target, r.status, r.cost_usd,
		       json_extract(e.payload,'$.` + key + `') AS ver,
		       CASE WHEN r.source = 'ingest'
		            THEN COALESCE((SELECT MAX(CAST(e3.payload AS INTEGER)) FROM events e3
		                 WHERE e3.run_id = r.id AND e3.kind = 'user_msg'), 0)
		            ELSE (SELECT count(*) FROM events e2
		                 WHERE e2.run_id = r.id AND e2.kind = 'steering_msg')
		       END AS steering
		FROM runs r
		JOIN first_inj fi ON fi.run_id = r.id
		JOIN events e ON e.id = fi.eid
		WHERE json_extract(e.payload,'$.` + key + `') IS NOT NULL
		  AND r.ended_at IS NOT NULL` + insightWindowClauseCol("r.started_at", days)

	rows, err := st.Read().Query(q)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []contextROIRow
	for rows.Next() {
		var r contextROIRow
		if err := rows.Scan(&r.runID, &r.target, &r.status, &r.cost, &r.version, &r.steering); err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

// ContextROI aggregates run outcomes per (layer, target, version) over the
// window, for all three context layers. Empty when the fleet has no
// context_injected data yet — callers should check HasContextData first to
// distinguish "no data" from "no signal in this window".
func ContextROI(st *store.Store, days int) ([]ContextVersionStat, error) {
	buckets := map[[3]string]*ContextVersionStat{} // key: layer|target|version

	for _, layer := range []string{"company", "team", "agent"} {
		rows, err := contextROIQuery(st, layer, days)
		if err != nil {
			return nil, err
		}
		for _, r := range rows {
			key := [3]string{layer, r.target, strconv.Itoa(r.version)}
			b, ok := buckets[key]
			if !ok {
				b = &ContextVersionStat{Layer: layer, Target: r.target, VersionN: r.version}
				buckets[key] = b
			}
			b.Runs++
			if r.status == "done" {
				b.Done++
			}
			b.TotalCost += r.cost
			b.Steering += r.steering
			b.RunIDs = append(b.RunIDs, r.runID)
		}
	}

	out := make([]ContextVersionStat, 0, len(buckets))
	for _, b := range buckets {
		if b.Done > 0 {
			b.CostPerDone = b.TotalCost / float64(b.Done)
		}
		out = append(out, *b)
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Layer != out[j].Layer {
			return out[i].Layer < out[j].Layer
		}
		if out[i].Target != out[j].Target {
			return out[i].Target < out[j].Target
		}
		return out[i].VersionN < out[j].VersionN
	})
	return out, nil
}

// ContextROIPairs groups ContextROI's buckets into adjacent-version deltas
// per (layer, target) and returns them split into "ready" (both sides meet
// MinSampleForInsight) and "insufficient" (at least one side below
// threshold) — the handler renders these in separate sections rather than
// hiding the insufficient ones.
func ContextROIPairs(st *store.Store, days int) (ready []VersionDelta, insufficient []VersionDelta, err error) {
	stats, err := ContextROI(st, days)
	if err != nil {
		return nil, nil, err
	}

	type groupKey struct{ layer, target string }
	groups := map[groupKey][]ContextVersionStat{}
	for _, s := range stats {
		k := groupKey{s.Layer, s.Target}
		groups[k] = append(groups[k], s)
	}

	var keys []groupKey
	for k := range groups {
		keys = append(keys, k)
	}
	sort.Slice(keys, func(i, j int) bool {
		if keys[i].layer != keys[j].layer {
			return keys[i].layer < keys[j].layer
		}
		return keys[i].target < keys[j].target
	})

	for _, k := range keys {
		vs := groups[k]
		sort.Slice(vs, func(i, j int) bool { return vs[i].VersionN < vs[j].VersionN })
		for i := 0; i+1 < len(vs); i++ {
			from, to := vs[i], vs[i+1]
			// Adjacent by sort order, not necessarily by VersionN+1 — a
			// version can be entirely absent from the window (no runs saw
			// it), so we compare the nearest two versions that DO have data
			// rather than requiring consecutive integers.
			d := buildVersionDelta(from, to)
			if from.Insufficient() || to.Insufficient() {
				insufficient = append(insufficient, d)
			} else {
				ready = append(ready, d)
			}
		}
	}
	return ready, insufficient, nil
}

func buildVersionDelta(from, to ContextVersionStat) VersionDelta {
	d := VersionDelta{
		Layer:    from.Layer,
		Target:   from.Target,
		FromV:    from.VersionN,
		ToV:      to.VersionN,
		RunsFrom: from.Runs,
		RunsTo:   to.Runs,

		CostPerDoneFrom: from.CostPerDone,
		CostPerDoneTo:   to.CostPerDone,
	}
	// F7: a zero-run side has no ratio to render — mark "no contrast"
	// instead of drawing a fake 0% bar.
	if from.Runs == 0 || to.Runs == 0 {
		d.NoContrast = true
		return d
	}
	d.DoneRateFrom = float64(from.Done) / float64(from.Runs)
	d.DoneRateTo = float64(to.Done) / float64(to.Runs)
	d.DoneRateFromLo, d.DoneRateFromHi = WilsonPct(from.Done, from.Runs)
	d.DoneRateToLo, d.DoneRateToHi = WilsonPct(to.Done, to.Runs)
	d.SteeringRateFrom = float64(from.Steering) / float64(from.Runs)
	d.SteeringRateTo = float64(to.Steering) / float64(to.Runs)
	return d
}

// HasContextData reports whether the fleet has recorded ANY context_injected
// event yet. The live DB currently has 0 rows (contexts feature shipped in
// v5/v8 but no fleet has configured layered context yet) — the handler must
// show an honest "chưa có dữ liệu context injection" + setup nudge rather
// than rendering fabricated zero-value charts (docs/07 principle #4).
func HasContextData(st *store.Store) (bool, error) {
	var exists int
	err := st.Read().QueryRow(`SELECT EXISTS(SELECT 1 FROM events WHERE kind = 'context_injected')`).Scan(&exists)
	if err != nil {
		return false, err
	}
	return exists == 1, nil
}
