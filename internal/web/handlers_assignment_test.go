package web

import (
	"net/http"
	"net/url"
	"strings"
	"testing"

	"github.com/phuc-nt/dandori/internal/store/testseed"
)

func TestAssignmentSuggestNoTaskShowsPrompt(t *testing.T) {
	s := testServer(t)
	s.registerPhase05Routes()
	rec := get(t, s, "/assign/suggest")
	if rec.Code != http.StatusOK {
		t.Fatalf("GET /assign/suggest → %d", rec.Code)
	}
}

func TestAssignmentSuggestRanksAgent(t *testing.T) {
	s := testServer(t)
	s.registerPhase05Routes()
	testseed.Agent(t, s.Store, "a1")
	testseed.WorkItem(t, s.Store, "jira", "SCRUM-1", "Done")
	s.Store.DB.Exec(`UPDATE work_items SET title='Fix login bug' WHERE key='SCRUM-1'`)
	testseed.Run(t, s.Store, "a1-r1", "a1", "done", 1, 0.5)
	s.Store.DB.Exec(`UPDATE runs SET task_key='SCRUM-1' WHERE id='a1-r1'`)

	rec := get(t, s, "/assign/suggest?task="+url.QueryEscape("fix the login bug"))
	if rec.Code != http.StatusOK {
		t.Fatalf("GET /assign/suggest?task=... → %d", rec.Code)
	}
	if !strings.Contains(rec.Body.String(), "a1") {
		t.Errorf("ranked fragment missing agent a1: %s", rec.Body.String())
	}
}

// L7 is suggest-only: the route must never accept a POST or issue any Jira
// write side effect. Absence of a POST route is the contract here.
func TestAssignmentSuggestHasNoWriteRoute(t *testing.T) {
	s := testServer(t)
	s.registerPhase05Routes()
	rec := postForm(t, s, "/assign/suggest", url.Values{"task": {"x"}})
	if rec.Code == http.StatusOK {
		t.Error("POST /assign/suggest should not be a valid write endpoint")
	}
}
