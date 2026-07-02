package confluence

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	"github.com/phuc-nt/dandori/internal/config"
	"github.com/phuc-nt/dandori/internal/integrations"
	"github.com/phuc-nt/dandori/internal/store"
	"github.com/phuc-nt/dandori/internal/store/testseed"
)

func fakeConfluence(t *testing.T) (*Client, *map[string]any) {
	t.Helper()
	var lastCreate map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case strings.HasPrefix(r.URL.Path, "/wiki/api/v2/pages/98466") && r.Method == "GET":
			json.NewEncoder(w).Encode(map[string]any{
				"title": "OKR page",
				"body":  map[string]any{"storage": map[string]any{"value": "<h1>Goals</h1><p>Ship it</p>"}},
			})
		case r.URL.Path == "/wiki/api/v2/pages" && r.Method == "POST":
			json.NewDecoder(r.Body).Decode(&lastCreate)
			w.WriteHeader(201)
			json.NewEncoder(w).Encode(map[string]string{"id": "424242"})
		default:
			w.WriteHeader(404)
		}
	}))
	t.Cleanup(srv.Close)
	c := New("test", "e@x.com", "tok")
	c.BaseURL = srv.URL
	return c, &lastCreate
}

func TestGetPageText(t *testing.T) {
	c, _ := fakeConfluence(t)
	title, text, err := c.GetPageText("98466")
	if err != nil {
		t.Fatal(err)
	}
	if title != "OKR page" || text != "Goals Ship it" {
		t.Errorf("got %q / %q", title, text)
	}
}

func TestReporterPostAndDedup(t *testing.T) {
	st, err := store.Open(filepath.Join(t.TempDir(), "c.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	testseed.Agent(t, st, "a1")
	testseed.Run(t, st, "r1", "a1", "done", 1, 2.5)
	cfg, _ := config.Load(filepath.Join(t.TempDir(), "no.yaml"))
	cfg.DryRun = false
	client, lastCreate := fakeConfluence(t)
	r := &Reporter{St: st, Client: client, Guard: &integrations.Guard{Cfg: cfg, St: st},
		SpaceID: "65846", Window: 30}

	pageID, err := r.Post("tester")
	if err != nil {
		t.Fatal(err)
	}
	if pageID != "424242" {
		t.Fatalf("page id: %q", pageID)
	}
	if (*lastCreate)["spaceId"] != "65846" {
		t.Errorf("spaceId: %v", (*lastCreate)["spaceId"])
	}
	body := (*lastCreate)["body"].(map[string]any)["value"].(string)
	for _, want := range []string{"Fleet leaderboard", "a1", "change-failure rate"} {
		if !strings.Contains(body, want) {
			t.Errorf("report body missing %q", want)
		}
	}
	// Same day again → dedup, no second create.
	pageID2, err := r.Post("tester")
	if err != nil || pageID2 != "" {
		t.Errorf("dedup failed: %q %v", pageID2, err)
	}
}

func TestReporterDryRun(t *testing.T) {
	st, _ := store.Open(filepath.Join(t.TempDir(), "d.db"))
	defer st.Close()
	cfg, _ := config.Load(filepath.Join(t.TempDir(), "no.yaml")) // DryRun default true
	client, lastCreate := fakeConfluence(t)
	r := &Reporter{St: st, Client: client, Guard: &integrations.Guard{Cfg: cfg, St: st},
		SpaceID: "65846", Window: 30}
	pageID, err := r.Post("tester")
	if err != nil || pageID != "dry-run" {
		t.Errorf("dry run: %q %v", pageID, err)
	}
	if *lastCreate != nil {
		t.Error("dry run must not call the API")
	}
}
