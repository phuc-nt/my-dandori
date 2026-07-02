package learn

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"time"

	"github.com/phuc-nt/dandori/internal/store"
)

// Behavior metrics: how the AGENT worked and how the HUMAN drove it, derived
// entirely from server-stored numerics (runs + events rows). No transcript
// or file read happens here — central mode has no transcript to read.
//
// Every value is a PROXY and says so in its Formula. Operator-level rollups
// are PRIVATE coaching signals (see behavior_aggregate.go) — steering an
// agent is often the RIGHT move, so these numbers must never become a
// public human leaderboard.

// Prompt-length bands (research: 50–200 words is the sweet spot; shorter
// under-specifies, much longer usually pastes context the agent can fetch).
const (
	promptShortMax = 20
	promptGoodMax  = 250
)

// abandonedAfter marks a run abandoned when it never ended and went silent.
const abandonedAfter = 6 * time.Hour

type BehaviorMetrics struct {
	RunID       string
	PromptWords int
	PromptSpec  int    // bit flags from capture.PromptProxy
	PromptBand  string // short | good | long | unknown
	Steering    int    // mid-run user messages (user_msg event)
	ToolErrors  int    // tool_result ok=0
	RetryLoops  int    // streaks of ≥3 consecutive same-tool errors
	PermAsks    int    // permission_ask events
	Abandoned   bool
	TaskSize    string // S | M | L by input tokens
	Formula     string
	EventIDs    []int64
}

// ComputeBehavior derives one run's behavior metrics from DB rows only.
func ComputeBehavior(st *store.Store, runID string) (*BehaviorMetrics, error) {
	b := &BehaviorMetrics{RunID: runID, PromptBand: "unknown"}
	var status string
	var endedAt sql.NullString
	var startedAt string
	var inputTokens int64
	err := st.Read().QueryRow(`SELECT status, started_at, ended_at, input_tokens
		FROM runs WHERE id = ?`, runID).Scan(&status, &startedAt, &endedAt, &inputTokens)
	if err != nil {
		return nil, err
	}
	switch {
	case inputTokens > 2_000_000:
		b.TaskSize = "L"
	case inputTokens > 300_000:
		b.TaskSize = "M"
	default:
		b.TaskSize = "S"
	}
	if status == "running" && !endedAt.Valid {
		if t, err := time.Parse(time.RFC3339, startedAt); err == nil && time.Since(t) > abandonedAfter {
			b.Abandoned = true
		}
	}

	rows, err := st.Read().Query(`SELECT id, kind, COALESCE(tool_name,''), ok, COALESCE(payload,'')
		FROM events WHERE run_id = ? ORDER BY id`, runID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	streak, streakTool := 0, ""
	for rows.Next() {
		var id int64
		var kind, tool, payload string
		var ok sql.NullInt64
		if err := rows.Scan(&id, &kind, &tool, &ok, &payload); err != nil {
			return nil, err
		}
		switch kind {
		case "user_msg":
			fmt.Sscanf(payload, "%d", &b.Steering)
		case "prompt_proxy":
			var p struct{ W, Spec int }
			if json.Unmarshal([]byte(payload), &p) == nil {
				b.PromptWords, b.PromptSpec = p.W, p.Spec
			}
		case "permission_ask":
			b.PermAsks++
			b.EventIDs = append(b.EventIDs, id)
		case "tool_result":
			if ok.Valid && ok.Int64 == 0 {
				b.ToolErrors++
				b.EventIDs = append(b.EventIDs, id)
				if tool == streakTool {
					streak++
				} else {
					streak, streakTool = 1, tool
				}
				if streak == 3 {
					b.RetryLoops++
				}
			} else {
				streak, streakTool = 0, ""
			}
		}
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	switch {
	case b.PromptWords == 0:
		b.PromptBand = "unknown"
	case b.PromptWords < promptShortMax:
		b.PromptBand = "short"
	case b.PromptWords <= promptGoodMax:
		b.PromptBand = "good"
	default:
		b.PromptBand = "long"
	}
	b.Formula = fmt.Sprintf(
		"proxy: prompt %dw/%s spec=%d · steering %d mid-run msgs · %d tool errors (%d retry loops ≥3) · %d permission asks · size %s",
		b.PromptWords, b.PromptBand, b.PromptSpec, b.Steering, b.ToolErrors, b.RetryLoops, b.PermAsks, b.TaskSize)
	return b, nil
}
