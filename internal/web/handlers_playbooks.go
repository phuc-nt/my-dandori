package web

import (
	"encoding/json"
	"net/http"
	"sort"

	"github.com/go-chi/chi/v5"

	"github.com/phuc-nt/dandori/internal/capture"
	"github.com/phuc-nt/dandori/internal/redact"
	"github.com/phuc-nt/dandori/internal/store"
)

// PlaybookRow is one saved starting kit.
type PlaybookRow struct {
	ID                                  int64
	Name, RunID, AgentID, TaskKey       string
	Prompt, Model, Notes, CreatedAt, By string
	CostUSD                             float64
	TopFiles                            []string
}

// handlePlaybookCreate packages a good run into a reusable playbook (UG4):
// prompt, most-touched files, model and cost norm — the LEARN flywheel.
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
	topFiles, _ := json.Marshal(s.topFiles(runID, 5))
	// Prompts come from the raw transcript (unredacted on disk) and users
	// paste credentials into first prompts — redact before persisting.
	_, err = s.Store.DB.Exec(`INSERT INTO playbooks(name, run_id, agent_id, task_key, prompt, model, cost_usd, top_files, notes, created_at, created_by)
		VALUES(?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		name, runID, run.AgentID, run.TaskKey, redact.String(prompt), run.Model, run.CostUSD,
		string(topFiles), redact.String(r.FormValue("notes")), store.Now(), s.Cfg.UserName)
	if err != nil {
		http.Error(w, err.Error(), 500)
		return
	}
	s.audit("save_playbook", runID, name)
	http.Redirect(w, r, "/playbooks", http.StatusSeeOther)
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
