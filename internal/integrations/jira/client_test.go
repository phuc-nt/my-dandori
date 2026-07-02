package jira

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"

	"github.com/phuc-nt/dandori/internal/store"
)

func fakeJira(t *testing.T) (*Client, *httptest.Server, *map[string]any) {
	t.Helper()
	var lastCreate map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.URL.Path == "/rest/api/3/search/jql" && r.Method == "GET":
			json.NewEncoder(w).Encode(map[string]any{"issues": []any{
				map[string]any{"key": "SCRUM-1", "fields": map[string]any{
					"summary": "Login bug", "status": map[string]string{"name": "Done"},
					"assignee": map[string]string{"displayName": "Phuc"},
					"labels":   []string{"agent"}, "updated": "2026-07-01T00:00:00Z"}},
				map[string]any{"key": "SCRUM-2", "fields": map[string]any{
					"summary": "Add feature", "status": map[string]string{"name": "In Progress"},
					"labels": []string{}, "updated": "2026-07-01T01:00:00Z"}},
			}})
		case r.URL.Path == "/rest/api/3/issue" && r.Method == "POST":
			json.NewDecoder(r.Body).Decode(&lastCreate)
			w.WriteHeader(201)
			json.NewEncoder(w).Encode(map[string]string{"key": "SCRUM-99"})
		default:
			w.WriteHeader(404)
		}
	}))
	t.Cleanup(srv.Close)
	c := New("test", "e@x.com", "tok")
	c.BaseURL = srv.URL
	return c, srv, &lastCreate
}

func TestSyncIssues(t *testing.T) {
	st, err := store.Open(filepath.Join(t.TempDir(), "j.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	c, _, _ := fakeJira(t)

	n, err := SyncIssues(st, c, "SCRUM")
	if err != nil {
		t.Fatal(err)
	}
	if n != 2 {
		t.Errorf("synced: %d", n)
	}
	var status string
	var isAgent int
	st.DB.QueryRow(`SELECT status, is_agent FROM work_items WHERE source='jira' AND key='SCRUM-1'`).
		Scan(&status, &isAgent)
	if status != "Done" || isAgent != 1 {
		t.Errorf("SCRUM-1: status=%s is_agent=%d", status, isAgent)
	}
	// Re-sync updates, no duplicates.
	SyncIssues(st, c, "SCRUM")
	var count int
	st.DB.QueryRow(`SELECT count(*) FROM work_items WHERE source='jira'`).Scan(&count)
	if count != 2 {
		t.Errorf("duplicated on resync: %d", count)
	}
}

func TestCreateIssuePayload(t *testing.T) {
	c, _, lastCreate := fakeJira(t)
	key, err := c.CreateIssue("SCRUM", "flagged run", "details here", []string{"dandori"})
	if err != nil {
		t.Fatal(err)
	}
	if key != "SCRUM-99" {
		t.Errorf("key: %s", key)
	}
	fields := (*lastCreate)["fields"].(map[string]any)
	if fields["summary"] != "flagged run" {
		t.Errorf("summary: %v", fields["summary"])
	}
	if fields["project"].(map[string]any)["key"] != "SCRUM" {
		t.Errorf("project: %v", fields["project"])
	}
	if fields["issuetype"].(map[string]any)["name"] != "Task" {
		t.Errorf("issuetype: %v", fields["issuetype"])
	}
	desc := fields["description"].(map[string]any)
	if desc["type"] != "doc" {
		t.Errorf("description must be ADF: %v", desc)
	}
}
