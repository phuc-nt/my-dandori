package learn

import (
	"strings"

	"github.com/phuc-nt/dandori/internal/store"
)

// Steering economics: does mid-run human steering associate with a better or
// worse outcome, and what kind of steering is it (corrective vs. scope-add
// vs. just informational)? Aggregate fleet-level only — this is NOT a
// per-person ranking (Goodhart ban, docs/07 §3.4). decided_by/steering
// author identity is never surfaced here.
//
// F6 numerator per capture mode: local runs (source IN 'hook','watcher') emit
// one `steering_msg` text event per mid-run message — COUNT(steering_msg) is
// the honest numerator. Central-mode runs (source='ingest') only ever get a
// single `user_msg` count event (internal/ingest/apply.go has no transcript
// to read per-message) — those runs use that count instead and must be
// labelled distinctly by callers (they carry no taxonomy, texts never left
// the client).

// SteerBucket is one side of the steer>0 / steer=0 split: outcome rate with
// a Wilson CI, plus average steering density for the runs that have a
// measurable duration.
type SteerBucket struct {
	Runs       int
	Done       int
	DoneRate   float64 // Done/Runs; 0 when Runs==0
	WilsonLo   float64
	WilsonHi   float64
	AvgDensity float64 // mean(numerator/duration_min) over runs with duration>0
}

// SteerCategory is one keyword-heuristic taxonomy bucket over steering_msg
// text. Heuristic is always true — this is NOT NLP/LLM classification, just
// a small keyword seed (locked scope, see phase-05 risk table).
type SteerCategory struct {
	Name      string // corrective | scope-add | informational | approval | other
	Count     int
	Heuristic bool
}

// SeqIndexBucket buckets steering messages by their ORDER-OF-EVENTS position
// within a run (early/mid/late thirds by event id), never by wall-clock ts.
// steering_msg.ts is a sync artifact (syncSteeringTexts deletes+reinserts the
// whole set with a fresh Now() on every reconcile — COUNT(DISTINCT ts)=1 per
// run in production), so any position signal derived from ts would be
// fabricated. Sequence index (ORDER BY event id) is the only honest position
// signal available.
type SeqIndexBucket struct {
	Early int
	Mid   int
	Late  int
}

// SteeringSummary is the full steering-economics result: outcome split by
// steer>0 vs steer=0, taxonomy over steering_msg text, and the sequence-index
// position distribution — plus the kept/total denominators so callers can
// show "trên N giữ lại / M đếm được" whenever steeringTextsCap truncated the
// text population (F4b; capture/transcript.go steeringTextsCap=32KB/run).
type SteeringSummary struct {
	WithSteer    SteerBucket
	WithoutSteer SteerBucket
	Taxonomy     []SteerCategory
	SeqIndex     SeqIndexBucket
	KeptTotal    int // steering_msg rows actually read (kept after cap truncation)
	CountTotal   int // user_msg numerator summed across runs with texts (M in "N/M")
}

// steerRun is one run's steering numerator + outcome + duration, read once
// and then split into the two buckets in Go (avoids two round-trip queries
// with duplicated window/join logic).
type steerRun struct {
	done       bool
	numerator  int
	durationMn float64 // 0 when duration unknown/non-positive — density skips it
}

// SteeringEconomics computes the steer>0/steer=0 outcome split and the
// keyword-heuristic taxonomy over the window. days<=0 means all-time.
func SteeringEconomics(st *store.Store, days int) (SteeringSummary, error) {
	var sum SteeringSummary

	runs, err := steerRuns(st, days)
	if err != nil {
		return sum, err
	}
	var withSteer, withoutSteer []steerRun
	for _, r := range runs {
		if r.numerator > 0 {
			withSteer = append(withSteer, r)
		} else {
			withoutSteer = append(withoutSteer, r)
		}
	}
	sum.WithSteer = buildSteerBucket(withSteer)
	sum.WithoutSteer = buildSteerBucket(withoutSteer)

	taxonomy, seqIdx, kept, total, err := steeringTaxonomy(st, days)
	if err != nil {
		return sum, err
	}
	sum.Taxonomy = taxonomy
	sum.SeqIndex = seqIdx
	sum.KeptTotal = kept
	sum.CountTotal = total

	return sum, nil
}

