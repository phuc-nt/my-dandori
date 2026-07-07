package web

import (
	"database/sql"
	"fmt"
	"html/template"
	"net/http"
	"strconv"
	"strings"

	"github.com/go-chi/chi/v5"

	"github.com/phuc-nt/dandori/internal/contexthub"
	"github.com/phuc-nt/dandori/internal/learn"
	"github.com/phuc-nt/dandori/internal/observer"
)

// Knowledge Units console (P3). One pipeline queue (detected → nominated →
// in_review → published → adopted/measured; branches rejected/retired/
// superseded) wrapping the existing distribution rails (context/rule/
// playbook are ref-not-duplicate; skill owns its body directly). Auth split
// (F9): nominate is any authenticated operator (viewer OK); every decide
// route (submit/publish-request/reject) is admin-only via requireAdmin in
// routes_knowledge.go. Content is immutable after nominate — an edit is a
// new row with supersedes_id, never an in-place mutation.

const knowledgeMaxBodyBytes = learn.MaxUnitBodySize

// handleKnowledgeQueue renders the /knowledge list, filterable by state+kind.
func (s *Server) handleKnowledgeQueue(w http.ResponseWriter, r *http.Request) {
	state := r.URL.Query().Get("state")
	kind := r.URL.Query().Get("kind")
	units, err := learn.ListUnits(s.Store, state)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	if kind != "" {
		filtered := units[:0]
		for _, u := range units {
			if u.Kind == kind {
				filtered = append(filtered, u)
			}
		}
		units = filtered
	}
	compliance, _ := learn.AgentCompliance(s.Store) // best-effort (F13): DB error → empty list, page still renders
	data := map[string]any{
		"Page": "knowledge", "Units": units,
		"State": state, "Kind": kind,
		"Compliance": compliance,
	}
	if isHTMX(r) {
		s.renderFragment(w, r, "knowledge", "knowledge_list", data)
		return
	}
	s.render(w, r, "knowledge", data)
}

// handleKnowledgeUnit renders one unit's full evidence (F1/C1: full body, no
// truncation) with LIVE-recomputed stats (F11 — the stored snapshot is only
// the nominate-time audit record, never the number shown to a reviewer) and
// an observational + regression-to-mean caveat (F10).
func (s *Server) handleKnowledgeUnit(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseInt(chi.URLParam(r, "id"), 10, 64)
	if err != nil {
		http.Error(w, "bad id", http.StatusBadRequest)
		return
	}
	u, err := learn.GetUnit(s.Store, id)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	if u == nil {
		http.Error(w, "unit not found", http.StatusNotFound)
		return
	}
	// F11: recompute present/absent Wilson CI live from the CURRENT stored
	// counts on the unit row rather than trusting any cached percent — the
	// unit row itself is the only source (no separate live re-query exists
	// per kind), so "recompute" here means "always derive the CI from the
	// integer counts at render time," never persist/reuse a pre-formatted CI.
	present, absent := 0, 0
	if u.NPresent != nil {
		present = *u.NPresent
	}
	if u.NAbsent != nil {
		absent = *u.NAbsent
	}
	donePresent := 0
	if u.DonePresent != nil {
		donePresent = int(*u.DonePresent*float64(present) + 0.5)
	}
	doneAbsent := 0
	if u.DoneAbsent != nil {
		doneAbsent = int(*u.DoneAbsent*float64(absent) + 0.5)
	}
	openPublish := s.openKnowledgeRequest(u.ID)
	data := map[string]any{
		"Page": "knowledge", "Unit": u,
		"PresentCI":                 learn.FormatWilson(donePresent, present),
		"AbsentCI":                  learn.FormatWilson(doneAbsent, absent),
		"OpenRequestPending":        openPublish,
		"OpenMandateRequestPending": s.openKnowledgeRequestType(u.ID, "knowledge-mandate"),
		"OpenRetireRequestPending":  s.openKnowledgeRequestType(u.ID, "knowledge-retire"),
	}
	// P4: kit's Body column is only the manifest — the real per-file content
	// lives in knowledge_kit_files, loaded here (not the review-queue path)
	// so /knowledge/unit/:id can show the manifest path+size list AND expand
	// full per-file bodies, recomputed from these SAME rows on every render.
	if u.Kind == learn.KindKit {
		files, err := learn.KitFiles(s.Store, u.ID)
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		data["KitFiles"] = files
		// Version bump: show a per-file diff against the superseded version so
		// a reviewer can see exactly what changed, not just the new manifest.
		if u.SupersedesID != nil {
			prevFiles, err := learn.KitFiles(s.Store, *u.SupersedesID)
			if err == nil && len(prevFiles) > 0 {
				data["KitDiff"] = buildKitDiff(prevFiles, files, *u.SupersedesID, u.ID)
			}
		}
	}
	s.render(w, r, "knowledge", data)
}

