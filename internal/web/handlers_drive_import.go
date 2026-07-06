// UC6 Drive → context import. The HIGHEST-security context-hub flow:
// imported Drive content becomes AGENT INSTRUCTIONS (contexthub/inject.go
// injects every layer's content into every agent session's SessionStart) —
// so EVERY layer's import goes through human approval (observer.RequestAction),
// never direct SaveContext (C1). Search and Review are pure reads; only
// Import (below) writes anything, and it writes an approval request, not a
// context version.
package web

import (
	"context"
	"net/http"
	"strings"
	"time"

	"github.com/phuc-nt/dandori/internal/contexthub"
	"github.com/phuc-nt/dandori/internal/integrations"
	"github.com/phuc-nt/dandori/internal/integrations/gws"
	"github.com/phuc-nt/dandori/internal/observer"
)

// driveImportTimeout bounds the search/export calls a browser tab is
// waiting on synchronously — a hung `gws` call must not wedge the console.
const driveImportTimeout = 15 * time.Second

func (s *Server) driveImporter() *gws.DriveImporter {
	guard := &integrations.Guard{Cfg: s.Cfg, St: s.Store}
	return &gws.DriveImporter{GWS: gws.NewRunner(guard), Scanner: contexthub.SecretFragment}
}

// handleDriveSearch renders the result list fragment for a Drive query
// (Google Docs only, folders excluded by DriveImporter.Search).
func (s *Server) handleDriveSearch(w http.ResponseWriter, r *http.Request) {
	q := strings.TrimSpace(r.URL.Query().Get("q"))
	data := map[string]any{"Page": "contexts", "DriveQuery": q}
	if q == "" {
		s.render(w, r, "contexts", data)
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), driveImportTimeout)
	defer cancel()
	files, err := s.driveImporter().Search(ctx, q)
	if err != nil {
		data["DriveError"] = "Tìm kiếm Drive lỗi: " + err.Error()
		s.render(w, r, "contexts", data)
		return
	}
	data["DriveResults"] = files
	s.render(w, r, "contexts", data)
}

// handleDriveReview exports a candidate doc and renders the FULL body
// (scrollable, never truncated — C1) plus the import form, or a block
// message when the size/secret gate trips. The full text is never written
// anywhere here — it only flows into the render and, if the human submits
// the import form, into the approval request's pinned params.
func (s *Server) handleDriveReview(w http.ResponseWriter, r *http.Request) {
	id := r.URL.Query().Get("id")
	name := r.URL.Query().Get("name")
	modified := r.URL.Query().Get("modified")
	data := map[string]any{"Page": "contexts", "Teams": s.listTeamsForPicker(), "Agents": s.listAgents()}
	if id == "" {
		http.Error(w, "missing id", http.StatusBadRequest)
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), driveImportTimeout)
	defer cancel()
	res, err := s.driveImporter().Review(ctx, gws.DriveFile{ID: id, Name: name, ModifiedTime: modified})
	if err != nil {
		data["DriveError"] = "Xem trước Drive lỗi: " + err.Error()
		s.render(w, r, "contexts", data)
		return
	}
	data["DriveReview"] = res
	s.render(w, r, "contexts", data)
}

// handleDriveImport requests an approval-gated import for ANY layer — this
// handler NEVER calls contexthub.SaveContext (C1). The full re-exported text
// is pinned into the approval's params so what the human approves later is
// exactly what was reviewed here, not whatever the doc has become by then.
func (s *Server) handleDriveImport(w http.ResponseWriter, r *http.Request) {
	layer := r.FormValue("layer")
	target := r.FormValue("target")
	docID := r.FormValue("doc_id")
	docName := r.FormValue("doc_name")
	modified := r.FormValue("modified")
	if layer != contexthub.LayerCompany && layer != contexthub.LayerTeam && layer != contexthub.LayerAgent {
		http.Error(w, "unknown layer", http.StatusBadRequest)
		return
	}
	if layer == contexthub.LayerCompany {
		target = contexthub.CompanyTarget
	}
	if target == "" {
		http.Error(w, "target is required", http.StatusBadRequest)
		return
	}
	if docID == "" {
		http.Error(w, "missing doc_id", http.StatusBadRequest)
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), driveImportTimeout)
	defer cancel()
	// Re-export at submit time rather than trusting a hidden form field for
	// the body (M1 — a hidden <textarea> is still attacker-influenceable
	// client-side input; the server must be the one that saw the real Drive
	// content). Review's gates run again here for the same reason.
	res, err := s.driveImporter().Review(ctx, gws.DriveFile{ID: docID, Name: docName, ModifiedTime: modified})
	if err != nil {
		http.Error(w, "Xem trước Drive lỗi: "+err.Error(), http.StatusBadGateway)
		return
	}
	if res.TooBig {
		s.contextBanner(w, "Tài liệu quá lớn để nhập (vượt 8000 ký tự) — vui lòng rút gọn trước khi nhập.")
		return
	}
	if res.HasSecret {
		s.contextBanner(w, "Tài liệu chứa chuỗi giống secret — không thể nhập: "+res.SecretHint)
		return
	}
	subject := layer + ":" + target
	summary := "Nhập tài liệu Drive " + docName + " vào " + subject + " (chờ duyệt)"
	params := map[string]any{
		"layer": layer, "target": target, "content": res.FullText,
		"doc_id": docID, "doc_name": docName, "modified_time": modified,
	}
	if _, err := observer.RequestAction(s.Store, "context-import", subject, summary, params, s.actor(r), "operator"); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	s.contextBanner(w, "Đã gửi duyệt — tài liệu sẽ được nhập vào "+subject+" sau khi được duyệt trong mục Cần duyệt.")
}
