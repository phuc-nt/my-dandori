// Package jira is a minimal Jira Cloud REST v3 client: search issues for the
// unified schema, create issues for flags. Basic auth (email + API token).
package jira

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
)

type Client struct {
	BaseURL string // https://<site>.atlassian.net (or a test server)
	Email   string
	Token   string
	HTTP    *http.Client
}

// New accepts the site as either a bare name ("phucnt0") or a full host
// ("phucnt0.atlassian.net").
func New(site, email, token string) *Client {
	host := site
	if !strings.Contains(host, ".") {
		host += ".atlassian.net"
	}
	return &Client{
		BaseURL: "https://" + host,
		Email:   email,
		Token:   token,
		HTTP:    &http.Client{Timeout: 15 * time.Second},
	}
}

func (c *Client) do(method, path string, body any) ([]byte, int, error) {
	var rdr io.Reader
	if body != nil {
		b, err := json.Marshal(body)
		if err != nil {
			return nil, 0, err
		}
		rdr = bytes.NewReader(b)
	}
	req, err := http.NewRequest(method, c.BaseURL+path, rdr)
	if err != nil {
		return nil, 0, err
	}
	req.SetBasicAuth(c.Email, c.Token)
	req.Header.Set("Accept", "application/json")
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := c.HTTP.Do(req)
	if err != nil {
		return nil, 0, err
	}
	defer resp.Body.Close()
	b, err := io.ReadAll(io.LimitReader(resp.Body, 4<<20))
	return b, resp.StatusCode, err
}

// Issue is the slice of a Jira issue Dandori cares about.
type Issue struct {
	Key      string
	Summary  string
	Status   string
	Assignee string
	Labels   []string
	Updated  string
}

type searchResponse struct {
	Issues []struct {
		Key    string `json:"key"`
		Fields struct {
			Summary string `json:"summary"`
			Status  struct {
				Name string `json:"name"`
			} `json:"status"`
			Assignee *struct {
				DisplayName string `json:"displayName"`
			} `json:"assignee"`
			Labels  []string `json:"labels"`
			Updated string   `json:"updated"`
		} `json:"fields"`
	} `json:"issues"`
}

// SearchIssues fetches up to 100 most recently updated issues of a project.
// Tries the newer /search/jql endpoint first, falls back to legacy /search.
func (c *Client) SearchIssues(project string) ([]Issue, error) {
	q := url.Values{
		"jql":        {fmt.Sprintf("project = %s ORDER BY updated DESC", project)},
		"maxResults": {"100"},
		"fields":     {"summary,status,assignee,labels,updated"},
	}.Encode()
	body, code, err := c.do("GET", "/rest/api/3/search/jql?"+q, nil)
	if err != nil {
		return nil, err
	}
	if code == 404 || code == 410 || code == 400 {
		body, code, err = c.do("GET", "/rest/api/3/search?"+q, nil)
		if err != nil {
			return nil, err
		}
	}
	if code != 200 {
		return nil, fmt.Errorf("jira search: HTTP %d: %.200s", code, body)
	}
	var sr searchResponse
	if err := json.Unmarshal(body, &sr); err != nil {
		return nil, err
	}
	out := make([]Issue, 0, len(sr.Issues))
	for _, it := range sr.Issues {
		iss := Issue{Key: it.Key, Summary: it.Fields.Summary, Status: it.Fields.Status.Name,
			Labels: it.Fields.Labels, Updated: it.Fields.Updated}
		if it.Fields.Assignee != nil {
			iss.Assignee = it.Fields.Assignee.DisplayName
		}
		out = append(out, iss)
	}
	return out, nil
}

// CreateIssue opens a Task with an ADF description and returns its key.
func (c *Client) CreateIssue(project, summary, description string, labels []string) (string, error) {
	payload := map[string]any{"fields": map[string]any{
		"project":   map[string]string{"key": project},
		"issuetype": map[string]string{"name": "Task"},
		"summary":   summary,
		"labels":    labels,
		"description": map[string]any{ // Atlassian Document Format
			"type": "doc", "version": 1,
			"content": []any{map[string]any{
				"type":    "paragraph",
				"content": []any{map[string]any{"type": "text", "text": description}},
			}},
		},
	}}
	body, code, err := c.do("POST", "/rest/api/3/issue", payload)
	if err != nil {
		return "", err
	}
	if code != 201 {
		return "", fmt.Errorf("jira create: HTTP %d: %.300s", code, body)
	}
	var out struct {
		Key string `json:"key"`
	}
	if err := json.Unmarshal(body, &out); err != nil {
		return "", err
	}
	return out.Key, nil
}
