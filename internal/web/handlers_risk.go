package web

import (
	"net/http"

	"github.com/phuc-nt/dandori/internal/learn"
)

// Risk overview: one page answering "what needs my attention" — agents at D/F,
// open flags, pending approvals, hot budgets. It reuses the reviews_pending and
// budgets_table partials verbatim (no duplicate markup); only the "agents
// needing attention" block is new here.

type riskFlag struct {
	ID      int64
	Agent   string
	Reason  string
	AgeDays int
}

func (s *Server) handleRisk(w http.ResponseWriter, r *http.Request) {
	s.render(w, r, "risk", s.riskData())
}

func (s *Server) handleRiskFragment(w http.ResponseWriter, r *http.Request) {
	s.renderFragment(w, r, "risk", "risk_body", s.riskData())
}

func (s *Server) riskData() map[string]any {
	// Reused, unchanged data sources.
	pending, _ := s.queryApprovals("pending")
	budgets, _ := s.queryBudgets()

	// New: agents currently graded D or F, with trend, most severe first.
	var attention []learn.LeaderboardRow
	if board, err := learn.LeaderboardCalibrated(s.Store, s.Cfg.LearnWindowDays, s.Cfg.CalibrateWithHumans); err == nil {
		for _, row := range board {
			if row.Grade.Letter == "D" || row.Grade.Letter == "F" {
				attention = append(attention, row)
			}
		}
	}

	return map[string]any{
		"Page":      "risk",
		"Pending":   pending,
		"Budgets":   budgets,
		"Attention": attention,
		"Flags":     s.openFlags(),
	}
}

// openFlags lists open governance flags with their age, oldest first.
func (s *Server) openFlags() []riskFlag {
	rows, err := s.Store.DB.Query(`SELECT f.id, COALESCE(r.agent_id,''), COALESCE(f.reason,''),
		CAST(julianday('now') - julianday(f.created_at) AS INTEGER)
		FROM flags f LEFT JOIN runs r ON r.id = f.run_id
		WHERE f.status = 'open'
		ORDER BY f.created_at ASC LIMIT 50`)
	if err != nil {
		return nil
	}
	defer rows.Close()
	var out []riskFlag
	for rows.Next() {
		var f riskFlag
		if err := rows.Scan(&f.ID, &f.Agent, &f.Reason, &f.AgeDays); err != nil {
			return out
		}
		out = append(out, f)
	}
	return out
}
