// Package confluence is a minimal Confluence Cloud v2 client sharing the
// Atlassian token with Jira: read pages (operator context), create pages
// (fleet reports). Every write goes through the integrations guard first.
package confluence

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"html"
	"io"
	"net/http"
	"regexp"
	"strings"
	"time"
)

// ErrPageExists is returned by CreatePage when a page with the same title
// already exists in the space (Confluence 400). Titles are unique per space,
// so this means the intended page is already there — callers that publish
// idempotently (e.g. the daily fleet report / flywheel card) should treat it
// as success rather than an error, especially when the local dedup record was
// lost (fresh DB, reset) but the remote page persists.
var ErrPageExists = errors.New("confluence: a page with this title already exists in the space")

type Client struct {
	BaseURL string // https://<site> (client appends /wiki/api/v2)
	Email   string
	Token   string
	HTTP    *http.Client
}

// New accepts a bare site name or full host, like jira.New.
func New(site, email, token string) *Client {
	host := site
	if !strings.Contains(host, ".") {
		host += ".atlassian.net"
	}
	return &Client{
		BaseURL: "https://" + host,
		Email:   email, Token: token,
		HTTP: &http.Client{Timeout: 15 * time.Second},
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

var tagRe = regexp.MustCompile(`<[^>]+>`)

// GetPageText fetches a page body (storage format) stripped to plain text.
func (c *Client) GetPageText(pageID string) (title, text string, err error) {
	b, code, err := c.do("GET", "/wiki/api/v2/pages/"+pageID+"?body-format=storage", nil)
	if err != nil {
		return "", "", err
	}
	if code != 200 {
		return "", "", fmt.Errorf("confluence get page: HTTP %d: %.200s", code, b)
	}
	var out struct {
		Title string `json:"title"`
		Body  struct {
			Storage struct {
				Value string `json:"value"`
			} `json:"storage"`
		} `json:"body"`
	}
	if err := json.Unmarshal(b, &out); err != nil {
		return "", "", err
	}
	text = html.UnescapeString(tagRe.ReplaceAllString(out.Body.Storage.Value, " "))
	return out.Title, strings.Join(strings.Fields(text), " "), nil
}

// CreatePage creates a page in a space (storage-format HTML body) and
// returns the new page id.
func (c *Client) CreatePage(spaceID, parentID, title, storageHTML string) (string, error) {
	payload := map[string]any{
		"spaceId": spaceID,
		"status":  "current",
		"title":   title,
		"body":    map[string]any{"representation": "storage", "value": storageHTML},
	}
	if parentID != "" {
		payload["parentId"] = parentID
	}
	b, code, err := c.do("POST", "/wiki/api/v2/pages", payload)
	if err != nil {
		return "", err
	}
	if code == 400 && bytes.Contains(b, []byte("same TITLE")) {
		return "", ErrPageExists
	}
	if code != 200 && code != 201 {
		return "", fmt.Errorf("confluence create page: HTTP %d: %.300s", code, b)
	}
	var out struct {
		ID string `json:"id"`
	}
	if err := json.Unmarshal(b, &out); err != nil {
		return "", err
	}
	return out.ID, nil
}
