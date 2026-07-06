package web

import (
	"fmt"
	"html/template"
	"net/http"
	"strconv"

	"github.com/phuc-nt/dandori/internal/govern"
	"github.com/phuc-nt/dandori/internal/runner"
)

// handleBulkKill kills every selected run through the SAME audited path as a
// single kill (process-group signal under lock + govern.KillRun). It reports
// per-outcome counts and NEVER claims a blanket "all killed" — a run with no
// live registry entry is only 'marked', not signalled (M4).
func (s *Server) handleBulkKill(w http.ResponseWriter, r *http.Request) {
	r.ParseForm()
	ids := r.Form["run_ids"]
	if len(ids) == 0 {
		s.bulkResult(w, "Chưa chọn run nào.")
		return
	}
	actor := s.actor(r)
	var signaled, marked, failed int
	for _, id := range ids {
		var (
			out runner.KillOutcome
			err error
		)
		if s.Launcher != nil {
			out, err = s.Launcher.Kill(id, actor, "bulk kill")
		} else {
			err = govern.KillRun(s.Store, id, actor, "bulk kill")
			out = runner.Marked
		}
		switch {
		case err != nil:
			failed++
		case out == runner.Signaled:
			signaled++
		default:
			marked++
		}
	}
	s.bulkResult(w, fmt.Sprintf(
		"%d đã dừng process · %d chỉ đánh dấu (không có process sống) · %d lỗi",
		signaled, marked, failed))
}

// handleBulkBudget sets a monthly budget on the AGENT of each selected run
// (budget is per-scope, not per-run). Each write is audited; failures don't
// abort the loop.
func (s *Server) handleBulkBudget(w http.ResponseWriter, r *http.Request) {
	r.ParseForm()
	ids := r.Form["run_ids"]
	amount, err := strconv.ParseFloat(r.FormValue("amount"), 64)
	if err != nil || amount <= 0 {
		s.bulkResult(w, "Hạn mức không hợp lệ.")
		return
	}
	if len(ids) == 0 {
		s.bulkResult(w, "Chưa chọn run nào.")
		return
	}
	actor := s.actor(r)
	seen := map[string]bool{}
	var set, failed int
	for _, id := range ids {
		var agentID string
		if s.Store.Read().QueryRow(`SELECT COALESCE(agent_id,'') FROM runs WHERE id = ?`, id).Scan(&agentID) != nil || agentID == "" {
			failed++
			continue
		}
		if seen[agentID] {
			continue // one budget per agent, even if several runs share it
		}
		seen[agentID] = true
		if _, e := s.Store.DB.Exec(`INSERT INTO budgets(scope_type, scope_id, limit_usd)
			VALUES('agent', ?, ?) ON CONFLICT(scope_type, scope_id) DO UPDATE SET limit_usd = excluded.limit_usd`,
			agentID, amount); e != nil {
			failed++
			continue
		}
		a := &govern.Audit{St: s.Store, Actor: actor}
		_, _ = a.Append("budget_set", "agent:"+agentID, fmt.Sprintf("limit → $%.2f (bulk)", amount))
		set++
	}
	s.bulkResult(w, fmt.Sprintf("Đặt ngân sách cho %d agent · %d lỗi", set, failed))
}

func (s *Server) bulkResult(w http.ResponseWriter, msg string) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	// Escape even though today's messages are integer counts — keeps this from
	// becoming a reflected-XSS sink one edit away (reviewer M1).
	w.Write([]byte(`<div class="bg-slate-100 border rounded p-2 text-sm text-slate-700">` +
		template.HTMLEscapeString(msg) + `</div>`))
}