// kitDiffEntry is one path's diff against the superseded version — Added/
// Removed/Changed classify without needing a template to inspect two slices,
// and DiffHTML is only computed for Changed (Added/Removed show the whole
// body once, a diff against "" would just be a wall of + or - lines).
type kitDiffEntry struct {
	Path    string
	Status  string // "added" | "removed" | "changed"
	Body    string // full body for added/removed
	DiffH   template.HTML
	OldSize int
	NewSize int
}

// buildKitDiff compares two kit versions' file sets by path, producing one
// entry per path that differs (identical content_hash paths are omitted —
// nothing to review there).
func buildKitDiff(oldFiles, newFiles []learn.KitFileRow, oldUnitID, newUnitID int64) []kitDiffEntry {
	oldByPath := make(map[string]learn.KitFileRow, len(oldFiles))
	for _, f := range oldFiles {
		oldByPath[f.Path] = f
	}
	newByPath := make(map[string]learn.KitFileRow, len(newFiles))
	for _, f := range newFiles {
		newByPath[f.Path] = f
	}
	var out []kitDiffEntry
	for _, nf := range newFiles {
		of, existed := oldByPath[nf.Path]
		switch {
		case !existed:
			out = append(out, kitDiffEntry{Path: nf.Path, Status: "added", Body: nf.Body, NewSize: nf.Size})
		case of.ContentHash != nf.ContentHash:
			out = append(out, kitDiffEntry{
				Path: nf.Path, Status: "changed", OldSize: of.Size, NewSize: nf.Size,
				DiffH: contexthub.DiffHTML(of.Body, nf.Body,
					fmt.Sprintf("unit #%d", oldUnitID), fmt.Sprintf("unit #%d", newUnitID)),
			})
		}
	}
	for _, of := range oldFiles {
		if _, stillPresent := newByPath[of.Path]; !stillPresent {
			out = append(out, kitDiffEntry{Path: of.Path, Status: "removed", Body: of.Body, OldSize: of.Size})
		}
	}
	return out
}

// handleKnowledgeNominate lets ANY authenticated operator (viewer included,
// F9) propose a candidate directly — capped at 64KB, secret-scanned, and
// name-deduped BEFORE calling NominateUnit so a flood of junk from a
// low-trust caller fails fast at the boundary instead of inside the learn
// package's own tx.
//
// C2 (v13 P2, load-bearing for the whole anti-Goodhart origin badge): this
// handler ALSO reads origin/origin_model/provenance_run_ids from the form —
// prior to this fix every nomination landed origin='human' regardless of
// what the form posted (import/draft callers included), so the badge never
// persisted. provenance_run_ids is attacker-controllable (any authenticated
// operator can nominate, F9) — each id is validated against a real `runs`
// row before persist; ANY unknown id rejects the WHOLE nominate, so a
// nominator cannot forge a "đã đúc" (minted-from-evidence) badge pointing at
// a run that never produced this content. P3's draft-fill POSTs to this same
// route (it fills origin=ai-draft + origin_model on the form) — it does not
// edit this handler.
func (s *Server) handleKnowledgeNominate(w http.ResponseWriter, r *http.Request) {
	r.Body = http.MaxBytesReader(w, r.Body, knowledgeMaxBodyBytes+4096) // +headroom for other form fields
	if err := r.ParseForm(); err != nil {
		http.Error(w, "form quá lớn (giới hạn 64KB nội dung)", http.StatusRequestEntityTooLarge)
		return
	}
	kind := r.FormValue("kind")
	name := r.FormValue("name")
	title := r.FormValue("title")
	body := r.FormValue("body")
	layer := r.FormValue("layer")
	layerTarget := r.FormValue("layer_target")
	origin := r.FormValue("origin")
	originModel := r.FormValue("origin_model")

	if len(body) > knowledgeMaxBodyBytes {
		http.Error(w, fmt.Sprintf("nội dung vượt giới hạn %d byte", knowledgeMaxBodyBytes), http.StatusBadRequest)
		return
	}
	if frag := contexthub.SecretFragment(body); frag != "" {
		s.contextBanner(w, "Nội dung chứa chuỗi giống secret — vui lòng gỡ trước khi đề cử: "+frag)
		return
	}
	if !learn.ValidSlug(name) {
		http.Error(w, fmt.Sprintf("tên không hợp lệ — dùng kebab-case, bắt đầu bằng chữ/số, tối đa %d ký tự", learn.MaxSlugLen), http.StatusBadRequest)
		return
	}
	if s.knowledgeDraftPending(kind, name) {
		s.contextBanner(w, fmt.Sprintf("%s %q đang có một bản nháp chờ duyệt (nominated/in_review) — đợi bản đó được publish/từ chối trước khi đề cử tiếp.", kind, name))
		return
	}

	provenance, err := s.parseProvenanceRunIDs(r.FormValue("provenance_run_ids"))
	if err != nil {
		s.contextBanner(w, err.Error())
		return
	}

	p := learn.NominateParams{
		Kind: kind, Name: name, Title: title, Body: body,
		Layer: layer, LayerTarget: layerTarget,
		NominatedBy:   s.actor(r),
		Origin:        origin,
		OriginModel:   originModel,
		ProvenanceRun: provenance,
	}
	if _, err := learn.NominateUnit(s.Store, p); err != nil {
		s.contextBanner(w, err.Error())
		return
	}
	w.Header().Set("HX-Refresh", "true")
}

