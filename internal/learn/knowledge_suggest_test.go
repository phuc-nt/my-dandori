package learn

import (
	"testing"

	"github.com/phuc-nt/dandori/internal/store"
	"github.com/phuc-nt/dandori/internal/store/testseed"
)

// publishSkillUnit nominates + force-transitions a skill-kind unit straight
// to state=published (bypassing submit/review — this test file only cares
// about suggest-time behavior on an already-published unit, not the review
// workflow itself, which knowledge_units_test.go already covers).
func publishSkillUnit(t *testing.T, st *store.Store, name, title string) int64 {
	t.Helper()
	id, err := NominateUnit(st, NominateParams{
		Kind: KindSkill, Name: name, Title: title,
		Body:        "# " + title + "\nsteps...",
		NominatedBy: "dandori-observer",
	})
	if err != nil {
		t.Fatalf("nominate %s: %v", name, err)
	}
	if err := transition(st, id, StateNominated, StateInReview, "admin", "auto"); err != nil {
		t.Fatalf("submit %s: %v", name, err)
	}
	if err := transition(st, id, StateInReview, StatePublished, "admin", "auto"); err != nil {
		t.Fatalf("publish %s: %v", name, err)
	}
	return id
}

// publishContextUnit is publishSkillUnit's context-kind twin, carrying a
// layer/layer_target so alreadyUsed's context-layer exclusion has something
// to match against.
func publishContextUnit(t *testing.T, st *store.Store, name, title, layer, layerTarget string) int64 {
	t.Helper()
	id, err := NominateUnit(st, NominateParams{
		Kind: KindContext, Name: name, Title: title,
		RefKind: "context_version", RefID: 1,
		Layer: layer, LayerTarget: layerTarget,
		NominatedBy: "dandori-observer",
	})
	if err != nil {
		t.Fatalf("nominate %s: %v", name, err)
	}
	if err := transition(st, id, StateNominated, StateInReview, "admin", "auto"); err != nil {
		t.Fatalf("submit %s: %v", name, err)
	}
	if err := transition(st, id, StateInReview, StatePublished, "admin", "auto"); err != nil {
		t.Fatalf("publish %s: %v", name, err)
	}
	return id
}

// seedAgentTaskHistory gives agentID one done run against a work item whose
// title carries the given keyword, so agentTaskKeywords has something to
// extract for matching against a unit's title/name.
func seedAgentTaskHistory(t *testing.T, st *store.Store, agentID, runID, keyword string) {
	t.Helper()
	testseed.Agent(t, st, agentID)
	key := "SCRUM-" + runID
	testseed.WorkItem(t, st, "jira", key, "Done")
	if _, err := st.DB.Exec(`UPDATE work_items SET title = ? WHERE key = ?`,
		"Improve "+keyword+" handling", key); err != nil {
		t.Fatal(err)
	}
	testseed.Run(t, st, runID, agentID, "done", 1, 1.0)
	if _, err := st.DB.Exec(`UPDATE runs SET task_key = ? WHERE id = ?`, key, runID); err != nil {
		t.Fatal(err)
	}
}

// TestSuggestUnitsForAgentExcludesAlreadyUsedSkill: an agent that has already
// invoked the skill via a Skill tool_use event must never see it suggested
// again, even though the keyword overlap and published state both qualify.
func TestSuggestUnitsForAgentExcludesAlreadyUsedSkill(t *testing.T) {
	st := testStore(t)
	publishSkillUnit(t, st, "checkout-flow-fix", "Checkout flow fix skill")
	seedAgentTaskHistory(t, st, "a1", "r1", "checkout")

	// a1 already used the skill.
	testseed.Event(t, st, "r1", "tool_use", "Skill", 1, `{"skill":"checkout-flow-fix","args":"x"}`)

	out, err := SuggestUnitsForAgent(st, "a1", 5)
	if err != nil {
		t.Fatal(err)
	}
	for _, s := range out {
		if s.Name == "checkout-flow-fix" {
			t.Errorf("already-used skill must be excluded, got suggestion: %+v", s)
		}
	}
}

