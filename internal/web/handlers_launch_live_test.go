package web

import (
	"net/http"
	"strings"
	"testing"

	"github.com/phuc-nt/dandori/internal/store"
)

func seedConsoleRun(t *testing.T, s *Server, id, status string) {
	t.Helper()
	s.Store.DB.Exec(`INSERT INTO agents(id,name,created_at) VALUES('a','a','now') ON CONFLICT DO NOTHING`)
	s.Store.DB.Exec(`INSERT INTO runs(id,session_id,agent_id,source,status,started_at) VALUES(?,?,'a','console',?, ?)`,
		id, id, status, store.Now())
}

// Terminal runs return HTTP 286 so the poller stops; running runs return 200
// with a live-poll fragment + Kill.
func TestStatusFragmentStopsPollingWhenTerminal(t *testing.T) {
	s := testServer(t)
	seedConsoleRun(t, s, "r-run", "running")
	seedConsoleRun(t, s, "r-done", "done")

	rec := get(t, s, "/runs/r-run/status-fragment")
	if rec.Code != http.StatusOK {
		t.Errorf("running: code %d, want 200", rec.Code)
	}
	if !strings.Contains(rec.Body.String(), "every 2s") || !strings.Contains(rec.Body.String(), "Dừng") {
		t.Errorf("running fragment should poll + show Kill: %s", rec.Body.String())
	}

	rec2 := get(t, s, "/runs/r-done/status-fragment")
	if rec2.Code != 286 {
		t.Errorf("done: code %d, want 286 (stop polling)", rec2.Code)
	}
	if strings.Contains(rec2.Body.String(), "every 2s") || strings.Contains(rec2.Body.String(), "Dừng") {
		t.Error("terminal fragment must not poll or show Kill")
	}
}

// Agent stdout containing HTML must render escaped (XSS defense).
func TestLogTailEscapesOutput(t *testing.T) {
	s := testServer(t)
	seedConsoleRun(t, s, "r-xss", "running")
	s.Store.DB.Exec(`INSERT INTO events(run_id,ts,kind,payload) VALUES('r-xss', ?, 'run_stdout', ?)`,
		store.Now(), `<script>alert(1)</script>`)

	rec := get(t, s, "/runs/r-xss/log-tail?since=0")
	body := rec.Body.String()
	if strings.Contains(body, "<script>alert(1)</script>") {
		t.Error("raw script tag rendered — XSS hole")
	}
	if !strings.Contains(body, "&lt;script&gt;") {
		t.Errorf("script not escaped: %s", body)
	}
	// Cursor bumped in the re-rendered poller.
	if !strings.Contains(body, "since=") {
		t.Error("log poller cursor not re-rendered")
	}
}

func TestLogTailCursorAdvances(t *testing.T) {
	s := testServer(t)
	seedConsoleRun(t, s, "r-cur", "running")
	s.Store.DB.Exec(`INSERT INTO events(run_id,ts,kind,payload) VALUES('r-cur', ?, 'run_stdout', 'dòng 1')`, store.Now())
	var firstID int64
	s.Store.DB.QueryRow(`SELECT id FROM events WHERE run_id='r-cur'`).Scan(&firstID)

	// since=firstID → no new lines (strictly id > since).
	rec := get(t, s, "/runs/r-cur/log-tail?since="+itoa64(firstID))
	if strings.Contains(rec.Body.String(), "dòng 1") {
		t.Error("already-seen line re-sent (cursor not strictly >)")
	}
}

func itoa64(n int64) string {
	if n == 0 {
		return "0"
	}
	var b []byte
	for n > 0 {
		b = append([]byte{byte('0' + n%10)}, b...)
		n /= 10
	}
	return string(b)
}