// buildSteerBucket reduces a slice of steerRun into rate/CI/density. n==0
// (F7: "chưa có contrast" side of the split) leaves every field at its zero
// value — callers must render "no contrast on this side" rather than a fake
// 0%, exactly like WilsonInterval already does for n<=0.
func buildSteerBucket(runs []steerRun) SteerBucket {
	b := SteerBucket{Runs: len(runs)}
	if b.Runs == 0 {
		return b
	}
	var densitySum float64
	var densityN int
	for _, r := range runs {
		if r.done {
			b.Done++
		}
		if r.durationMn > 0 {
			densitySum += float64(r.numerator) / r.durationMn
			densityN++
		}
	}
	b.DoneRate = float64(b.Done) / float64(b.Runs)
	b.WilsonLo, b.WilsonHi = WilsonInterval(b.Done, b.Runs, zWilson95)
	if densityN > 0 {
		b.AvgDensity = densitySum / float64(densityN)
	}
	return b
}

// steerRuns reads one row per ended run in the window: outcome + steering
// numerator (F6 per-mode: local mode counts steering_msg text events,
// central mode has no per-message text so it falls back to the run's
// user_msg count) + duration in minutes (0 when either timestamp is
// missing/non-positive, so density skips it instead of dividing by ~0).
//
// The user_msg side (um) is grouped by run_id with MAX(payload) rather than
// joined ungrouped: this relies on the one-user_msg-per-run UPSERT invariant
// (internal/ingest/apply.go), and GROUP BY + MAX keeps the join
// deterministic and single-row-per-run even if that invariant were ever
// violated by a duplicate row, instead of silently fanning the LEFT JOIN out
// into duplicate run rows.
func steerRuns(st *store.Store, days int) ([]steerRun, error) {
	rows, err := st.DB.Query(`
		SELECT r.status,
		       CASE WHEN r.source = 'ingest' THEN COALESCE(um.n, 0) ELSE COALESCE(sm.n, 0) END AS numerator,
		       (julianday(r.ended_at) - julianday(r.started_at)) * 1440.0 AS dur_min
		FROM runs r
		LEFT JOIN (SELECT run_id, count(*) AS n FROM events WHERE kind = 'steering_msg' GROUP BY run_id) sm
		       ON sm.run_id = r.id
		LEFT JOIN (SELECT run_id, MAX(CAST(payload AS INTEGER)) AS n FROM events WHERE kind = 'user_msg' GROUP BY run_id) um
		       ON um.run_id = r.id
		WHERE r.ended_at IS NOT NULL AND r.started_at IS NOT NULL` + insightWindowClauseCol("r.started_at", days))
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []steerRun
	for rows.Next() {
		var status string
		var numerator int
		var durMin float64
		if err := rows.Scan(&status, &numerator, &durMin); err != nil {
			return nil, err
		}
		if durMin < 0 {
			durMin = 0
		}
		out = append(out, steerRun{done: status == "done", numerator: numerator, durationMn: durMin})
	}
	return out, rows.Err()
}

// taxonomy keyword rules (Vietnamese + English seed, both languages appear
// in the fleet's transcripts). Order matters: first match wins, so more
// specific corrective/approval phrasing is checked before the informational
// catch-alls. This is a small heuristic seed, not NLP — "other" is expected
// to hold a large share (system markers like <command-message>,
// <ide_opened_file>, "[Request interrupted...]" all fall to other, see F6
// manual-audit note in the phase report).
var taxonomyRules = []struct {
	name     string
	keywords []string
}{
	{"corrective", []string{"won't work", "wont work", "sai", "thử lại", "thu lai", "not working", "fix", "lỗi", "loi ", "revert", "quay lại", "quay lai"}},
	{"scope-add", []string{"also", "thêm", "them ", "bổ sung", "bo sung", "additionally", "ngoài ra", "ngoai ra"}},
	{"approval", []string{"yes", "approve", "ok", "được", "duoc", "đồng ý", "dong y", "ổn", "on roi", "chuẩn", "chuan"}},
	{"informational", []string{"note", "fyi", "lưu ý", "luu y", "for your information", "context:"}},
}

