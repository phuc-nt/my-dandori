package learn

import (
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"sort"

	"github.com/phuc-nt/dandori/internal/store"
)

// KindKit is the kind=kit knowledge unit: a manifest-as-body bundle of
// git-tracked `.claude/` files (P4, v13). Manifest-as-body keeps the v12
// 1-unit-1-review envelope intact — a kit never explodes into N child
// units/reviews; every bundled file's own content lives in the
// knowledge_kit_files table (migration 017), keyed by unit_id+path.
const KindKit = "kit"

// Kit caps (P4 spec): a review queue entry must stay small enough for a
// human to actually review — 200 files and 1MB total comfortably cover a
// real agent-pack (.claude/agents + rules + skills + commands) while
// bounding worst case.
const (
	MaxKitFiles      = 200
	MaxKitTotalBytes = 1 << 20 // 1 MiB
)

// KitManifestFile is one entry in a kit manifest — the per-file identity
// (path), the Merkle-lite pinned hash (content_hash = sha256 of that file's
// own body, NOT the manifest hash), and its size for display.
type KitManifestFile struct {
	Path        string `json:"path"`
	ContentHash string `json:"content_hash"`
	Size        int    `json:"size"`
}

// KitManifest is the canonical JSON shape stored as a kind=kit unit's body.
// Canonical = Files sorted by Path before marshal (BuildKitManifest enforces
// this) — a stable byte-for-byte manifest is required for content_hash
// (sha256 of the manifest JSON) to be deterministic regardless of scan/map
// iteration order (risk table: "Manifest hash unstable (map ordering)").
type KitManifest struct {
	Files []KitManifestFile `json:"files"`
}

// KitFileInput is one file's (path, body) pair fed into BuildKitManifest —
// the caller (kit_cmd.go's nominate scan) already validated path via
// kitpolicy and body via size/secret-scan before this point; this function
// only builds the canonical manifest + per-file hash, it does not re-validate
// path safety or content.
type KitFileInput struct {
	Path string
	Body string
}

// BuildKitManifest computes each file's content_hash+size, sorts the
// resulting entries by path (canonical — hash determinism), and returns the
// manifest alongside the total byte count (sum of all file bodies) so the
// caller can enforce MaxKitTotalBytes without a second pass.
func BuildKitManifest(files []KitFileInput) (KitManifest, int) {
	entries := make([]KitManifestFile, 0, len(files))
	total := 0
	for _, f := range files {
		sum := sha256.Sum256([]byte(f.Body))
		entries = append(entries, KitManifestFile{
			Path:        f.Path,
			ContentHash: hex.EncodeToString(sum[:]),
			Size:        len(f.Body),
		})
		total += len(f.Body)
	}
	sort.Slice(entries, func(i, j int) bool { return entries[i].Path < entries[j].Path })
	return KitManifest{Files: entries}, total
}

// MarshalCanonical serializes m to its canonical JSON body — Files must
// already be sorted by path (BuildKitManifest guarantees this); this
// function does not re-sort so a caller who hand-builds a KitManifest (e.g.
// re-parsing one for a diff) is responsible for sort order itself. Plain
// json.Marshal is stable for this shape: struct field order is fixed by the
// Go type, and slice order is exactly what the caller provided — no map
// involved anywhere in the shape, so encoding/json cannot reorder anything
// on its own.
func (m KitManifest) MarshalCanonical() (string, error) {
	b, err := json.Marshal(m)
	if err != nil {
		return "", err
	}
	return string(b), nil
}

// ContentHash returns sha256(canonical JSON body) hex — the SINGLE hash the
// audit chain records at publish time (Merkle-lite: this one hash covers
// every per-file hash inside Files, since the manifest JSON embeds them all).
func (m KitManifest) ContentHash() (string, error) {
	body, err := m.MarshalCanonical()
	if err != nil {
		return "", err
	}
	sum := sha256.Sum256([]byte(body))
	return hex.EncodeToString(sum[:]), nil
}

// ParseKitManifest parses a kind=kit unit's body back into a KitManifest —
// used by the applier (per-file H1 verify) and the review UI (manifest
// list + diff render).
func ParseKitManifest(body string) (KitManifest, error) {
	var m KitManifest
	if err := json.Unmarshal([]byte(body), &m); err != nil {
		return KitManifest{}, fmt.Errorf("parse kit manifest: %w", err)
	}
	return m, nil
}

// FileByPath returns the manifest entry for path, or (zero, false) if absent
// — the H1 per-file verify lookup (applier) and per-file diff render (review
// UI) both key off this.
func (m KitManifest) FileByPath(path string) (KitManifestFile, bool) {
	for _, f := range m.Files {
		if f.Path == path {
			return f, true
		}
	}
	return KitManifestFile{}, false
}

// KitNominateParams is the input to NominateUnitTx — a superset of
// NominateParams' kit-relevant fields plus the actual per-file bodies to
// persist into knowledge_kit_files. Body/ContentHash on the embedded params
// are computed here from files (the caller must NOT pre-fill them) so the
// manifest-body and the per-file rows can never disagree about what "the
// kit" contains.
type KitNominateParams struct {
	Name        string
	Title       string
	Files       []KitFileInput
	NominatedBy string
	Origin      string
	OriginModel string
}

