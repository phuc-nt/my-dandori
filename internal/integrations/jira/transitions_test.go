package jira

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

func fakeJiraTransitions(t *testing.T, retryOnce bool) (*Client, *int) {
	t.Helper()
	calls := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == "GET" && r.URL.Path == "/rest/api/3/issue/SCRUM-1/transitions":
			json.NewEncoder(w).Encode(map[string]any{"transitions": []any{
				map[string]any{"id": "11", "name": "Done", "hasScreen": true,
					"to": map[string]string{"name": "Done"}},
				map[string]any{"id": "21", "name": "In Progress", "hasScreen": false,
					"to": map[string]string{"name": "In Progress"}},
			}})
		case r.Method == "POST" && r.URL.Path == "/rest/api/3/issue/SCRUM-1/transitions":
			calls++
			if retryOnce && calls == 1 {
				w.WriteHeader(409)
				return
			}
			w.WriteHeader(204)
		default:
			w.WriteHeader(404)
		}
	}))
	t.Cleanup(srv.Close)
	c := New("test", "e@x.com", "tok")
	c.BaseURL = srv.URL
	return c, &calls
}

func TestTransitionsList(t *testing.T) {
	c, _ := fakeJiraTransitions(t, false)
	trs, err := c.Transitions("SCRUM-1")
	if err != nil {
		t.Fatal(err)
	}
	if len(trs) != 2 {
		t.Fatalf("transitions: got %d, want 2", len(trs))
	}
	if trs[0].ID != "11" || trs[0].Name != "Done" || !trs[0].HasScreen {
		t.Errorf("transition[0]: %+v", trs[0])
	}
	if trs[0].To.Name != "Done" {
		t.Errorf("transition[0].To.Name: %s", trs[0].To.Name)
	}
}

func TestTransitionSuccess(t *testing.T) {
	c, calls := fakeJiraTransitions(t, false)
	if err := c.Transition("SCRUM-1", "11"); err != nil {
		t.Fatal(err)
	}
	if *calls != 1 {
		t.Errorf("POST calls: %d, want 1", *calls)
	}
}

func TestTransitionRetriesOnce409(t *testing.T) {
	c, calls := fakeJiraTransitions(t, true)
	if err := c.Transition("SCRUM-1", "11"); err != nil {
		t.Fatal(err)
	}
	if *calls != 2 {
		t.Errorf("POST calls: %d, want 2 (initial 409 + retry)", *calls)
	}
}

func TestTransitionErrorOnBadKey(t *testing.T) {
	c, _ := fakeJiraTransitions(t, false)
	if err := c.Transition("NOPE-1", "11"); err == nil {
		t.Fatal("expected error for unknown issue")
	}
}
