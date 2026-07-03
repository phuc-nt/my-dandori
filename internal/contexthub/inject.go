package contexthub

import "encoding/json"

// injection is the SessionStart hook's stdout JSON. Claude Code reads
// hookSpecificOutput.additionalContext and wraps it in a system-reminder for
// the model. Field names/casing must match exactly (mirrors the PreToolUse
// output struct in internal/cli/pre_tool.go).
type injection struct {
	HookSpecificOutput struct {
		HookEventName     string `json:"hookEventName"`
		AdditionalContext string `json:"additionalContext"`
	} `json:"hookSpecificOutput"`
}

// BuildInjection marshals the SessionStart injection JSON for the given
// effective context. Returns nil for empty text so the caller emits nothing
// (fail-open: no context, session proceeds).
func BuildInjection(text string) []byte {
	if text == "" {
		return nil
	}
	var inj injection
	inj.HookSpecificOutput.HookEventName = "SessionStart"
	inj.HookSpecificOutput.AdditionalContext = text
	b, err := json.Marshal(inj)
	if err != nil {
		return nil
	}
	return b
}

// ProvPayload renders a Provenance as the compact JSON stored on the
// context_injected event, omitting absent layers.
func ProvPayload(p Provenance) string {
	b, err := json.Marshal(p)
	if err != nil {
		return "{}"
	}
	return string(b)
}
