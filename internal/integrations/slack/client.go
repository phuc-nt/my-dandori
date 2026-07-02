// Package slack talks to the Slack Web API with a browser session token
// (xoxc bearer + xoxd cookie) — no bot app needed. Browser tokens are broad:
// only whitelisted channels are ever written to, and every write passes the
// integrations guard first.
package slack

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
)

type Client struct {
	BaseURL string // https://slack.com/api (or test server)
	Xoxc    string
	Xoxd    string // URL-encoded cookie value, sent verbatim
	HTTP    *http.Client
}

func New(xoxc, xoxd string) *Client {
	return &Client{
		BaseURL: "https://slack.com/api",
		Xoxc:    xoxc, Xoxd: xoxd,
		HTTP: &http.Client{Timeout: 15 * time.Second},
	}
}

// call POSTs a form-encoded Web API method and decodes the JSON envelope.
func (c *Client) call(method string, form url.Values, out any) error {
	req, err := http.NewRequest("POST", c.BaseURL+"/"+method, strings.NewReader(form.Encode()))
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "Bearer "+c.Xoxc)
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("Cookie", "d="+c.Xoxd)
	resp, err := c.HTTP.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return err
	}
	var envelope struct {
		OK    bool   `json:"ok"`
		Error string `json:"error"`
	}
	if err := json.Unmarshal(body, &envelope); err != nil {
		return fmt.Errorf("slack %s: bad response: %.200s", method, body)
	}
	if !envelope.OK {
		return fmt.Errorf("slack %s: %s", method, envelope.Error)
	}
	if out != nil {
		return json.Unmarshal(body, out)
	}
	return nil
}

// AuthTest verifies the token (read-only).
func (c *Client) AuthTest() error {
	return c.call("auth.test", url.Values{}, nil)
}

// PostMessage sends text to a channel and returns the message timestamp.
func (c *Client) PostMessage(channel, text string) (string, error) {
	var out struct {
		TS string `json:"ts"`
	}
	err := c.call("chat.postMessage", url.Values{"channel": {channel}, "text": {text}}, &out)
	return out.TS, err
}

// Reaction is one emoji reaction with the users who added it.
type Reaction struct {
	Name  string   `json:"name"`
	Users []string `json:"users"`
}

// GetReactions lists reactions on a message.
func (c *Client) GetReactions(channel, ts string) ([]Reaction, error) {
	var out struct {
		Message struct {
			Reactions []Reaction `json:"reactions"`
		} `json:"message"`
	}
	err := c.call("reactions.get", url.Values{"channel": {channel}, "timestamp": {ts}}, &out)
	return out.Message.Reactions, err
}

// UserName resolves a user id to a display name (best effort).
func (c *Client) UserName(userID string) string {
	var out struct {
		User struct {
			Name    string `json:"name"`
			Profile struct {
				DisplayName string `json:"display_name"`
			} `json:"profile"`
		} `json:"user"`
	}
	if err := c.call("users.info", url.Values{"user": {userID}}, &out); err != nil {
		return userID
	}
	if out.User.Profile.DisplayName != "" {
		return out.User.Profile.DisplayName
	}
	if out.User.Name != "" {
		return out.User.Name
	}
	return userID
}
