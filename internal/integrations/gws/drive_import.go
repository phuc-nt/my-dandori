package gws

import (
	"context"
	"fmt"
	"strings"
)

// maxImportRunes caps the reviewable/import-able body at 8k runes — headroom
// under EffectiveContext's ~10k rune merge budget (contexthub/merge.go). This
// is a size gate on top of DriveExport's own 10MB byte cap.
const maxImportRunes = 8000

// docsMimeType is the only mime type Search returns — Sheets/Slides export
// as text/plain poorly and are out of scope (research §6).
const docsMimeType = "application/vnd.google-apps.document"

// ReviewResult is what the human sees before deciding whether to import a
// Drive doc as Context Hub content. FullText is populated ONLY when neither
// gate tripped — a doc that is too big or secret-bearing never has its body
// rendered to the browser.
type ReviewResult struct {
	FullText   string
	Runes      int
	TooBig     bool
	HasSecret  bool
	SecretHint string // first offending substring, for the block message only
	DocID      string
	DocName    string
	ModifiedAt string
}

// SecretScanner matches contexthub.SecretFragment's signature. Injected so
// this package does not import contexthub (avoids a dependency cycle risk
// and keeps gws integration-only) while still reusing the single scanner
// implementation — the caller (web handler) passes contexthub.SecretFragment.
type SecretScanner func(content string) string

// DriveImporter is UC6's search-then-review step: find Drive docs and
// export a candidate's FULL text under size/secret gates. It never writes
// anything — Search/Review are pure reads. The actual import request
// (observer.RequestAction) is issued by the web handler, not here: this
// package sits below internal/observer in the import graph (observer
// already imports gws for the calendar-event apply case), so a
// RequestAction call cannot live in this package without an import cycle.
// The approval-gate discipline (C1 — never SaveContext directly) is
// enforced at the caller.
type DriveImporter struct {
	GWS     *Runner
	Scanner SecretScanner
}

// Search finds Google Docs (folders and other mime types excluded) whose
// name contains q. The query is always carried as a JSON struct field by
// DriveList (M1) — q is never string-concatenated into a query literal here
// beyond the fixed `name contains '...'` wrapper, and that wrapper itself is
// JSON-marshaled whole by DriveList, so a hostile `"`/`\` in q cannot break
// out of the emitted --params payload.
func (d *DriveImporter) Search(ctx context.Context, q string) ([]DriveFile, error) {
	// Escape ' and \ for the Drive query DSL: inside a '...' literal a bare
	// quote would terminate the string and a backslash is the escape char.
	// (JSON break-out is already prevented by DriveList marshaling the whole
	// query — this is about a valid DSL, not injection.)
	esc := strings.NewReplacer(`\`, `\\`, `'`, `\'`).Replace(q)
	query := fmt.Sprintf("name contains '%s' and mimeType = '%s' and trashed = false", esc, docsMimeType)
	files, err := d.GWS.DriveList(ctx, query)
	if err != nil {
		return nil, err
	}
	out := make([]DriveFile, 0, len(files))
	for _, f := range files {
		if f.MimeType == docsMimeType {
			out = append(out, f)
		}
	}
	return out, nil
}

// Review exports a doc's full text and applies the size/secret gates (C1):
// an export over Drive's 10MB limit or a body over 8k runes is too big for
// the merge budget, and any secret-shaped substring blocks the body from
// ever reaching the browser. FullText is set only when every gate passes —
// callers must check TooBig/HasSecret before using it. Both size gates
// surface as a graceful TooBig block, not an error, so a large doc reads to
// the human as "too big to import" rather than a server error.
func (d *DriveImporter) Review(ctx context.Context, file DriveFile) (ReviewResult, error) {
	raw, err := d.GWS.DriveExport(ctx, file.ID, "text/plain")
	if err == ErrExportTooLarge {
		return ReviewResult{TooBig: true, DocID: file.ID, DocName: file.Name, ModifiedAt: file.ModifiedTime}, nil
	}
	if err != nil {
		return ReviewResult{}, err
	}
	text := stripExportBanner(string(raw))
	res := ReviewResult{
		Runes: len([]rune(text)), DocID: file.ID, DocName: file.Name, ModifiedAt: file.ModifiedTime,
	}
	if res.Runes > maxImportRunes {
		res.TooBig = true
		return res, nil
	}
	if d.Scanner != nil {
		if hint := d.Scanner(text); hint != "" {
			res.HasSecret = true
			res.SecretHint = hint
			return res, nil
		}
	}
	res.FullText = text
	return res, nil
}

// exportBannerPrefix is the keyring preamble the real gws CLI prints before
// ANY command's stdout, including raw (non-JSON) export bytes (documented
// in docs/integration-setup.md as needing to be stripped). DriveExport
// (drive.go) uses the raw exec path — Runner.run's stripBanner only knows
// how to slice at the first '{'/'[' for JSON commands, which does nothing
// for plain-text export bodies — so a rune count or secret scan done on the
// unstripped bytes would be off by this preamble. Stripped here, scoped to
// the import/review path, rather than in the shared exec plumbing.
const exportBannerPrefix = "Using keyring backend: keyring\n"

func stripExportBanner(s string) string {
	return strings.TrimPrefix(s, exportBannerPrefix)
}
