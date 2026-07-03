package contexthub

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestBuildInjectionShape(t *testing.T) {
	b := BuildInjection("<!-- dandori-context: begin (company v1) -->\nhi\n<!-- dandori-context: end -->")
	if b == nil {
		t.Fatal("non-empty text should build injection")
	}
	var got injection
	if err := json.Unmarshal(b, &got); err != nil {
		t.Fatal(err)
	}
	if got.HookSpecificOutput.HookEventName != "SessionStart" {
		t.Errorf("hookEventName = %q", got.HookSpecificOutput.HookEventName)
	}
	if !strings.Contains(got.HookSpecificOutput.AdditionalContext, "dandori-context: begin") {
		t.Error("additionalContext missing the context")
	}
	// Exact key casing matters — Claude Code reads these literal keys.
	if !strings.Contains(string(b), `"hookSpecificOutput"`) || !strings.Contains(string(b), `"additionalContext"`) {
		t.Errorf("wrong JSON keys: %s", b)
	}
}

func TestBuildInjectionEmptyIsNil(t *testing.T) {
	if BuildInjection("") != nil {
		t.Error("empty text must yield nil (caller emits nothing → fail-open)")
	}
}

func TestProvPayloadOmitsAbsent(t *testing.T) {
	three := 3
	p := Provenance{Company: &three} // team/agent nil
	got := ProvPayload(p)
	if got != `{"company":3}` {
		t.Errorf("prov payload = %q, want only company", got)
	}
	if ProvPayload(Provenance{}) != "{}" {
		t.Errorf("empty prov = %q", ProvPayload(Provenance{}))
	}
}
