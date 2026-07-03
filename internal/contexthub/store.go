// Package contexthub owns layered org context docs (company → team → agent),
// their immutable version history, and the merge into an agent's effective
// context. Content is human-curated policy DATA distributed to every agent
// session, so writes are validated (no raw secrets) and — for the company
// layer — approval-gated by the web layer, never here.
package contexthub

import (
	"database/sql"
	"errors"
	"fmt"

	"github.com/phuc-nt/dandori/internal/redact"
	"github.com/phuc-nt/dandori/internal/store"
)

// ErrSecretInContent is returned by SaveContext when the content contains a
// secret-shaped substring. Context is curated policy: silently redacting it
// would corrupt the policy meaning org-wide, while silently accepting it
// would break the no-raw-secrets invariant — so we fail loud and make the
// author reword. Legit prose like "rotate your Bearer token" trips this too;
// that false-positive is an accepted cost (reword the example to ship).
var ErrSecretInContent = errors.New("nội dung chứa chuỗi giống secret — vui lòng gỡ hoặc viết lại trước khi lưu")

// Layer names.
const (
	LayerCompany  = "company"
	LayerTeam     = "team"
	LayerAgent    = "agent"
	CompanyTarget = "*" // company docs always target "*"
)

// Doc is one context doc at a specific version (head or historical).
type Doc struct {
	Layer     string
	TargetID  string
	VersionN  int
	Content   string
	Author    string
	Note      string
	CreatedAt string
}

// Hub is the store-backed context API.
type Hub struct{ St *store.Store }

func New(st *store.Store) *Hub { return &Hub{St: st} }

func validLayer(l string) bool {
	return l == LayerCompany || l == LayerTeam || l == LayerAgent
}

// SecretFragment returns the first secret-shaped substring in content, for a
// human-readable save error. Empty when content is clean.
func SecretFragment(content string) string {
	red := redact.String(content)
	if red == content {
		return ""
	}
	// Find the first run that differs — good enough to point the author at it.
	for i := 0; i < len(content) && i < len(red); i++ {
		if content[i] != red[i] {
			end := i + 24
			if end > len(content) {
				end = len(content)
			}
			return content[i:end]
		}
	}
	return "(gần cuối nội dung)"
}

// SaveContext writes a new immutable version and moves the head. Rejects
// secret-shaped content (M1). version_n is computed atomically from
// MAX(version_n) inside the tx; a UNIQUE race gets one internal retry so two
// console tabs never surface a raw 500.
func (h *Hub) SaveContext(layer, targetID, content, author, note string) (int, error) {
	if !validLayer(layer) {
		return 0, fmt.Errorf("unknown layer %q", layer)
	}
	if redact.String(content) != content {
		return 0, ErrSecretInContent
	}
	if layer == LayerCompany {
		targetID = CompanyTarget
	}
	for attempt := 0; attempt < 2; attempt++ {
		n, err := h.saveOnce(layer, targetID, content, author, note)
		if err == nil {
			return n, nil
		}
		// A duplicate version_n from a concurrent writer is the only retryable case.
		if attempt == 0 && isUniqueViolation(err) {
			continue
		}
		return 0, err
	}
	return 0, fmt.Errorf("save context: version race did not settle")
}

func (h *Hub) saveOnce(layer, targetID, content, author, note string) (int, error) {
	tx, err := h.St.DB.Begin()
	if err != nil {
		return 0, err
	}
	defer tx.Rollback()

	var ctxID int64
	err = tx.QueryRow(`SELECT id FROM contexts WHERE layer = ? AND target_id = ?`, layer, targetID).Scan(&ctxID)
	if err == sql.ErrNoRows {
		res, e := tx.Exec(`INSERT INTO contexts(layer, target_id, created_at, updated_at) VALUES(?, ?, ?, ?)`,
			layer, targetID, store.Now(), store.Now())
		if e != nil {
			return 0, e
		}
		ctxID, _ = res.LastInsertId()
	} else if err != nil {
		return 0, err
	} else {
		if _, e := tx.Exec(`UPDATE contexts SET updated_at = ? WHERE id = ?`, store.Now(), ctxID); e != nil {
			return 0, e
		}
	}

	var nextN int
	if err := tx.QueryRow(`SELECT COALESCE(MAX(version_n),0)+1 FROM context_versions WHERE context_id = ?`, ctxID).
		Scan(&nextN); err != nil {
		return 0, err
	}
	res, err := tx.Exec(`INSERT INTO context_versions(context_id, version_n, content, author, note, created_at)
		VALUES(?, ?, ?, ?, ?, ?)`, ctxID, nextN, content, author, note, store.Now())
	if err != nil {
		return 0, err
	}
	verID, _ := res.LastInsertId()
	if _, err := tx.Exec(`INSERT INTO context_heads(context_id, version_id) VALUES(?, ?)
		ON CONFLICT(context_id) DO UPDATE SET version_id = excluded.version_id`, ctxID, verID); err != nil {
		return 0, err
	}
	if err := tx.Commit(); err != nil {
		return 0, err
	}
	return nextN, nil
}

