package learn

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"time"

	"github.com/phuc-nt/dandori/internal/store"
)

// AIReviewer turns metric provenance into the "vài câu nhận xét tiếng
// người" the vision promises for L1. Input is ONLY computed metrics and
// formulas — never raw code or transcripts. Output is cached per ISO week
// and always labeled AI-generated.
type AIReviewer struct {
	BaseURL string // https://openrouter.ai/api (or test server)
	APIKey  string
	Model   string
	St      *store.Store
	HTTP    *http.Client
}

func NewAIReviewer(st *store.Store, apiKey, model string) *AIReviewer {
	if model == "" {
		model = "anthropic/claude-haiku-4.5"
	}
	return &AIReviewer{
		BaseURL: "https://openrouter.ai/api",
		APIKey:  apiKey, Model: model, St: st,
		HTTP: &http.Client{Timeout: 20 * time.Second},
	}
}

// Review returns the cached or freshly generated performance blurb for an
// agent. Missing API key or upstream errors yield "" — the UI hides the
// section rather than failing the page.
func (a *AIReviewer) Review(agentID string, windowDays int) string {
	if a.APIKey == "" {
		return ""
	}
	year, week := time.Now().UTC().ISOWeek()
	cacheKey := fmt.Sprintf("aireview:%s:%d-W%02d", agentID, year, week)
	if cached := a.St.Setting(cacheKey); cached != "" {
		return cached
	}
	m, err := Compute(a.St, agentID, windowDays)
	if err != nil || m.Runs == 0 {
		return ""
	}
	roi, err := ComputeROI(a.St, agentID, windowDays, m.Acceptance.Value)
	if err != nil {
		return ""
	}
	text, err := a.generate(m, roi)
	if err != nil || text == "" {
		return ""
	}
	_ = a.St.SetSetting(cacheKey, text)
	return text
}

func (a *AIReviewer) generate(m *AgentMetrics, roi *ROI) (string, error) {
	prompt := fmt.Sprintf(`You are writing a 2-4 sentence performance review of an AI coding agent for an engineering leader. Be concrete, use the numbers, mention one strength and one concern, no fluff, no headers.

Agent: %s (window %d days, %d runs, $%.2f spent, %.0f%% of spend useful)
- acceptance %.0f (%s)
- success %.0f (%s)
- autonomy %.0f (%s)
- reliability %.0f (%s)`,
		m.AgentName, m.WindowDays, m.Runs, m.CostUSD, roi.UsefulPct,
		m.Acceptance.Value, m.Acceptance.Formula,
		m.Success.Value, m.Success.Formula,
		m.Autonomy.Value, m.Autonomy.Formula,
		m.Reliability.Value, m.Reliability.Formula)

	body, _ := json.Marshal(map[string]any{
		"model":      a.Model,
		"max_tokens": 250,
		"messages":   []map[string]string{{"role": "user", "content": prompt}},
	})
	req, err := http.NewRequest("POST", a.BaseURL+"/v1/chat/completions", bytes.NewReader(body))
	if err != nil {
		return "", err
	}
	req.Header.Set("Authorization", "Bearer "+a.APIKey)
	req.Header.Set("Content-Type", "application/json")
	resp, err := a.HTTP.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	raw, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return "", err
	}
	if resp.StatusCode != 200 {
		return "", fmt.Errorf("openrouter: HTTP %d: %.200s", resp.StatusCode, raw)
	}
	var out struct {
		Choices []struct {
			Message struct {
				Content string `json:"content"`
			} `json:"message"`
		} `json:"choices"`
	}
	if err := json.Unmarshal(raw, &out); err != nil {
		return "", err
	}
	if len(out.Choices) == 0 {
		return "", fmt.Errorf("openrouter: empty choices")
	}
	return out.Choices[0].Message.Content, nil
}
