// Package capture ingests Claude Code activity into the store: hook events,
// transcript token usage, and a watcher for sessions that bypassed hooks.
package capture

import "encoding/json"

// HookInput is the JSON Claude Code pipes to hook commands on stdin.
// Decoding is tolerant: unknown fields are ignored, all fields optional.
type HookInput struct {
	SessionID      string          `json:"session_id"`
	TranscriptPath string          `json:"transcript_path"`
	CWD            string          `json:"cwd"`
	HookEventName  string          `json:"hook_event_name"`
	ToolName       string          `json:"tool_name"`
	ToolInput      json.RawMessage `json:"tool_input"`
	ToolResponse   json.RawMessage `json:"tool_response"`
	PermissionMode string          `json:"permission_mode"`
	Source         string          `json:"source"` // SessionStart: startup|resume|...
}

// truncate caps payloads stored in events at n bytes (tool IO can be huge).
func truncate(b []byte, n int) string {
	if len(b) <= n {
		return string(b)
	}
	return string(b[:n]) + `…(truncated)`
}

// Truncate caps a payload at the standard event size (exported for the
// central-mode client, which builds event records outside this package).
func Truncate(b []byte) string { return truncate(b, payloadCap) }

// IsErrorResponse reports whether a raw tool response looks like a failure.
func IsErrorResponse(raw []byte) bool { return isErrorResponse(raw) }