// parseProvenanceRunIDs splits a comma/space-separated provenance_run_ids
// form value and validates EACH id against a real `runs` row (C2) — this is
// the attacker-controllable boundary: the field is free text on a form any
// authenticated operator (viewer included, F9) can submit, so a nominator
// could otherwise type in an arbitrary run id and forge a "đã đúc" badge on
// evidence that never produced this content. ANY unknown id rejects the
// WHOLE nominate (fail closed) rather than silently dropping the bad id and
// keeping the good ones, which would let a nominator learn which ids are
// real by trial and error while still getting a partial forged badge through.
//
// A genuinely unknown id (sql.ErrNoRows) is the only case labeled "forged" —
// any OTHER error from the lookup (locked db, disk I/O, etc.) is a distinct
// transient failure and must say so, not accuse the nominator of forgery for
// a problem that is ours, not theirs.
func (s *Server) parseProvenanceRunIDs(raw string) ([]string, error) {
	fields := strings.FieldsFunc(raw, func(r rune) bool {
		return r == ',' || r == ' ' || r == '\t' || r == '\n'
	})
	if len(fields) == 0 {
		return nil, nil
	}
	out := make([]string, 0, len(fields))
	seen := map[string]bool{}
	for _, f := range fields {
		id := strings.TrimSpace(f)
		if id == "" || seen[id] {
			continue
		}
		seen[id] = true
		var exists int
		err := s.Store.Read().QueryRow(`SELECT 1 FROM runs WHERE id = ?`, id).Scan(&exists)
		switch {
		case err == nil:
			out = append(out, id)
		case err == sql.ErrNoRows:
			return nil, fmt.Errorf("provenance run id không tồn tại: %q — đề cử bị từ chối để tránh làm giả bằng chứng", id)
		default:
			return nil, fmt.Errorf("lỗi kiểm tra provenance run id %q: %w", id, err)
		}
	}
	return out, nil
}

// handleKnowledgeSubmit moves a unit from nominated → in_review (admin-only,
// wired via requireAdmin in routes_knowledge.go).
func (s *Server) handleKnowledgeSubmit(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseInt(chi.URLParam(r, "id"), 10, 64)
	if err != nil {
		http.Error(w, "bad id", http.StatusBadRequest)
		return
	}
	if err := learn.SubmitForReview(s.Store, id, s.actor(r)); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	redirectBack(w, r, fmt.Sprintf("/knowledge/unit/%d", id))
}

// handleKnowledgeReject rejects a nominated/in_review unit with a required
// reason (admin-only).
func (s *Server) handleKnowledgeReject(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseInt(chi.URLParam(r, "id"), 10, 64)
	if err != nil {
		http.Error(w, "bad id", http.StatusBadRequest)
		return
	}
	why := r.FormValue("note")
	if why == "" {
		http.Error(w, "cần lý do từ chối", http.StatusBadRequest)
		return
	}
	if err := learn.RejectUnit(s.Store, id, s.actor(r), why); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	redirectBack(w, r, "/knowledge")
}

