package web

import (
	"html/template"
	"net/http"
	"strconv"

	"github.com/go-chi/chi/v5"

	"github.com/phuc-nt/dandori/internal/contexthub"
	"github.com/phuc-nt/dandori/internal/govern"
	"github.com/phuc-nt/dandori/internal/observer"
)

// Context Hub UI. Team/agent docs are edited directly (audited). Company docs
// are the most sensitive mutation in the product — they inject into EVERY
// agent org-wide — so company edits and rollbacks are approval-gated through
// the same RequestAction→applier path as chatbot actions (C1), never written
// directly here.

func (s *Server) hub() *contexthub.Hub { return contexthub.New(s.Store) }

// handleContexts renders the editor + doc list.
func (s *Server) handleContexts(w http.ResponseWriter, r *http.Request) {
	docs, err := s.hub().ListDocs()
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	s.render(w, r, "contexts", map[string]any{
		"Page": "contexts", "Docs": docs,
		"Agents": s.listAgents(), "Teams": s.listTeamsForPicker(),
	})
}

// handleContextSave writes a team/agent version directly, or routes a company
// write through approval (C1). Secret-shaped content is rejected (M1).
func (s *Server) handleContextSave(w http.ResponseWriter, r *http.Request) {
	layer := r.FormValue("layer")
	target := r.FormValue("target")
	content := r.FormValue("content")
	note := r.FormValue("note")

	if frag := contexthub.SecretFragment(content); frag != "" {
		s.contextBanner(w, "Nội dung chứa chuỗi giống secret — vui lòng gỡ trước khi lưu: "+frag)
		return
	}

	if layer == contexthub.LayerCompany {
		_, err := observer.RequestAction(s.Store, "context-company-edit", "company:*",
			"Sửa chính sách công ty (chờ duyệt)",
			map[string]any{"content": content}, s.execActor(), "operator")
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		s.contextBanner(w, "Đã gửi duyệt — chính sách công ty sẽ áp dụng sau khi được duyệt trong mục Cần duyệt (Kỹ thuật).")
		return
	}

	if _, err := s.hub().SaveContext(layer, target, content, s.execActor(), note); err != nil {
		if err == contexthub.ErrSecretInContent {
			s.contextBanner(w, err.Error())
			return
		}
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	a := &govern.Audit{St: s.Store, Actor: s.execActor()}
	_, _ = a.Append("context_saved", layer+":"+target, note)
	w.Header().Set("HX-Refresh", "true")
}

// handleContextHistory lists a doc's versions with an empty diff panel.
func (s *Server) handleContextHistory(w http.ResponseWriter, r *http.Request) {
	layer := chi.URLParam(r, "layer")
	target := chi.URLParam(r, "target")
	versions, err := s.hub().Versions(layer, target)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	s.render(w, r, "contexts", map[string]any{
		"Page": "contexts", "HistoryLayer": layer, "HistoryTarget": target,
		"Versions": versions, "CompanyLayer": layer == contexthub.LayerCompany,
	})
}

// handleContextDiff renders the line diff between two versions (HTMX fragment).
func (s *Server) handleContextDiff(w http.ResponseWriter, r *http.Request) {
	layer := r.URL.Query().Get("layer")
	target := r.URL.Query().Get("target")
	to, _ := strconv.Atoi(r.URL.Query().Get("to"))
	from, _ := strconv.Atoi(r.URL.Query().Get("from"))
	if from == 0 {
		from = to - 1
	}
	var beforeC, afterC string
	if d, _ := s.hub().Version(layer, target, from); d != nil {
		beforeC = d.Content
	}
	if d, _ := s.hub().Version(layer, target, to); d != nil {
		afterC = d.Content
	}
	html := contexthub.DiffHTML(beforeC, afterC, "v"+strconv.Itoa(from), "v"+strconv.Itoa(to))
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Write([]byte(html))
}

// handleContextRollback: team/agent roll back directly; company rollback is
// approval-gated (C1), pinning the target version's content.
func (s *Server) handleContextRollback(w http.ResponseWriter, r *http.Request) {
	layer := r.FormValue("layer")
	target := r.FormValue("target")
	toN, _ := strconv.Atoi(r.FormValue("to"))

	if layer == contexthub.LayerCompany {
		old, _ := s.hub().Version(contexthub.LayerCompany, contexthub.CompanyTarget, toN)
		if old == nil {
			http.Error(w, "version not found", http.StatusBadRequest)
			return
		}
		_, err := observer.RequestAction(s.Store, "context-company-edit", "company:*",
			"Khôi phục chính sách công ty về v"+strconv.Itoa(toN)+" (chờ duyệt)",
			map[string]any{"content": old.Content}, s.execActor(), "operator")
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		s.contextBanner(w, "Đã gửi duyệt khôi phục — áp dụng sau khi được duyệt.")
		return
	}

	if _, err := s.hub().Rollback(layer, target, toN, s.execActor()); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	a := &govern.Audit{St: s.Store, Actor: s.execActor()}
	_, _ = a.Append("context_rolled_back", layer+":"+target, "→ v"+strconv.Itoa(toN))
	w.Header().Set("HX-Refresh", "true")
}

// handleContextPromote proposes a team doc as company policy (approval-gated,
// pinning the team head's version so the approver approves fixed bytes — H3).
func (s *Server) handleContextPromote(w http.ResponseWriter, r *http.Request) {
	teamID := r.FormValue("team")
	head, err := s.hub().Head(contexthub.LayerTeam, teamID)
	if err != nil || head == nil {
		http.Error(w, "team context not found", http.StatusBadRequest)
		return
	}
	subject := "team:" + teamID
	// Dedup (L5): skip if an open promote for this team already exists.
	if s.openContextRequest("request_context-promote", subject) {
		s.contextBanner(w, "Đã có một đề xuất đang chờ duyệt cho đội này.")
		return
	}
	_, err = observer.RequestAction(s.Store, "context-promote", subject,
		"Đề xuất chính sách đội "+teamID+" (v"+strconv.Itoa(head.VersionN)+") thành chính sách công ty",
		map[string]any{
			"source_layer": contexthub.LayerTeam, "source_target": teamID,
			"source_version_n": head.VersionN,
		}, s.execActor(), "operator")
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	s.contextBanner(w, "Đã gửi đề xuất — cần duyệt trong mục Cần duyệt (Kỹ thuật).")
}

// handleContextEffective previews an agent's merged effective context.
func (s *Server) handleContextEffective(w http.ResponseWriter, r *http.Request) {
	agent := r.URL.Query().Get("agent")
	data := map[string]any{"Page": "contexts", "Preview": true, "PreviewAgent": agent, "Agents": s.listAgents()}
	if agent != "" {
		text, prov, err := s.hub().EffectiveContext(agent)
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		data["PreviewText"] = text
		data["PreviewProv"] = prov
	}
	s.render(w, r, "contexts", data)
}

func (s *Server) contextBanner(w http.ResponseWriter, msg string) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Write([]byte(`<div class="bg-amber-50 border border-amber-200 rounded p-3 text-sm text-amber-800">` +
		template.HTMLEscapeString(msg) + `</div>`))
}

func (s *Server) openContextRequest(typ, subject string) bool {
	var n int
	_ = s.Store.Read().QueryRow(`SELECT count(*) FROM insights
		WHERE type = ? AND subject = ? AND status IN ('open','surfaced')`, typ, subject).Scan(&n)
	return n > 0
}

func (s *Server) listTeamsForPicker() []struct{ ID, Name string } {
	rows, err := s.Store.Read().Query(`SELECT CAST(id AS TEXT), name FROM teams ORDER BY id`)
	if err != nil {
		return nil
	}
	defer rows.Close()
	var out []struct{ ID, Name string }
	for rows.Next() {
		var t struct{ ID, Name string }
		if rows.Scan(&t.ID, &t.Name) == nil {
			out = append(out, t)
		}
	}
	return out
}

// contextCardData feeds the exec_home read-only card: how many policies are
// live and when one was last updated.
func (s *Server) contextCardData() (count int, lastUpdate string) {
	_ = s.Store.Read().QueryRow(`SELECT count(*) FROM context_heads`).Scan(&count)
	_ = s.Store.Read().QueryRow(`SELECT COALESCE(MAX(created_at),'') FROM context_versions`).Scan(&lastUpdate)
	return count, lastUpdate
}