// TestSuggestUnitsForAgentExcludesAlreadyInjectedContext: a context unit
// targeting a layer the agent already receives (company, always-on) must
// never be suggested — it is already part of the agent's effective context.
func TestSuggestUnitsForAgentExcludesAlreadyInjectedContext(t *testing.T) {
	st := testStore(t)
	publishContextUnit(t, st, "checkout-notes", "Checkout handoff notes", "company", "*")
	seedAgentTaskHistory(t, st, "a1", "r1", "checkout")

	out, err := SuggestUnitsForAgent(st, "a1", 5)
	if err != nil {
		t.Fatal(err)
	}
	for _, s := range out {
		if s.Name == "checkout-notes" {
			t.Errorf("company-layer context already injected, must be excluded: %+v", s)
		}
	}
}

// TestSuggestUnitsForAgentExcludesNonPublished: retired/superseded/nominated
// units must never surface — F5 published-only.
func TestSuggestUnitsForAgentExcludesNonPublished(t *testing.T) {
	st := testStore(t)
	seedAgentTaskHistory(t, st, "a1", "r1", "checkout")

	// nominated only (never submitted/published) — must be excluded.
	if _, err := NominateUnit(st, NominateParams{
		Kind: KindSkill, Name: "checkout-draft-skill", Title: "Checkout draft skill",
		Body: "# draft\nsteps", NominatedBy: "dandori-observer",
	}); err != nil {
		t.Fatal(err)
	}

	// published, then retired — must be excluded.
	retiredID := publishSkillUnit(t, st, "checkout-retired-skill", "Checkout retired skill")
	if err := transition(st, retiredID, StatePublished, StateRetired, "admin", "deprecated"); err != nil {
		t.Fatal(err)
	}

	out, err := SuggestUnitsForAgent(st, "a1", 5)
	if err != nil {
		t.Fatal(err)
	}
	for _, s := range out {
		if s.Name == "checkout-draft-skill" || s.Name == "checkout-retired-skill" {
			t.Errorf("non-published unit must be excluded: %+v", s)
		}
	}
}

// TestSuggestUnitsForAgentNoDataIsEmptyNotError: an agent with zero task
// history must get an empty slice, not an error and not a fabricated entry.
func TestSuggestUnitsForAgentNoDataIsEmptyNotError(t *testing.T) {
	st := testStore(t)
	testseed.Agent(t, st, "lonely")
	publishSkillUnit(t, st, "checkout-flow-fix", "Checkout flow fix skill")

	out, err := SuggestUnitsForAgent(st, "lonely", 5)
	if err != nil {
		t.Fatal(err)
	}
	if len(out) != 0 {
		t.Errorf("agent with no task history must get empty suggestions, got %+v", out)
	}
}

