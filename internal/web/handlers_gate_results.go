// Gate-results rendering for a run: the per-check pass/fail rows plus the
// UB4 override control on any failed, not-yet-overridden check.
package web

import (
	"net/http"

	"github.com/go-chi/chi/v5"
)

// GateResultRow is one post-check run for a run — a pass, a still-failing
// check awaiting a decision, or an overridden failure (kept, never deleted).
type GateResultRow struct {
	CheckName                          string
	OK                                 bool
	Output                             string
	Overridden                         bool
	OverriddenAt, OverriddenBy, Reason string
}

func (s *Server) queryGateResults(runID string) ([]GateResultRow, error) {
	rows, err := s.Store.Read().Query(`SELECT check_name, ok, COALESCE(output,''),
		COALESCE(overridden_at,''), COALESCE(overridden_by,''), COALESCE(override_reason,'')
		FROM gate_results WHERE run_id = ? ORDER BY id`, runID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []GateResultRow
	for rows.Next() {
		var g GateResultRow
		var ok int
		if err := rows.Scan(&g.CheckName, &ok, &g.Output, &g.OverriddenAt, &g.OverriddenBy, &g.Reason); err != nil {
			return nil, err
		}
		g.OK = ok != 0
		g.Overridden = g.OverriddenAt != ""
		out = append(out, g)
	}
	return out, rows.Err()
}

// handleRunGateResults is the HTMX-loaded fragment on run_detail showing
// each post-check gate result with an Override control on failed, not-yet-
// overridden rows (UB4).
func (s *Server) handleRunGateResults(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	s.renderRunGateResults(w, id)
}

func (s *Server) renderRunGateResults(w http.ResponseWriter, runID string) {
	results, err := s.queryGateResults(runID)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	s.renderFragment(w, "run_detail", "run_gate_results", map[string]any{
		"RunID": runID, "GateResults": results,
	})
}

// auditAppender adapts Server.audit (which logs failures rather than
// returning them) to learn.Auditor's Append signature.
type auditAppender struct{ s *Server }

func (a *auditAppender) Append(action, subject, detail string) (int64, error) {
	a.s.audit(action, subject, detail)
	return 0, nil
}