// classify returns the first matching taxonomy category name, or "other"
// when no keyword rule matches (case-insensitive substring match).
func classify(text string) string {
	lower := strings.ToLower(text)
	for _, rule := range taxonomyRules {
		for _, kw := range rule.keywords {
			if strings.Contains(lower, kw) {
				return rule.name
			}
		}
	}
	return "other"
}

// steeringTaxonomy reads every steering_msg text row (local mode only —
// central mode never has per-message text to classify) ORDER BY id, applies
// the keyword heuristic, and buckets sequence-index position by thirds
// within each run. kept is the number of rows actually read (post cap
// truncation); total is the per-run steering numerator summed across runs
// that have at least one kept text row, so callers can show the honest
// "kept/total" denominator (F4b) instead of implying kept==total.
func steeringTaxonomy(st *store.Store, days int) (categories []SteerCategory, seqIdx SeqIndexBucket, kept, total int, err error) {
	rows, err := st.DB.Query(`
		SELECT e.run_id, e.id, COALESCE(e.payload, '')
		FROM events e
		JOIN runs r ON r.id = e.run_id
		WHERE e.kind = 'steering_msg' AND r.started_at IS NOT NULL` + insightWindowClauseCol("r.started_at", days) + `
		ORDER BY e.run_id, e.id`)
	if err != nil {
		return nil, seqIdx, 0, 0, err
	}
	defer rows.Close()

	counts := map[string]int{}
	// perRun buffers each run's texts so sequence-index thirds can be
	// computed against that run's own kept-row count (a run with 3 kept
	// texts has different third-boundaries than one with 70).
	type runRow struct{ id int64 }
	perRun := map[string][]runRow{}
	var order []string
	seenRun := map[string]bool{}

	for rows.Next() {
		var runID, text string
		var id int64
		if err := rows.Scan(&runID, &id, &text); err != nil {
			return nil, seqIdx, 0, 0, err
		}
		kept++
		counts[classify(text)]++
		perRun[runID] = append(perRun[runID], runRow{id: id})
		if !seenRun[runID] {
			seenRun[runID] = true
			order = append(order, runID)
		}
	}
	if err := rows.Err(); err != nil {
		return nil, seqIdx, 0, 0, err
	}

	for _, runID := range order {
		list := perRun[runID]
		n := len(list)
		if n == 0 {
			continue
		}
		thirdLo := n / 3
		thirdHi := (2 * n) / 3
		for i := range list {
			switch {
			case i < thirdLo:
				seqIdx.Early++
			case i < thirdHi:
				seqIdx.Mid++
			default:
				seqIdx.Late++
			}
		}
	}

	total, err = steerNumeratorTotal(st, days, order)
	if err != nil {
		return nil, seqIdx, 0, 0, err
	}

	for _, rule := range taxonomyRules {
		categories = append(categories, SteerCategory{Name: rule.name, Count: counts[rule.name], Heuristic: true})
	}
	categories = append(categories, SteerCategory{Name: "other", Count: counts["other"], Heuristic: true})

	return categories, seqIdx, kept, total, nil
}

// steerNumeratorTotal sums the per-run steering numerator (F6: COUNT(steering_msg)
// for local, since these are all local-mode runs by construction — only local
// mode ever writes steering_msg text) for the runs that contributed at least
// one kept text row, giving the "M counted" side of the kept/total ratio.
func steerNumeratorTotal(st *store.Store, days int, runIDs []string) (int, error) {
	if len(runIDs) == 0 {
		return 0, nil
	}
	placeholders := strings.TrimSuffix(strings.Repeat("?,", len(runIDs)), ",")
	args := make([]any, len(runIDs))
	for i, id := range runIDs {
		args[i] = id
	}
	var total int
	q := `SELECT COALESCE(sum(n), 0) FROM (
		SELECT run_id, count(*) AS n FROM events
		WHERE kind = 'steering_msg' AND run_id IN (` + placeholders + `)
		GROUP BY run_id)`
	err := st.DB.QueryRow(q, args...).Scan(&total)
	return total, err
}
