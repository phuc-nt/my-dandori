package ingest

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"os/user"
	"path/filepath"
	"time"

	"github.com/phuc-nt/dandori/internal/config"
	"github.com/phuc-nt/dandori/internal/redact"
)

// Client is the dev-machine side of central mode. Capture stays fail-open:
// every send error ends in the spool, never in a broken Claude session.
type Client struct {
	cfg  *config.Config
	http *http.Client
}

func NewClient(cfg *config.Config) *Client {
	return &Client{cfg: cfg, http: &http.Client{Timeout: 3 * time.Second}}
}

// Enabled reports whether this machine is connected to a central server.
func Enabled(cfg *config.Config) bool {
	return cfg.ServerURL != "" && cfg.IngestToken != ""
}

// Principal identifies the human on this machine (username@hostname). Sent
// only as a diagnostic hint header (X-Dandori-Principal-Hint) — the server
// derives the real principal from the bearer token server-side and never
// trusts this header for access control (H1, prevents spoofing).
func Principal() string {
	name := "unknown"
	if u, err := user.Current(); err == nil && u.Username != "" {
		name = u.Username
	}
	host, _ := os.Hostname()
	if host == "" {
		host = "unknown-host"
	}
	return name + "@" + host
}

// authorize attaches this machine's per-operator (or legacy shared) ingest
// token. X-Dandori-Principal-Hint is diagnostic only — the server derives
// the authoritative principal from the token itself, never from this
// header, so a malicious sender cannot use it to spoof another operator.
func (c *Client) authorize(req *http.Request) {
	req.Header.Set("Authorization", "Bearer "+c.cfg.IngestToken)
	req.Header.Set("X-Dandori-Principal-Hint", Principal())
	req.Header.Set("Content-Type", "application/json")
}

// AppendEvent redacts and spools one record. Delivery happens on the next
// flush — appends themselves never touch the network (hook latency).
func (c *Client) AppendEvent(rec Record) error {
	rec.Payload = redact.String(rec.Payload)
	return spoolAppend(rec)
}

// Flush drains the spool and POSTs everything in one batch. On failure the
// drained records are re-spooled — combined with server-side ULID dedup this
// makes delivery at-least-once and counting exactly-once (red-team C4).
func (c *Client) Flush() error {
	unlock, ok := flushLock()
	if !ok {
		return nil // another hook process is flushing
	}
	defer unlock()
	recs, err := spoolDrain()
	if err != nil || len(recs) == 0 {
		return err
	}
	if err := c.post(recs); err != nil {
		for _, r := range recs {
			_ = spoolAppend(r)
		}
		return err
	}
	return nil
}

// FlushThrottled flushes at most once per 10s across hook processes —
// post-tool events reach the server near-live without a POST per tool call.
func (c *Client) FlushThrottled() {
	marker := filepath.Join(filepath.Dir(spoolPath()), "last_flush")
	if fi, err := os.Stat(marker); err == nil && time.Since(fi.ModTime()) < 10*time.Second {
		return
	}
	_ = os.WriteFile(marker, nil, 0o600)
	_ = c.Flush()
}

func (c *Client) post(recs []Record) error {
	body, err := json.Marshal(Batch{Records: recs})
	if err != nil {
		return err
	}
	req, err := http.NewRequest(http.MethodPost, c.cfg.ServerURL+"/ingest/events", bytes.NewReader(body))
	if err != nil {
		return err
	}
	c.authorize(req)
	resp, err := c.http.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("ingest: server returned %s", resp.Status)
	}
	return nil
}

// flushLock serializes flushes across concurrent hook processes via an
// exclusive lock file. Stale locks (crashed process) expire after 30s.
func flushLock() (func(), bool) {
	path := filepath.Join(filepath.Dir(spoolPath()), "flush.lock")
	if fi, err := os.Stat(path); err == nil && time.Since(fi.ModTime()) > 30*time.Second {
		_ = os.Remove(path)
	}
	f, err := os.OpenFile(path, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
	if err != nil {
		return nil, false
	}
	f.Close()
	return func() { _ = os.Remove(path) }, true
}
