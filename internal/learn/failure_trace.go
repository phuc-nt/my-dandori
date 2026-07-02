package learn

import (
	"fmt"
	"strings"
)

// FailureTrace answers "why did this run fail?" without making the operator
// read 200 timeline rows: rule-based, deterministic, highlights the evidence.
type FailureTrace struct {
	Verdict      string
	HighlightIDs map[int64]bool
}

// TraceEvent is the minimal event shape the tracer needs (mirrors the web
// layer's timeline row without importing it).
type TraceEvent struct {
	ID      int64
	Kind    string
	Tool    string
	OK      *int64
	Payload string
}

// Trace inspects a failed/killed run's events in order.
func Trace(events []TraceEvent) FailureTrace {
	t := FailureTrace{HighlightIDs: map[int64]bool{}}

	// Loop detection: ≥3 consecutive tool errors on the same tool with the
	// same payload prefix.
	streak := 0
	var streakTool, streakSig string
	var streakIDs []int64
	var lastError *TraceEvent
	var lastBlock *TraceEvent
	var killed *TraceEvent

	for i := range events {
		e := &events[i]
		switch e.Kind {
		case "tool_result":
			if e.OK != nil && *e.OK == 0 {
				lastError = e
				sig := e.Tool + "|" + prefix(e.Payload, 60)
				if e.Tool == streakTool && sig == streakSig {
					streak++
				} else {
					streak, streakTool, streakSig, streakIDs = 1, e.Tool, sig, nil
				}
				streakIDs = append(streakIDs, e.ID)
			} else {
				streak, streakTool, streakSig, streakIDs = 0, "", "", nil
			}
		case "guardrail_block":
			lastBlock = e
		case "kill":
			killed = e
		}
		if streak >= 3 {
			for _, id := range streakIDs {
				t.HighlightIDs[id] = true
			}
			t.Verdict = fmt.Sprintf("Lặp lỗi: %d lần %s thất bại liên tiếp với cùng lỗi — dấu hiệu vòng lặp.", streak, streakTool)
		}
	}

	switch {
	case killed != nil:
		t.HighlightIDs[killed.ID] = true
		if t.Verdict == "" {
			t.Verdict = "Run bị kill bởi operator: " + prefix(killed.Payload, 120)
		} else {
			t.Verdict += " Sau đó run bị kill: " + prefix(killed.Payload, 80)
		}
	case t.Verdict != "":
		// loop verdict stands
	case lastBlock != nil && lastError == nil:
		t.HighlightIDs[lastBlock.ID] = true
		t.Verdict = "Guardrail chặn hành động cuối cùng: " + prefix(lastBlock.Payload, 120)
	case lastError != nil:
		t.HighlightIDs[lastError.ID] = true
		t.Verdict = fmt.Sprintf("Lỗi tool cuối cùng (%s): %s", lastError.Tool, prefix(lastError.Payload, 120))
	default:
		t.Verdict = "Không có lỗi tool nào được ghi — run kết thúc fail ở tầng phiên (xem transcript)."
	}
	return t
}

// prefix truncates rune-safely (payloads carry Vietnamese text).
func prefix(s string, n int) string {
	s = strings.TrimSpace(s)
	runes := []rune(s)
	if len(runes) > n {
		return string(runes[:n]) + "…"
	}
	return s
}
