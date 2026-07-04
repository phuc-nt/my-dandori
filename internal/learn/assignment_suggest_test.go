package learn

import (
	"testing"

	"github.com/phuc-nt/dandori/internal/store/testseed"
)

// TestSuggestAgentsRanksHigherSuccessFirst seeds two agents with the same
// task keyword ("checkout"): a1 all-done (cheap), a2 all-failed (expensive).
// a1 must rank first — higher success rate AND lower cost, no ambiguity.
func TestSuggestAgentsRanksHigherSuccessFirst(t *testing.T) {
	st := testStore(t)
	testseed.Agent(t, st, "a1")
	testseed.Agent(t, st, "a2")

	testseed.WorkItem(t, st, "jira", "SCRUM-1", "Done")
	testseed.WorkItem(t, st, "jira", "SCRUM-2", "Done")
	st.DB.Exec(`UPDATE work_items SET title='Fix checkout flow bug' WHERE key='SCRUM-1'`)
	st.DB.Exec(`UPDATE work_items SET title='Improve checkout latency' WHERE key='SCRUM-2'`)

	testseed.Run(t, st, "a1-r1", "a1", "done", 1, 0.50)
	testseed.Run(t, st, "a1-r2", "a1", "done", 2, 0.60)
	st.DB.Exec(`UPDATE runs SET task_key='SCRUM-1' WHERE id='a1-r1'`)
	st.DB.Exec(`UPDATE runs SET task_key='SCRUM-2' WHERE id='a1-r2'`)

	testseed.Run(t, st, "a2-r1", "a2", "failed", 1, 5.00)
	testseed.Run(t, st, "a2-r2", "a2", "failed", 2, 6.00)
	st.DB.Exec(`UPDATE runs SET task_key='SCRUM-1' WHERE id='a2-r1'`)
	st.DB.Exec(`UPDATE runs SET task_key='SCRUM-2' WHERE id='a2-r2'`)

	out, err := SuggestAgents(st, "please fix the checkout flow", 5)
	if err != nil {
		t.Fatal(err)
	}
	if len(out) != 2 {
		t.Fatalf("suggestions = %d, want 2", len(out))
	}
	if out[0].AgentID != "a1" {
		t.Errorf("top suggestion = %s, want a1 (higher success, lower cost)", out[0].AgentID)
	}
	if out[0].Samples != 2 || out[1].Samples != 2 {
		t.Errorf("sample counts = %d,%d want 2,2", out[0].Samples, out[1].Samples)
	}
	if out[0].SuccessRate != 1.0 {
		t.Errorf("a1 success rate = %f, want 1.0", out[0].SuccessRate)
	}
}

// Empty fleet history for the task's keywords → empty, no-data result (not
// an error, not a panic).
func TestSuggestAgentsEmptyHistoryReturnsEmpty(t *testing.T) {
	st := testStore(t)
	testseed.Agent(t, st, "a1")
	testseed.Run(t, st, "a1-r1", "a1", "done", 1, 1.0)
	// No work_items at all — task_key never joins.

	out, err := SuggestAgents(st, "totally unrelated obscure task description", 5)
	if err != nil {
		t.Fatal(err)
	}
	if len(out) != 0 {
		t.Errorf("suggestions = %d, want 0 (no matching history)", len(out))
	}
}

func TestExtractKeywordsDropsStopwordsAndShortTokens(t *testing.T) {
	kws := extractKeywords("Fix the login bug for id 5")
	want := map[string]bool{"login": true, "bug": true}
	if len(kws) != len(want) {
		t.Fatalf("keywords = %v, want exactly %v", kws, want)
	}
	for _, k := range kws {
		if !want[k] {
			t.Errorf("unexpected keyword %q", k)
		}
	}
}
