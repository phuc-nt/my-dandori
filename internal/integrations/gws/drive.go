package gws

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
)

// maxExportBytes caps DriveExport output at 10MB — Drive's own export
// limit — to avoid holding an oversized doc in memory.
const maxExportBytes = 10 << 20

// ErrExportTooLarge is returned when exported content exceeds the 10MB
// Drive export limit.
var ErrExportTooLarge = errors.New("gws: drive export exceeds 10MB limit")

// DriveFile is one result row from DriveList.
type DriveFile struct {
	ID           string `json:"id"`
	Name         string `json:"name"`
	MimeType     string `json:"mimeType"`
	ModifiedTime string `json:"modifiedTime"`
}

type driveListParams struct {
	Q      string `json:"q"`
	Fields string `json:"fields"`
}

type driveListResponse struct {
	Files []DriveFile `json:"files"`
}

// DriveList searches Drive files by query. Read-only — no Guard needed.
// The query is always carried as a JSON struct field (never string-concat
// into the JSON literal), so a hostile `"` or `\` in query text cannot
// break out of the emitted --params payload.
func (r *Runner) DriveList(ctx context.Context, query string) ([]DriveFile, error) {
	params, err := json.Marshal(driveListParams{
		Q:      query,
		Fields: "files(id,name,mimeType,modifiedTime)",
	})
	if err != nil {
		return nil, err
	}
	out, err := r.run(ctx, "drive", "files", "list", "--params", string(params))
	if err != nil {
		return nil, fmt.Errorf("gws drive list: %w", err)
	}
	var resp driveListResponse
	if err := json.Unmarshal(out, &resp); err != nil {
		return nil, fmt.Errorf("gws drive list: parse response: %w", err)
	}
	return resp.Files, nil
}

type driveExportParams struct {
	FileID   string `json:"fileId"`
	MimeType string `json:"mimeType"`
}

// DriveExport exports a Drive file as the given MIME type and returns the
// raw bytes (stdout is the file content, NOT JSON). Read-only — no Guard
// needed. Rejects exports over 10MB per Drive's export limit.
func (r *Runner) DriveExport(ctx context.Context, fileID, mimeType string) ([]byte, error) {
	params, err := json.Marshal(driveExportParams{FileID: fileID, MimeType: mimeType})
	if err != nil {
		return nil, err
	}
	out, err := r.exec(ctx, "drive", "files", "export", "--params", string(params))
	if err != nil {
		return nil, fmt.Errorf("gws drive export %s: %w", fileID, err)
	}
	if len(out) > maxExportBytes {
		return nil, ErrExportTooLarge
	}
	return out, nil
}
