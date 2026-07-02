package ingest

import (
	"fmt"
	"net/http"
	"path/filepath"
	"sync"
	"testing"

	"github.com/phuc-nt/dandori/internal/config"
	"github.com/phuc-nt/dandori/internal/store"
)

// Multi-machine burst: concurrent batches must all land (or 429 cleanly),
// with no lost counts and no race (run with -race).
func TestIngestConcurrentSenders(t *testing.T) {
	s, st := testServer(t)
	h := s.Handler()
	const senders, perSender = 8, 5
	var wg sync.WaitGroup
	for i := 0; i < senders; i++ {
		wg.Add(1)
		go func(machine int) {
			defer wg.Done()
			for j := 0; j < perSender; j++ {
				b := Batch{Records: []Record{{
					Type: "event", SessionID: fmt.Sprintf("s-%d", machine), AgentName: "a",
					ULID: fmt.Sprintf("%d-%d", machine, j), Kind: "tool_use", Tool: "Bash",
				}}}
				for {
					w := postBatch(t, h, "secret-token", b)
					if w.Code == http.StatusOK {
						break
					}
					if w.Code != http.StatusTooManyRequests {
						t.Errorf("machine %d: %d %s", machine, w.Code, w.Body.String())
						return
					} // 429 → semaphore full, retry like the client spool would
				}
			}
		}(i)
	}
	wg.Wait()
	var events, runs int
	st.DB.QueryRow(`SELECT count(*) FROM events WHERE kind='tool_use'`).Scan(&events)
	st.DB.QueryRow(`SELECT count(*) FROM runs`).Scan(&runs)
	if events != senders*perSender || runs != senders {
		t.Errorf("events=%d (want %d) runs=%d (want %d)", events, senders*perSender, runs, senders)
	}
}

// Documented ceiling: single-writer SQLite, batches of 20 events.
func BenchmarkIngestBatch20(b *testing.B) {
	st, err := store.Open(filepath.Join(b.TempDir(), "bench.db"))
	if err != nil {
		b.Fatal(err)
	}
	defer st.Close()
	cfg := &config.Config{IngestToken: "secret-token"}
	s := NewServer(cfg, st)
	h := s.Handler()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		recs := make([]Record, 20)
		for j := range recs {
			recs[j] = Record{Type: "event", SessionID: "bench", AgentName: "a",
				ULID: fmt.Sprintf("b-%d-%d", i, j), Kind: "tool_use", Tool: "Bash", Payload: "go test ./..."}
		}
		w := postBatch(&testing.T{}, h, "secret-token", Batch{Records: recs})
		if w.Code != http.StatusOK {
			b.Fatalf("status %d", w.Code)
		}
	}
}
