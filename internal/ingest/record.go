// Package ingest implements central mode: dev machines derive run data
// locally (the transcript never leaves the machine) and POST redacted
// numeric records to a token-authed listener on the central server.
package ingest

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"time"
)

// Record is one spool line and one element of a POST /ingest/events batch.
// Every record is self-describing (session + attribution) so the server can
// create the run lazily regardless of arrival order.
type Record struct {
	Type      string `json:"type"` // event | finalize
	SessionID string `json:"session_id"`
	AgentName string `json:"agent_name"`
	Project   string `json:"project"`
	CWD       string `json:"cwd"`

	// Type == event
	ULID     string `json:"ulid,omitempty"`
	Kind     string `json:"kind,omitempty"`
	Tool     string `json:"tool,omitempty"`
	OK       *int64 `json:"ok,omitempty"`
	Payload  string `json:"payload,omitempty"` // redacted client-side before spool/POST
	ClientTS string `json:"client_ts,omitempty"`

	// Type == finalize
	Finalize *RunFinalize `json:"finalize,omitempty"`
}

// RunFinalize carries ONLY derived numerics — no prompt or transcript text
// crosses the wire (red-team H4). SET semantics server-side: replays are
// idempotent.
type RunFinalize struct {
	Model        string  `json:"model"`
	InputTokens  int64   `json:"input_tokens"`
	OutputTokens int64   `json:"output_tokens"`
	CacheRead    int64   `json:"cache_read"`
	CacheWrite   int64   `json:"cache_write"`
	CostUSD      float64 `json:"cost_usd"`
	TaskKey      string  `json:"task_key"`
	MidRunMsgs   int     `json:"mid_run_msgs"`
	PromptWords  int     `json:"prompt_words"`
	PromptSpec   int     `json:"prompt_spec"`
	LinesAdded   int     `json:"lines_added"`
	LinesDeleted int     `json:"lines_deleted"`
	HeadBefore   string  `json:"head_before"`
	HeadAfter    string  `json:"head_after"`
	Status       string  `json:"status"`
	EndedAt      string  `json:"ended_at"`
}

// Batch is the POST /ingest/events body.
type Batch struct {
	Records []Record `json:"records"`
}

// NewULID returns a sortable unique event id: ms timestamp + 10 random bytes.
// Uniqueness is what matters (idempotency key); lexical sort order is a bonus.
func NewULID() string {
	b := make([]byte, 10)
	_, _ = rand.Read(b)
	return fmt.Sprintf("%013x-%s", time.Now().UnixMilli(), hex.EncodeToString(b))
}
