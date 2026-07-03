package ingest

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/phuc-nt/dandori/internal/config"
	"github.com/phuc-nt/dandori/internal/contexthub"
	"github.com/phuc-nt/dandori/internal/store"
)

func seedContext(t *testing.T, st *store.Store) {
	t.Helper()
	h := contexthub.New(st)
	if _, err := h.SaveContext(contexthub.LayerCompany, "*", "Không commit secret.", "phuc", ""); err != nil {
		t.Fatal(err)
	}
	st.DB.Exec(`INSERT INTO agents(id, name, created_at) VALUES('bot','bot','now')`)
}

func TestContextEndpointServesMerged(t *testing.T) {
	s, st := testServer(t)
	seedContext(t, st)
	req := httptest.NewRequest(http.MethodGet, "/ingest/context?agent=bot", nil)
	req.Header.Set("Authorization", "Bearer secret-token")
	w := httptest.NewRecorder()
	s.Handler().ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("status %d", w.Code)
	}
	var body struct {
		Context    string                `json:"context"`
		Provenance contexthub.Provenance `json:"provenance"`
	}
	json.Unmarshal(w.Body.Bytes(), &body)
	if body.Context == "" || body.Provenance.Company == nil {
		t.Errorf("empty context/prov: %+v", body)
	}
}

func TestContextEndpointUnknownAgentEmpty(t *testing.T) {
	s, st := testServer(t)
	seedContext(t, st) // only company exists → any agent gets company layer
	// An agent with no docs at all still gets the company layer (org-wide) —
	// use an agent with zero applicable docs by clearing company:
	st.DB.Exec(`DELETE FROM context_heads`)
	req := httptest.NewRequest(http.MethodGet, "/ingest/context?agent=nobody", nil)
	req.Header.Set("Authorization", "Bearer secret-token")
	w := httptest.NewRecorder()
	s.Handler().ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("status %d", w.Code)
	}
	var body struct {
		Context string `json:"context"`
	}
	json.Unmarshal(w.Body.Bytes(), &body)
	if body.Context != "" {
		t.Errorf("no-docs should give empty context: %q", body.Context)
	}
}

func TestContextEndpointUnauthorized(t *testing.T) {
	s, _ := testServer(t)
	req := httptest.NewRequest(http.MethodGet, "/ingest/context?agent=bot", nil)
	w := httptest.NewRecorder()
	s.Handler().ServeHTTP(w, req)
	if w.Code != http.StatusUnauthorized {
		t.Errorf("no token: %d, want 401", w.Code)
	}
}

// The central client must keep a good stale cache when the server returns
// non-200 (incl. 500 on DB error), never overwrite it with empty (M4).
func TestContextClientStaleOnServerError(t *testing.T) {
	isolateHome(t)
	// Prime the cache with a good value via a working server.
	good := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(map[string]any{"context": "GOOD", "provenance": map[string]int{"company": 1}})
	}))
	cfg := &config.Config{ServerURL: good.URL, IngestToken: "t"}
	c := NewClient(cfg)
	if txt, _ := c.Context("bot"); txt != "GOOD" {
		t.Fatalf("prime: %q", txt)
	}
	good.Close()

	// Now the server errors (500). Force cache staleness by rewinding the
	// cached timestamp, then confirm the client keeps the stale GOOD value.
	bad := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "boom", http.StatusInternalServerError)
	}))
	defer bad.Close()
	cfg.ServerURL = bad.URL
	expireContextCache(t)
	if txt, _ := c.Context("bot"); txt != "GOOD" {
		t.Errorf("server 500 overwrote good stale cache: %q", txt)
	}
}

// expireContextCache rewrites every entry's timestamp far in the past so the
// next Context() call treats the cache as stale and tries the network.
func expireContextCache(t *testing.T) {
	t.Helper()
	all := readContextCache()
	for k, v := range all {
		v.CachedAt = v.CachedAt.Add(-time.Hour)
		all[k] = v
	}
	writeContextCache(all)
}
