package learn

import (
	"sort"

	"github.com/phuc-nt/dandori/internal/store"
)

// Approval funnel: how many requests reach each stage, and how long a human
// takes to decide. Aggregate action-type level ONLY — this is NOT a
// per-person ranking (Goodhart ban, docs/07 §3.4). decided_by is read
// elsewhere only to count decision coverage, never to rank or rate a person.
//
// F2: `expired` is NOT a human decision. gate.go expireStale sweeps pending
// approvals past the TTL and sets status='expired', decided_at=<sweep time>
// — that decided_at is the janitor's clock, not a person's. Publishing
// latency over "decided_at IS NOT NULL" would silently include expiry sweeps
// as if a human had acted. Latency in this file is computed ONLY over
// status IN ('approved','rejected'); expired is tracked as its own funnel
// stage (and is, as of today's fleet, the dominant signal — 9/9 decided rows
// are expiry sweeps, zero human decisions).

// FunnelStages is the count of approval requests at each terminal/pending
// state over the window.
type FunnelStages struct {
	Requested int // total rows in window (any status)
	Approved  int
	Rejected  int
	Expired   int // TTL sweep, not a human decision (F2)
	Pending   int
}

// ActionLatency is the human-decision latency for one action type, computed
// ONLY over approved/rejected rows (F2) — never expired.
type ActionLatency struct {
	Action    string
	Count     int
	MedianMin float64
	P90Min    float64
}

// FunnelResult is the full approval-funnel result. HasHumanDecisions gates
// whether ByAction should be rendered at all: when false, every "decided"
// row in the window is an expiry sweep and callers must show the empty-state
// "chưa có quyết định người, chỉ có expiry sweep" instead of a latency table
// built entirely from janitor timestamps.
type FunnelResult struct {
	Stages            FunnelStages
	ByAction          []ActionLatency
	HasHumanDecisions bool
}

// ApprovalFunnel computes stage counts and per-action human-decision latency
// over the window. days<=0 means all-time. Approvals has no started_at
// column, so the window is applied to requested_at (F8).
func ApprovalFunnel(st *store.Store, days int) (FunnelResult, error) {
	var res FunnelResult

	stages, err := funnelStages(st, days)
	if err != nil {
		return res, err
	}
	res.Stages = stages
	res.HasHumanDecisions = stages.Approved+stages.Rejected > 0

	byAction, err := funnelLatencyByAction(st, days)
	if err != nil {
		return res, err
	}
	res.ByAction = byAction

	return res, nil
}

func funnelStages(st *store.Store, days int) (FunnelStages, error) {
	var s FunnelStages
	rows, err := st.DB.Query(`
		SELECT status, count(*) FROM approvals
		WHERE 1=1` + insightWindowClauseCol("requested_at", days) + `
		GROUP BY status`)
	if err != nil {
		return s, err
	}
	defer rows.Close()
	for rows.Next() {
		var status string
		var n int
		if err := rows.Scan(&status, &n); err != nil {
			return s, err
		}
		s.Requested += n
		switch status {
		case "approved":
			s.Approved = n
		case "rejected":
			s.Rejected = n
		case "expired":
			s.Expired = n
		case "pending":
			s.Pending = n
		}
	}
	return s, rows.Err()
}

// funnelLatencyByAction computes median/p90 decision latency per action,
// restricted to status IN ('approved','rejected') (F2) — expired rows never
// enter this query.
func funnelLatencyByAction(st *store.Store, days int) ([]ActionLatency, error) {
	rows, err := st.DB.Query(`
		SELECT action, (julianday(decided_at) - julianday(requested_at)) * 1440.0 AS lat_min
		FROM approvals
		WHERE status IN ('approved','rejected') AND decided_at IS NOT NULL` +
		insightWindowClauseCol("requested_at", days) + `
		ORDER BY action`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	byAction := map[string][]float64{}
	var order []string
	for rows.Next() {
		var action string
		var latMin float64
		if err := rows.Scan(&action, &latMin); err != nil {
			return nil, err
		}
		if latMin < 0 {
			latMin = 0
		}
		if _, ok := byAction[action]; !ok {
			order = append(order, action)
		}
		byAction[action] = append(byAction[action], latMin)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}

	out := make([]ActionLatency, 0, len(order))
	for _, action := range order {
		vals := byAction[action]
		sorted := append([]float64(nil), vals...)
		sort.Float64s(sorted)
		out = append(out, ActionLatency{
			Action:    action,
			Count:     len(sorted),
			MedianMin: median(sorted),
			P90Min:    percentileFloat(sorted, 90),
		})
	}
	return out, nil
}

// percentileFloat uses nearest-rank (ceil(p/100·n)−1), matching percentileDur
// in github_intel.go but over float64 minutes instead of time.Duration.
func percentileFloat(sorted []float64, p int) float64 {
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
