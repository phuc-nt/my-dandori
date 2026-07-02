package learn

import (
	"strings"
	"testing"
)

func okv(v int64) *int64 { return &v }

func TestTraceLoopDetection(t *testing.T) {
	var events []TraceEvent
	for i := int64(1); i <= 5; i++ {
		events = append(events,
			TraceEvent{ID: i * 2, Kind: "tool_use", Tool: "Bash", Payload: `{"command":"npm test"}`},
			TraceEvent{ID: i*2 + 1, Kind: "tool_result", Tool: "Bash", OK: okv(0), Payload: "Error: 3 tests failed"},
		)
	}
	events = append(events, TraceEvent{ID: 99, Kind: "kill", Payload: "runaway loop"})

	tr := Trace(events)
	if !strings.Contains(tr.Verdict, "Lặp lỗi") || !strings.Contains(tr.Verdict, "kill") {
		t.Errorf("verdict: %q", tr.Verdict)
	}
	if !tr.HighlightIDs[99] || !tr.HighlightIDs[3] {
		t.Errorf("highlights: %v", tr.HighlightIDs)
	}
}

func TestTraceLastErrorAndBlock(t *testing.T) {
	tr := Trace([]TraceEvent{
		{ID: 1, Kind: "tool_result", Tool: "Bash", OK: okv(1), Payload: "ok"},
		{ID: 2, Kind: "tool_result", Tool: "Edit", OK: okv(0), Payload: "permission denied"},
	})
	if !strings.Contains(tr.Verdict, "Edit") || !tr.HighlightIDs[2] {
		t.Errorf("last-error trace: %q %v", tr.Verdict, tr.HighlightIDs)
	}

	tr2 := Trace([]TraceEvent{
		{ID: 5, Kind: "guardrail_block", Tool: "Bash", Payload: "tool=Bash verdict=deny reason=[dandori G1]"},
	})
	if !strings.Contains(tr2.Verdict, "Guardrail") || !tr2.HighlightIDs[5] {
		t.Errorf("block trace: %q", tr2.Verdict)
	}

	tr3 := Trace(nil)
	if tr3.Verdict == "" {
		t.Error("empty events still need a verdict")
	}
}
