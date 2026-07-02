package web

import (
	"net/http"
	"strings"
	"time"

	"github.com/phuc-nt/dandori/internal/learn"
)

// handleSpikes explains cost anomalies (UF4): pick a date (default today or
// from a chart click) → recent spike events + top contributing runs.
func (s *Server) handleSpikes(w http.ResponseWriter, r *http.Request) {
	date := r.URL.Query().Get("date")
	if date == "" {
		date = time.Now().UTC().Format("2006-01-02")
	}
	agent := r.URL.Query().Get("agent")
	contributors, err := learn.SpikeContributors(s.Store, date, agent)
	if err != nil {
		http.Error(w, err.Error(), 500)
		return
	}
	events, _ := s.querySpikeEvents()
	s.render(w, r, "spikes", map[string]any{
		"Page": "spikes", "Date": date, "Agent": agent,
		"Contributors": contributors, "SpikeEvents": events,
	})
}

type spikeEventRow struct {
	TS, Agent, Detail string
}

func (s *Server) querySpikeEvents() ([]spikeEventRow, error) {
	rows, err := s.Store.DB.Query(`SELECT ts, COALESCE(tool_name,''), COALESCE(payload,'')
		FROM events WHERE kind = 'cost_spike' ORDER BY id DESC LIMIT 30`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []spikeEventRow
	for rows.Next() {
		var e spikeEventRow
		if err := rows.Scan(&e.TS, &e.Agent, &e.Detail); err != nil {
			return nil, err
		}
		out = append(out, e)
	}
	return out, rows.Err()
}

// handleRunCompare (UF3): side-by-side comparison of 2–5 runs.
func (s *Server) handleRunCompare(w http.ResponseWriter, r *http.Request) {
	ids := strings.Split(r.URL.Query().Get("ids"), ",")
	if len(ids) > 5 {
		http.Error(w, "compare at most 5 runs", 400)
		return
	}
	var runs []RunRow
	var events [][]EventRow
	for _, id := range ids {
		id = strings.TrimSpace(id)
		if id == "" {
			continue
		}
		rr, err := s.queryRuns(`WHERE id = ?`, id)
		if err != nil || len(rr) == 0 {
			http.Error(w, "run not found: "+id, 404)
			return
		}
		runs = append(runs, rr[0])
		ev, _ := s.queryEvents(id)
		events = append(events, ev)
	}
	if len(runs) < 2 {
		http.Error(w, "pick at least 2 runs to compare (?ids=a,b)", 400)
		return
	}
	stats := make([]compareStats, len(runs))
	for i, ev := range events {
		stats[i] = summarizeEvents(ev)
	}
	s.render(w, r, "run_compare", map[string]any{
		"Page": "runs", "Runs": runs, "Stats": stats,
	})
}

type compareStats struct {
	ToolCalls, ToolErrors, Blocks, Asks int
}

func summarizeEvents(events []EventRow) compareStats {
	var c compareStats
	for _, e := range events {
		switch e.Kind {
		case "tool_use":
			c.ToolCalls++
		case "tool_result":
			if e.OK != nil && *e.OK == 0 {
				c.ToolErrors++
			}
		case "guardrail_block":
			c.Blocks++
		case "permission_ask":
			c.Asks++
		}
	}
	return c
}