// Head returns the current version of a doc, or nil (no error) when absent.
func (h *Hub) Head(layer, targetID string) (*Doc, error) {
	if layer == LayerCompany {
		targetID = CompanyTarget
	}
	d := &Doc{Layer: layer, TargetID: targetID}
	err := h.St.Read().QueryRow(`SELECT v.version_n, v.content, v.author, v.note, v.created_at
		FROM contexts c
		JOIN context_heads hd ON hd.context_id = c.id
		JOIN context_versions v ON v.id = hd.version_id
		WHERE c.layer = ? AND c.target_id = ?`, layer, targetID).
		Scan(&d.VersionN, &d.Content, &d.Author, &d.Note, &d.CreatedAt)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return d, nil
}

// Version returns a specific historical version (for diff + pinned apply).
func (h *Hub) Version(layer, targetID string, n int) (*Doc, error) {
	if layer == LayerCompany {
		targetID = CompanyTarget
	}
	d := &Doc{Layer: layer, TargetID: targetID, VersionN: n}
	err := h.St.Read().QueryRow(`SELECT v.content, v.author, v.note, v.created_at
		FROM contexts c JOIN context_versions v ON v.context_id = c.id
		WHERE c.layer = ? AND c.target_id = ? AND v.version_n = ?`, layer, targetID, n).
		Scan(&d.Content, &d.Author, &d.Note, &d.CreatedAt)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return d, nil
}

// Versions lists a doc's history newest-first.
func (h *Hub) Versions(layer, targetID string) ([]Doc, error) {
	if layer == LayerCompany {
		targetID = CompanyTarget
	}
	rows, err := h.St.Read().Query(`SELECT v.version_n, v.content, v.author, v.note, v.created_at
		FROM contexts c JOIN context_versions v ON v.context_id = c.id
		WHERE c.layer = ? AND c.target_id = ? ORDER BY v.version_n DESC`, layer, targetID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Doc
	for rows.Next() {
		d := Doc{Layer: layer, TargetID: targetID}
		if err := rows.Scan(&d.VersionN, &d.Content, &d.Author, &d.Note, &d.CreatedAt); err != nil {
			return nil, err
		}
		out = append(out, d)
	}
	return out, rows.Err()
}

// Rollback creates a NEW version copying an old version's content (history is
// never deleted). Layer-agnostic; the web layer routes company rollbacks
// through approval instead of calling this directly.
func (h *Hub) Rollback(layer, targetID string, toN int, author string) (int, error) {
	old, err := h.Version(layer, targetID, toN)
	if err != nil {
		return 0, err
	}
	if old == nil {
		return 0, fmt.Errorf("version %d not found", toN)
	}
	return h.SaveContext(layer, targetID, old.Content, author, fmt.Sprintf("rollback → v%d", toN))
}

// ListDocs returns one row per doc at its head — for the editor list and the
// preview picker.
func (h *Hub) ListDocs() ([]Doc, error) {
	rows, err := h.St.Read().Query(`SELECT c.layer, c.target_id, v.version_n, v.content, v.author, v.note, v.created_at
		FROM contexts c
		JOIN context_heads hd ON hd.context_id = c.id
		JOIN context_versions v ON v.id = hd.version_id
		ORDER BY c.layer, c.target_id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Doc
	for rows.Next() {
		var d Doc
		if err := rows.Scan(&d.Layer, &d.TargetID, &d.VersionN, &d.Content, &d.Author, &d.Note, &d.CreatedAt); err != nil {
			return nil, err
		}
		out = append(out, d)
	}
	return out, rows.Err()
}
