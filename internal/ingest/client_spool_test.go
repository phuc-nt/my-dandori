package ingest

import (
	"net/http/httptest"
	"os"
	"strings"
	"testing"

	"github.com/phuc-nt/dandori/internal/config"
)

// isolateHome points spool/cache/session paths at a temp dir.
func isolateHome(t *testing.T) {
	t.Helper()
	t.Setenv("HOME", t.TempDir())
}

// Server down → records spool (redacted); server back → one flush delivers,
// spool empties. The capture contract: nothing is lost, nothing raw on disk.
func TestSpoolRedactsAndRelays(t *testing.T) {
	isolateHome(t)
	srv, st := testServer(t)
	ts := httptest.NewServer(srv.Handler())
	defer ts.Close()

	// Not connected yet — client points at a dead port.
	cfg := &config.Config{ServerURL: "http://127.0.0.1:1", IngestToken: "secret-token", AllowLegacyIngestToken: true}
	c := NewClient(cfg)
	rec := Record{Type: "event", SessionID: "s9", AgentName: "a", ULID: NewULID(),
		Kind: "tool_use", Tool: "Bash",
		Payload: `export OPENAI_KEY; curl -H "Authorization: Bearer sk-topsecret99999"`}
	if err := c.AppendEvent(rec); err != nil {
		t.Fatal(err)
	}
	if err := c.Flush(); err == nil {
		t.Fatal("flush against dead server must error")
	}
	raw, err := os.ReadFile(spoolPath())
	if err != nil {
		t.Fatal("record must survive failed flush in the spool:", err)
	}
	if strings.Contains(string(raw), "sk-topsecret") {
		t.Error("spool holds a raw secret")
	}

	// Server comes back.
	cfg.ServerURL = ts.URL
	if err := c.Flush(); err != nil {
		t.Fatalf("relay: %v", err)
	}
	var n int
	st.DB.QueryRow(`SELECT count(*) FROM events WHERE run_id='s9'`).Scan(&n)
	if n != 1 {
		t.Errorf("relayed events: %d, want 1", n)
	}
	if b, _ := os.ReadFile(spoolPath()); len(b) != 0 {
		t.Errorf("spool not empty after relay: %q", b)
	}
	// A second flush is a no-op (nothing left to send → no double count).
	if err := c.Flush(); err != nil {
		t.Fatal(err)
	}
	st.DB.QueryRow(`SELECT count(*) FROM events WHERE run_id='s9'`).Scan(&n)
	if n != 1 {
		t.Errorf("after re-flush: %d events", n)
	}
}

func TestSpoolRotationKeepsNewestHalf(t *testing.T) {
	isolateHome(t)
	big := strings.Repeat("x", 4000)
	for i := 0; i < 3000; i++ {
		if err := spoolAppend(Record{Type: "event", SessionID: "s", ULID: NewULID(), Kind: "k", Payload: big}); err != nil {
			t.Fatal(err)
		}
	}
	fi, err := os.Stat(spoolPath())
	if err != nil {
		t.Fatal(err)
	}
	if fi.Size() > spoolMaxBytes+8192 {
		t.Errorf("spool grew past cap: %d bytes", fi.Size())
	}
	recs, err := spoolDrain()
	if err != nil {
		t.Fatal(err)
	}
	if len(recs) == 0 {
		t.Error("rotation dropped everything")
	}
	for _, r := range recs {
		if r.SessionID != "s" {
			t.Fatalf("corrupt record after rotation: %+v", r)
		}
	}
}

func TestUlidUnique(t *testing.T) {
	seen := map[string]bool{}
	for i := 0; i < 1000; i++ {
		id := NewULID()
		if seen[id] {
			t.Fatalf("duplicate ulid: %s", id)
		}
		seen[id] = true
	}
}
