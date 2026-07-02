package capture

import (
	"bufio"
	"encoding/json"
	"os"
)

// Usage is the aggregate token usage of one transcript, deduplicated by
// assistant message id (one message can span several JSONL lines).
type Usage struct {
	Model      string
	Input      int64
	Output     int64
	CacheRead  int64
	CacheWrite int64
	FirstUser  string // first user prompt text (task-key attribution)
	// MidRunMsgs counts user text messages that arrive AFTER the assistant
	// has started working — mid-run steering, the autonomy signal. The
	// opening prompt(s) before the first assistant turn are not counted.
	MidRunMsgs int
	CWD        string // cwd recorded in transcript lines, if any
}

type transcriptLine struct {
	Type        string `json:"type"`
	CWD         string `json:"cwd"`
	IsSidechain bool   `json:"isSidechain"`
	IsMeta      bool   `json:"isMeta"`
	Message     struct {
		ID      string          `json:"id"`
		Role    string          `json:"role"`
		Model   string          `json:"model"`
		Content json.RawMessage `json:"content"`
		Usage   *struct {
			Input      int64 `json:"input_tokens"`
			Output     int64 `json:"output_tokens"`
			CacheRead  int64 `json:"cache_read_input_tokens"`
			CacheWrite int64 `json:"cache_creation_input_tokens"`
		} `json:"usage"`
	} `json:"message"`
}

// ParseTranscript reads a full session JSONL and returns aggregate usage.
// Full reparse + SET semantics (instead of incremental offsets) makes
// reconciliation idempotent — double counting is structurally impossible.
func ParseTranscript(path string) (Usage, error) {
	var u Usage
	f, err := os.Open(path)
	if err != nil {
		return u, err
	}
	defer f.Close()

	seen := map[string]bool{}
	assistantStarted := false
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 1024*1024), 16*1024*1024)
	for sc.Scan() {
		var line transcriptLine
		if json.Unmarshal(sc.Bytes(), &line) != nil {
			continue
		}
		if u.CWD == "" && line.CWD != "" {
			u.CWD = line.CWD
		}
		switch line.Type {
		case "assistant":
			assistantStarted = true
			m := line.Message
			if m.Model != "" {
				u.Model = m.Model
			}
			if m.Usage == nil || m.ID == "" || seen[m.ID] {
				continue
			}
			seen[m.ID] = true
			u.Input += m.Usage.Input
			u.Output += m.Usage.Output
			u.CacheRead += m.Usage.CacheRead
			u.CacheWrite += m.Usage.CacheWrite
		case "user":
			// Sidechain (subagent) and meta lines are not human steering.
			if line.IsSidechain || line.IsMeta {
				continue
			}
			text := userText(line.Message.Content)
			if text == "" {
				continue
			}
			if u.FirstUser == "" {
				u.FirstUser = text
			}
			if assistantStarted {
				u.MidRunMsgs++
			}
		}
	}
	return u, sc.Err()
}

// userText extracts plain text from a user message content (string or blocks).
// Tool results also arrive as user turns; those return "" and are not counted.
func userText(raw json.RawMessage) string {
	var s string
	if json.Unmarshal(raw, &s) == nil {
		return s
	}
	var blocks []struct {
		Type string `json:"type"`
		Text string `json:"text"`
	}
	if json.Unmarshal(raw, &blocks) == nil {
		for _, b := range blocks {
			if b.Type == "text" && b.Text != "" {
				return b.Text
			}
		}
	}
	return ""
}