// handleKnowledgePublishRequest opens the gated knowledge-publish approval
// (admin-only). F5 idempotency: a second click while one request is still
// open is a no-op with a banner, not a second approval row.
func (s *Server) handleKnowledgePublishRequest(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseInt(chi.URLParam(r, "id"), 10, 64)
	if err != nil {
		http.Error(w, "bad id", http.StatusBadRequest)
		return
	}
	if s.openKnowledgeRequest(id) {
		s.contextBanner(w, "Đã có một đề xuất publish đang chờ duyệt cho unit này.")
		return
	}
	if _, err := learn.RequestPublish(s.Store, observer.RequestAction, id, s.actor(r)); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	redirectBack(w, r, fmt.Sprintf("/knowledge/unit/%d", id))
}

// openKnowledgeRequest reports whether a "knowledge-publish" request for this
// unit is still open (status open/surfaced) — mirrors openContextRequest
// (handlers_contexts.go:154), but keyed on the insight.type written by
// requestUnitAction ("request_knowledge-publish") and the exact subject
// format "<kind>:<name>" it builds (knowledge_units_actions.go), fetched via
// unit id since the handler only has the numeric id.
func (s *Server) openKnowledgeRequest(unitID int64) bool {
	return s.openKnowledgeRequestType(unitID, "knowledge-publish")
}

// openKnowledgeRequestType generalizes openKnowledgeRequest to any of the
// three knowledge-* request types (F13: mandate/retire need the same open-
// request guard publish already has, so a double-click can't open two
// approval rows for the same unit).
func (s *Server) openKnowledgeRequestType(unitID int64, typ string) bool {
	u, err := learn.GetUnit(s.Store, unitID)
	if err != nil || u == nil {
		return false
	}
	subject := fmt.Sprintf("%s:%s", u.Kind, u.Name)
	var n int
	_ = s.Store.Read().QueryRow(`SELECT count(*) FROM insights
		WHERE type = ? AND subject = ? AND status IN ('open','surfaced')`,
		"request_"+typ, subject).Scan(&n)
	return n > 0
}

// handleKnowledgeMandate opens the gated "knowledge-mandate" approval
// (admin-only, F13) — the reachable-from-UI point where a manager decides to
// make a published unit's compliance visible (SessionStart notice). Never
// changes state directly; applyKnowledgeMandate (observer package) does that
// only after a human approves at /reviews.
func (s *Server) handleKnowledgeMandate(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseInt(chi.URLParam(r, "id"), 10, 64)
	if err != nil {
		http.Error(w, "bad id", http.StatusBadRequest)
		return
	}
	if s.openKnowledgeRequestType(id, "knowledge-mandate") {
		s.contextBanner(w, "Đã có một đề xuất mandate đang chờ duyệt cho unit này.")
		return
	}
	if _, err := learn.RequestMandate(s.Store, observer.RequestAction, id, s.actor(r)); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	redirectBack(w, r, fmt.Sprintf("/knowledge/unit/%d", id))
}

// handleKnowledgeRetire opens the gated "knowledge-retire" approval
// (admin-only, F13).
func (s *Server) handleKnowledgeRetire(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseInt(chi.URLParam(r, "id"), 10, 64)
	if err != nil {
		http.Error(w, "bad id", http.StatusBadRequest)
		return
	}
	if s.openKnowledgeRequestType(id, "knowledge-retire") {
		s.contextBanner(w, "Đã có một đề xuất retire đang chờ duyệt cho unit này.")
		return
	}
	if _, err := learn.RequestRetire(s.Store, observer.RequestAction, id, s.actor(r)); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	redirectBack(w, r, fmt.Sprintf("/knowledge/unit/%d", id))
}

// knowledgeDraftPending (M5 — renamed from knowledgeNameLive, whose doc
// comment and banner text wrongly claimed this checked
// published/adopted/measured; the SQL has always checked the DRAFT states)
// reports whether a (kind,name) pair already has a nominated/in_review row —
// nominate-time dedup guidance at the handler boundary (F9), a friendlier
// pre-check duplicating NominateUnit's own draft-dedup (which returns
// learn.ErrDuplicateDraft on the same condition, now backed by the
// idx_ku_kind_name_draft partial unique index, M1). A live PUBLISHED head
// existing is NOT checked here — that is exactly what makes a new nominate a
// legitimate "v2 supersedes v1" proposal, never blocked.
func (s *Server) knowledgeDraftPending(kind, name string) bool {
	var n int
	_ = s.Store.Read().QueryRow(`SELECT count(*) FROM knowledge_units
		WHERE kind = ? AND name = ? AND state IN ('nominated','in_review')`, kind, name).Scan(&n)
	return n > 0
}
