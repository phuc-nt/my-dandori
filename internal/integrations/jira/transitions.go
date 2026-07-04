package jira

import (
	"encoding/json"
	"fmt"
	"time"
)

// Transition is one workflow-specific state change available on an issue.
// IDs are per-workflow — always fetch via Transitions before calling
// Transition; never hardcode an id.
type Transition struct {
	ID        string `json:"id"`
	Name      string `json:"name"`
	HasScreen bool   `json:"hasScreen"`
	To        struct {
		Name string `json:"name"`
	} `json:"to"`
}

type transitionsResponse struct {
	Transitions []Transition `json:"transitions"`
}

// Transitions lists the workflow transitions currently available on an
// issue. Read-only — no Guard needed.
func (c *Client) Transitions(key string) ([]Transition, error) {
	body, code, err := c.do("GET", "/rest/api/3/issue/"+key+"/transitions", nil)
	if err != nil {
		return nil, err
	}
	if code != 200 {
		return nil, fmt.Errorf("jira transitions: HTTP %d: %.200s", code, body)
	}
	var tr transitionsResponse
	if err := json.Unmarshal(body, &tr); err != nil {
		return nil, err
	}
	return tr.Transitions, nil
}

// Transition performs a workflow transition by id (from Transitions).
// Accepts 200 or 204 as success. Another agent transitioning the same issue
// concurrently can produce a 409 — retried once after a short backoff.
func (c *Client) Transition(key, transitionID string) error {
	payload := map[string]any{"transition": map[string]string{"id": transitionID}}
	body, code, err := c.do("POST", "/rest/api/3/issue/"+key+"/transitions", payload)
	if err != nil {
		return err
	}
	if code == 409 {
		time.Sleep(1 * time.Second)
		body, code, err = c.do("POST", "/rest/api/3/issue/"+key+"/transitions", payload)
		if err != nil {
			return err
		}
	}
	if code != 200 && code != 204 {
		return fmt.Errorf("jira transition %s->%s: HTTP %d: %.200s", key, transitionID, code, body)
	}
	return nil
}