// TestSuggestUnitsForAgentRecomputesLive proves F11: the present/absent
// stats returned by SuggestUnitsForAgent are recomputed FRESH on every call
// from events/runs, never read from the unit's stored nominate-time
// snapshot. Seeds a skill unit, calls once, then adds new run+event data
// that shifts the present-side done-rate, calls again, and asserts the
// numbers actually changed.
func TestSuggestUnitsForAgentRecomputesLive(t *testing.T) {
	st := testStore(t)
	publishSkillUnit(t, st, "checkout-flow-fix", "Checkout flow fix skill")
	seedAgentTaskHistory(t, st, "a1", "r1", "checkout")
	testseed.Agent(t, st, "other-agent")

	// Seed one present-side run (used the skill, failed) and one absent-side
	// run (did not use the skill, done) so both buckets have n>0.
	testseed.Run(t, st, "p1", "other-agent", "failed", 1, 1.0)
	testseed.Event(t, st, "p1", "tool_use", "Skill", 1, `{"skill":"checkout-flow-fix","args":"x"}`)
	testseed.Run(t, st, "abs1", "other-agent", "done", 1, 1.0)

	first, err := SuggestUnitsForAgent(st, "a1", 5)
	if err != nil {
		t.Fatal(err)
	}
	if len(first) != 1 {
		t.Fatalf("expected 1 suggestion, got %d: %+v", len(first), first)
	}
	firstDonePresent := first[0].DonePresent
	firstNPresent := first[0].NPresent

	// Now add several more present-side runs, all done, which must shift
	// DonePresent upward and NPresent up by count — a stored/cached snapshot
	// would return the exact same numbers as the first call.
	for i := 0; i < 4; i++ {
		id := "p-more-" + string(rune('a'+i))
		testseed.Run(t, st, id, "other-agent", "done", 1, 1.0)
		testseed.Event(t, st, id, "tool_use", "Skill", 1, `{"skill":"checkout-flow-fix","args":"x"}`)
	}

	second, err := SuggestUnitsForAgent(st, "a1", 5)
	if err != nil {
		t.Fatal(err)
	}
	if len(second) != 1 {
		t.Fatalf("expected 1 suggestion on second call, got %d: %+v", len(second), second)
	}
	if second[0].NPresent == firstNPresent {
		t.Errorf("NPresent did not change after adding present-side runs: first=%d second=%d — stats are not live-recomputed", firstNPresent, second[0].NPresent)
	}
	if second[0].DonePresent == firstDonePresent {
		t.Errorf("DonePresent did not change after adding done runs: first=%.2f second=%.2f — stats are not live-recomputed", firstDonePresent, second[0].DonePresent)
	}
}

// TestSuggestUnitsForAgentRanksByDeltaTimesOverlap: two candidate skills with
// equal keyword overlap but different present/absent delta — the one with
// the larger positive delta must rank first.
func TestSuggestUnitsForAgentRanksByDeltaTimesOverlap(t *testing.T) {
	st := testStore(t)
	seedAgentTaskHistory(t, st, "a1", "r1", "checkout")
	testseed.Agent(t, st, "other-agent")
	publishSkillUnit(t, st, "checkout-good-skill", "Checkout good skill")
	publishSkillUnit(t, st, "checkout-flat-skill", "Checkout flat skill")

	// good-skill: present all done, absent all failed → large positive delta.
	for i := 0; i < 3; i++ {
		id := "good-p" + string(rune('a'+i))
		testseed.Run(t, st, id, "other-agent", "done", 1, 1.0)
		testseed.Event(t, st, id, "tool_use", "Skill", 1, `{"skill":"checkout-good-skill","args":"x"}`)
	}
	for i := 0; i < 3; i++ {
		id := "good-a" + string(rune('a'+i))
		testseed.Run(t, st, id, "other-agent", "failed", 1, 1.0)
	}

	// flat-skill: present and absent both done → delta ~0 (needs its own
	// isolated runs since liveSkillStats is scoped by skill name via the
	// events join, not by unit, so these do not interfere with good-skill).
	for i := 0; i < 3; i++ {
		id := "flat-p" + string(rune('a'+i))
		testseed.Run(t, st, id, "other-agent", "done", 1, 1.0)
		testseed.Event(t, st, id, "tool_use", "Skill", 1, `{"skill":"checkout-flat-skill","args":"x"}`)
	}
	for i := 0; i < 3; i++ {
		id := "flat-a" + string(rune('a'+i))
		testseed.Run(t, st, id, "other-agent", "done", 1, 1.0)
	}

	out, err := SuggestUnitsForAgent(st, "a1", 5)
	if err != nil {
		t.Fatal(err)
	}
	if len(out) != 2 {
		t.Fatalf("expected 2 suggestions, got %d: %+v", len(out), out)
	}
	if out[0].Name != "checkout-good-skill" {
		t.Errorf("top suggestion = %s, want checkout-good-skill (largest positive delta)", out[0].Name)
	}
}