// NominateUnitTx creates a kind=kit knowledge_units row (state=nominated)
// AND its knowledge_kit_files rows in ONE transaction — the atomicity
// requirement from the risk table ("Non-atomic kit write (unit without
// files)"): any per-file insert failure rolls back the unit row too, so a
// kit unit can never exist without its backing file rows. Mirrors
// NominateUnit's own validation (slug, title, dedup, supersede lineage) but
// cannot reuse NominateUnit directly — that function commits its own
// transaction internally, leaving no hook point to insert the file rows
// before commit.
func NominateUnitTx(st *store.Store, p KitNominateParams) (int64, error) {
	if !ValidSlug(p.Name) {
		return 0, fmt.Errorf("invalid name slug %q — must match ^[a-z0-9][a-z0-9-]*$", p.Name)
	}
	if p.Title == "" {
		return 0, fmt.Errorf("title required")
	}
	if len(p.Files) == 0 {
		return 0, fmt.Errorf("kit requires at least one file")
	}
	if len(p.Files) > MaxKitFiles {
		return 0, fmt.Errorf("kit has %d files, exceeds cap %d", len(p.Files), MaxKitFiles)
	}

	manifest, total := BuildKitManifest(p.Files)
	if total > MaxKitTotalBytes {
		return 0, fmt.Errorf("kit total %d bytes exceeds cap %d", total, MaxKitTotalBytes)
	}
	body, err := manifest.MarshalCanonical()
	if err != nil {
		return 0, err
	}
	contentHash, err := manifest.ContentHash()
	if err != nil {
		return 0, err
	}

	tx, err := st.DB.Begin()
	if err != nil {
		return 0, err
	}
	defer tx.Rollback()

	var nextN int
	if err := tx.QueryRow(`SELECT COALESCE(MAX(version_n),0)+1 FROM knowledge_units WHERE kind = ? AND name = ?`,
		KindKit, p.Name).Scan(&nextN); err != nil {
		return 0, err
	}
	now := store.Now()

	// Same draft-dedup guard NominateUnit applies (M1 lineage): refuse a
	// second kit draft for the same name while one is already
	// nominated/in_review.
	var draftID int64
	err = tx.QueryRow(`SELECT id FROM knowledge_units WHERE kind = ? AND name = ?
		AND state IN ('nominated','in_review')`, KindKit, p.Name).Scan(&draftID)
	if err == nil {
		return 0, fmt.Errorf("%w: kit %q is already pending review (unit #%d)", ErrDuplicateDraft, p.Name, draftID)
	} else if err != sql.ErrNoRows {
		return 0, err
	}

	var supersedes *int64
	var liveID int64
	err = tx.QueryRow(`SELECT id FROM knowledge_units WHERE kind = ? AND name = ?
		AND state IN ('published','adopted','measured')`, KindKit, p.Name).Scan(&liveID)
	if err == nil {
		supersedes = &liveID
	} else if err != sql.ErrNoRows {
		return 0, err
	}

	origin := p.Origin
	if origin == "" {
		origin = "human"
	}

	res, err := tx.Exec(`INSERT INTO knowledge_units(
			kind, name, title, state, version_n, supersedes_id,
			ref_kind, ref_id, body, content_hash, layer, layer_target, required,
			n_present, n_absent, done_present, done_absent,
			ci_present_lo, ci_present_hi, ci_absent_lo, ci_absent_hi,
			cost_present, cost_absent, provenance_run_ids, nominated_by,
			origin, origin_model,
			created_at, updated_at)
		VALUES(?, ?, ?, ?, ?, ?, NULL, NULL, ?, ?, NULL, NULL, 0,
			0, 0, 0, 0, 0, 0, 0, 0, 0, 0, '[]', ?, ?, ?, ?, ?)`,
		KindKit, p.Name, p.Title, StateNominated, nextN, supersedes,
		body, contentHash,
		p.NominatedBy, origin, nullStrIf(p.OriginModel),
		now, now)
	if err != nil {
		if isUniqueViolation(err) {
			return 0, fmt.Errorf("%w: kit %q is already pending review", ErrDuplicateDraft, p.Name)
		}
		return 0, err
	}
	unitID, _ := res.LastInsertId()

	for _, f := range manifest.Files {
		var fb string
		for _, in := range p.Files {
			if in.Path == f.Path {
				fb = in.Body
				break
			}
		}
		if _, err := tx.Exec(`INSERT INTO knowledge_kit_files(unit_id, path, body, content_hash, size)
			VALUES(?, ?, ?, ?, ?)`, unitID, f.Path, fb, f.ContentHash, f.Size); err != nil {
			return 0, fmt.Errorf("insert kit file %q: %w", f.Path, err)
		}
	}

	if err := recordTransitionTx(tx, unitID, StateDetected, StateNominated, p.NominatedBy, "kit nominated"); err != nil {
		return 0, err
	}
	if err := tx.Commit(); err != nil {
		return 0, err
	}
	return unitID, nil
}
