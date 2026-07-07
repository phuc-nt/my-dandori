package web

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"sort"
	"strconv"

	"github.com/go-chi/chi/v5"

	"github.com/phuc-nt/dandori/internal/capture"
	"github.com/phuc-nt/dandori/internal/learn"
)

// PlaybookRow is one saved starting kit.
type PlaybookRow struct {
	ID                                  int64
	Name, RunID, AgentID, TaskKey       string
	Prompt, Model, Notes, CreatedAt, By string
	CostUSD                             float64
	TopFiles                            []string
}

// handlePlaybookCreate (H3) no longer writes the playbooks table directly —
// that bypassed the knowledge review gate entirely (a viewer's "save as
// playbook" click went live with zero review, unlike every other knowledge
// kind). It now nominates a kind=playbook knowledge_units row instead (F9:
// nominate stays viewer-ok); the REAL playbooks row is only created by
// applyKnowledgePlaybookWriteTx after an admin approves knowledge-publish at
// /reviews, same gate PromoteCandidate's auto-detected candidates go through.
func (s *Server) handlePlaybookCreate(w http.ResponseWriter, r *http.Request) {
	runID := chi.URLParam(r, "id")
	name := r.FormValue("name")
	if name == "" {
		http.Error(w, "playbook needs a name", 400)
		return
	}
	runs, err := s.queryRuns(`WHERE id = ?`, runID)
	if err != nil || len(runs) == 0 {
		http.NotFound(w, r)
		return
	}
	run := runs[0]
	prompt := ""
	if run.ID != "" {
		var transcript string
		s.Store.DB.QueryRow(`SELECT COALESCE(transcript_path,'') FROM runs WHERE id = ?`, runID).Scan(&transcript)
		if transcript != "" {
			if u, err := capture.ParseTranscript(transcript); err == nil {
				prompt = u.FirstUser
			}
		}
	}
	_ = prompt // retained for future playbook body/prompt capture (not yet part of NominateParams)

	unitID, err := learn.NominateUnit(s.Store, learn.NominateParams{
		Kind:          learn.KindPlaybook,
		Name:          learn.PlaybookSlug(runID),
		Title:         name,
		RefKind:       "run",
		ProvenanceRun: []string{runID},
		NominatedBy:   s.actor(r),
	})
	if err != nil {
		if errors.Is(err, learn.ErrDuplicateDraft) {
			http.Error(w, err.Error(), http.StatusConflict)
			return
		}
		http.Error(w, err.Error(), 500)
		return
	}
	s.audit(r, "nominate_playbook", runID, name)
	http.Redirect(w, r, fmt.Sprintf("/knowledge/unit/%d", unitID), http.StatusSeeOther)
}

// topFiles ranks the files a run touched most (Read/Edit/Write payloads).
func (s *Server) topFiles(runID string, n int) []string {
	rows, err := s.Store.DB.Query(`SELECT COALESCE(payload,'') FROM events
		WHERE run_id = ? AND kind = 'tool_use' AND tool_name IN ('Read','Edit','Write')`, runID)
	if err != nil {
		return nil
	}
	defer rows.Close()
	counts := map[string]int{}
	for rows.Next() {
		var payload string
		if rows.Scan(&payload) != nil {
			continue
		}
		var in struct {
			FilePath string `json:"file_path"`
		}
		if json.Unmarshal([]byte(payload), &in) == nil && in.FilePath != "" {
			counts[in.FilePath]++
		}
	}
	type fc struct {
		f string
		c int
	}
	var all []fc
	for f, c := range counts {
		all = append(all, fc{f, c})
	}
	sort.Slice(all, func(i, j int) bool { return all[i].c > all[j].c })
	var out []string
	for i := 0; i < len(all) && i < n; i++ {
		out = append(out, all[i].f)
	}
	return out
}

// handlePlaybooks lists saved playbooks.
func (s *Server) handlePlaybooks(w http.ResponseWriter, r *http.Request) {
	rows, err := s.Store.DB.Query(`SELECT id, name, COALESCE(run_id,''), COALESCE(agent_id,''), COALESCE(task_key,''),
		COALESCE(prompt,''), COALESCE(model,''), COALESCE(notes,''), created_at, COALESCE(created_by,''),
		COALESCE(cost_usd,0), COALESCE(top_files,'[]') FROM playbooks ORDER BY id DESC`)
	if err != nil {
		http.Error(w, err.Error(), 500)
		return
	}
	defer rows.Close()
	var out []PlaybookRow
	for rows.Next() {
		var p PlaybookRow
		var files string
		if err := rows.Scan(&p.ID, &p.Name, &p.RunID, &p.AgentID, &p.TaskKey,
			&p.Prompt, &p.Model, &p.Notes, &p.CreatedAt, &p.By, &p.CostUSD, &files); err != nil {
			http.Error(w, err.Error(), 500)
			return
		}
		json.Unmarshal([]byte(files), &p.TopFiles)
		out = append(out, p)
	}
	s.render(w, r, "playbooks", map[string]any{"Page": "playbooks", "Playbooks": out})
}

// handlePlaybookAdopt records that someone starts working from a playbook —
// the flywheel's explicit adoption signal. metric_before freezes now;
// outcomes compute later and stay private (coaching, not ranking).
func (s *Server) handlePlaybookAdopt(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseInt(chi.URLParam(r, "id"), 10, 64)
	if err != nil {
		http.Error(w, "bad playbook id", http.StatusBadRequest)
		return
	}
	operator := r.FormValue("operator")
	if operator == "" {
		operator = s.actor(r)
	}
	if _, err := learn.RecordAdoption(s.Store, id, operator, "", s.Cfg.LearnWindowDays); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	w.Write([]byte(`<span class="text-green-600 text-sm">Đã ghi nhận — chúc chạy mượt!</span>`))
}
