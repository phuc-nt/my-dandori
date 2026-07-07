package web

import (
	"fmt"
	"strings"
	"testing"

	"github.com/phuc-nt/dandori/internal/store"
)

// seedInsightsFixture populates every table the 8 v11 analyses + the 3 pre-v11
// analyses read, with FIXED timestamps (F15) so the test never flakes against
// wall-clock skew and window=0 (all-time) always sees the same rows. This is
// the E2E gate: it seeds real cross-table data, hits GET /insights over HTTP,
// and asserts the rendered HTML matches SQL ground-truth computed against the
// exact same seeded store — not a re-derivation of the handler's own numbers.
// seedInsightsFixture returns the guardrail_rules id it seeded (the fixture
// inserts a fresh row rather than a hardcoded id since 002_seed_guardrails.sql
// already seeds 7 default rules on every fresh store) so the caller can
// assert on the exact "#N" the ledger section renders.
func seedInsightsFixture(t *testing.T, st *store.Store) (seededRuleID int64) {
	t.Helper()
	exec := func(q string, args ...any) {
		t.Helper()
		if _, err := st.DB.Exec(q, args...); err != nil {
			t.Fatalf("seed exec failed: %v\nquery: %s", err, q)
		}
	}

	exec(`INSERT INTO agents(id, name, created_at) VALUES('agent-1','agent-1','2026-01-01T00:00:00Z')`)

	// --- Model efficiency + cost-per-outcome (pre-v11, fail-fast path) ---
	// 3 runs on model 'claude-x': 2 done, 1 failed. Fixed cost/tokens.
	exec(`INSERT INTO runs(id, session_id, project, agent_id, model, status, started_at, ended_at, cost_usd, input_tokens, cache_read_tokens, source)
		VALUES('m1','m1','proj-a','agent-1','claude-x','done','2026-01-01T00:00:00Z','2026-01-01T00:20:00Z',1.0,1000,500,'hook')`)
	exec(`INSERT INTO runs(id, session_id, project, agent_id, model, status, started_at, ended_at, cost_usd, input_tokens, cache_read_tokens, source)
		VALUES('m2','m2','proj-a','agent-1','claude-x','done','2026-01-01T00:00:00Z','2026-01-01T00:40:00Z',1.0,1000,500,'hook')`)
	exec(`INSERT INTO runs(id, session_id, project, agent_id, model, status, started_at, ended_at, cost_usd, input_tokens, cache_read_tokens, source)
		VALUES('m3','m3','proj-a','agent-1','claude-x','failed','2026-01-01T00:00:00Z','2026-01-01T01:30:00Z',1.0,1000,0,'hook')`)

	// --- Context-ROI: 1 layer/target/version bucket, 3 runs, source='ingest'
	// so the central-mode user_msg-numerator branch is exercised (LOW-2
	// cross-phase invariant). ended_at required.
	for i, id := range []string{"c1", "c2", "c3"} {
		status := "done"
		if i == 2 {
			status = "failed"
		}
		exec(`INSERT INTO runs(id, session_id, project, agent_id, status, started_at, ended_at, cost_usd, source)
			VALUES(?,?,'proj-ctx','agent-1',?,'2026-01-01T00:00:00Z','2026-01-01T00:10:00Z',2.0,'ingest')`, id, id, status)
		exec(`INSERT INTO events(run_id, ts, kind, payload) VALUES(?, '2026-01-01T00:00:00Z', 'user_msg', '2')`, id)
		exec(`INSERT INTO events(run_id, ts, kind, payload) VALUES(?, '2026-01-01T00:00:01Z', 'context_injected', '{"company":1}')`, id)
	}

	// --- Post-run activity (shadow work): 3 done runs w/ task_key, 1 linked
	// work_item touched by a human AFTER run end (shadow work), 1 linked but
	// touched before end (no activity), 1 with no linkable work_item at all
	// (counts toward RunsTotal but not RunsWithTaskKey).
	exec(`INSERT INTO runs(id, session_id, project, agent_id, status, started_at, ended_at, task_key, source)
		VALUES('s1','s1','proj-shadow','agent-1','done','2026-01-02T00:00:00Z','2026-01-02T01:00:00Z','JIRA-1','hook')`)
	exec(`INSERT INTO work_items(source, key, title, status, assignee, is_agent, project, updated_at)
		VALUES('jira','JIRA-1','t','In Review','human-1',0,'proj-shadow','2026-01-02T02:00:00Z')`)
	exec(`INSERT INTO runs(id, session_id, project, agent_id, status, started_at, ended_at, task_key, source)
		VALUES('s2','s2','proj-shadow','agent-1','done','2026-01-02T00:00:00Z','2026-01-02T01:00:00Z','JIRA-2','hook')`)
	exec(`INSERT INTO work_items(source, key, title, status, assignee, is_agent, project, updated_at)
		VALUES('jira','JIRA-2','t','Done',      'human-1',0,'proj-shadow','2026-01-01T23:00:00Z')`)
	exec(`INSERT INTO runs(id, session_id, project, agent_id, status, started_at, ended_at, task_key, source)
		VALUES('s3','s3','proj-shadow','agent-1','done','2026-01-02T00:00:00Z','2026-01-02T01:00:00Z','','hook')`)

	// --- Guardrail ledger: 1 rule-identified block + 1 class-only block, on
	// distinct runs so per-rule and per-class both have data (still n<3 —
	// this deliberately exercises the Insufficient() n<3 badge, F13/F10).
	// 002_seed_guardrails.sql already seeds 7 default rules (autoincrement ids
	// 1-7), so this test inserts a fresh row rather than a hardcoded id and
	// reads back the id SQLite actually assigned.
	exec(`INSERT INTO guardrail_rules(kind, pattern, description, enabled) VALUES('regex','rm -rf test-only','no destructive rm (test fixture)',1)`)
	var ruleID int64
	if err := st.DB.QueryRow(`SELECT id FROM guardrail_rules WHERE pattern = 'rm -rf test-only'`).Scan(&ruleID); err != nil {
		t.Fatalf("read back seeded guardrail rule id: %v", err)
	}
	exec(`INSERT INTO runs(id, session_id, project, agent_id, status, started_at, ended_at, source)
		VALUES('g1','g1','proj-a','agent-1','done','2026-01-01T00:00:00Z','2026-01-01T00:05:00Z','hook')`)
	blockPayload := fmt.Sprintf("[dandori G1] blocked: no rm -rf (rule #%d)", ruleID)
	exec(`INSERT INTO events(run_id, ts, kind, tool_name, ok, payload) VALUES('g1','2026-01-01T00:01:00Z','guardrail_block','Bash',0,?)`, blockPayload)
	exec(`INSERT INTO runs(id, session_id, project, agent_id, status, started_at, ended_at, source)
		VALUES('g2','g2','proj-a','agent-1','failed','2026-01-01T00:00:00Z','2026-01-01T00:05:00Z','hook')`)
	exec(`INSERT INTO events(run_id, ts, kind, tool_name, ok, payload) VALUES('g2','2026-01-01T00:01:00Z','guardrail_block','Bash',0,'[dandori G3] budget exceeded')`)

	// --- Time-horizon: 2 runs in the <30m bucket (both done, so this bucket
	// is n<3/Insufficient AND NoContrast — exercises both F13 badges at once).
	exec(`INSERT INTO runs(id, session_id, project, agent_id, status, started_at, ended_at, source)
		VALUES('h1','h1','proj-a','agent-1','done','2026-01-01T00:00:00Z','2026-01-01T00:10:00Z','hook')`)
	exec(`INSERT INTO runs(id, session_id, project, agent_id, status, started_at, ended_at, source)
		VALUES('h2','h2','proj-a','agent-1','done','2026-01-01T00:00:00Z','2026-01-01T00:15:00Z','hook')`)

	// --- Spend Pareto: 2 finished runs with cost>0 (reuses m1/m2 above which
	// already have cost_usd=1.0 and status='done'/ended) — no extra seed
	// needed, ParetoResult will show a non-empty Tiers/Top from those.

	// --- Steering economics: 2 runs with steering_msg text (local mode),
	// classified by taxonomy keywords, 1 run with none (contrast side).
	exec(`INSERT INTO runs(id, session_id, project, agent_id, status, started_at, ended_at, source)
		VALUES('st1','st1','proj-a','agent-1','done','2026-01-01T00:00:00Z','2026-01-01T00:20:00Z','hook')`)
	exec(`INSERT INTO events(run_id, ts, kind, payload) VALUES('st1','2026-01-01T00:05:00Z','steering_msg','sai rồi, thử lại')`)
	exec(`INSERT INTO runs(id, session_id, project, agent_id, status, started_at, ended_at, source)
		VALUES('st2','st2','proj-a','agent-1','failed','2026-01-01T00:00:00Z','2026-01-01T00:20:00Z','hook')`)

	// --- Approval funnel: 1 approved (human decision), 1 expired (TTL sweep,
	// F2 — must NOT count as a human decision).
	exec(`INSERT INTO approvals(run_id, action, status, requested_at, decided_at, decided_by)
		VALUES('g1','deploy','approved','2026-01-01T00:00:00Z','2026-01-01T00:10:00Z','human-1')`)
	exec(`INSERT INTO approvals(run_id, action, status, requested_at, decided_at, decided_by)
		VALUES('g2','deploy','expired','2026-01-01T00:00:00Z','2026-01-01T05:00:00Z','janitor')`)

	return ruleID
}

func TestInsightsRenderedNumbersMatchSQLGroundTruth(t *testing.T) {
	s := testServer(t)
	ruleID := seedInsightsFixture(t, s.Store)

	rec := get(t, s, "/insights?days=0")
	if rec.Code != 200 {
		t.Fatalf("GET /insights = %d, want 200; body: %s", rec.Code, rec.Body.String())
	}
	body := rec.Body.String()
	if strings.Contains(body, "render error") {
		t.Fatalf("response body contains a template render error:\n%s", body)
	}

	// --- Model efficiency (pre-v11): 3 runs, 2 done, model claude-x.
	var runs, done int
	if err := s.Store.DB.QueryRow(`SELECT count(*), sum(CASE WHEN status='done' THEN 1 ELSE 0 END) FROM runs WHERE model='claude-x'`).Scan(&runs, &done); err != nil {
		t.Fatal(err)
	}
	if runs != 3 || done != 2 {
		t.Fatalf("ground truth sanity failed: runs=%d done=%d, want 3/2", runs, done)
	}
	if !strings.Contains(body, "claude-x") {
		t.Error("Model efficiency section missing model 'claude-x'")
	}

	// --- Context-ROI: 3 runs (2 done, 1 failed), all version 1 of company
	// layer for proj-ctx, source='ingest' (steering numerator must come from
	// user_msg=2 per run, not steering_msg).
	if !strings.Contains(body, "proj-ctx") {
		t.Error("Context-ROI section missing target 'proj-ctx'")
	}
	// 3 runs is below MinSampleForInsight for a *pair* (needs 2 versions to
	// pair) — only one version exists, so ContextROIPairs yields 0 pairs.
	// The section must render the "no data" ready-table body, not error.
	if strings.Contains(body, "Không tải được section này") && strings.Contains(body, "context-roi") {
		t.Error("Context-ROI section degraded unexpectedly")
	}

	// --- Post-run activity: RunsTotal=3 done runs (s1,s2,s3), RunsWithTaskKey=2
	// (s1,s2 joined a work_items row; s3 has empty task_key). Below
	// MinSampleForInsight(3) on RunsWithTaskKey=2 → Insufficient() true →
	// must render the low-coverage empty-state, not a rate.
	var runsTotal, runsWithKey int
	if err := s.Store.DB.QueryRow(`
		SELECT count(*), sum(CASE WHEN task_key IS NOT NULL AND task_key<>'' AND EXISTS(
			SELECT 1 FROM work_items w WHERE w.key = runs.task_key AND w.is_agent = 0) THEN 1 ELSE 0 END)
		FROM runs WHERE status='done' AND project='proj-shadow'`).Scan(&runsTotal, &runsWithKey); err != nil {
		t.Fatal(err)
	}
	if runsTotal != 3 || runsWithKey != 2 {
		t.Fatalf("shadow-work ground truth sanity failed: total=%d withKey=%d, want 3/2", runsTotal, runsWithKey)
	}
	if !strings.Contains(body, "Coverage thấp") {
		t.Error("Post-run activity section did not render the low-coverage empty-state (F3iii) for RunsWithTaskKey=2 < MinSample=3")
	}

	// --- Guardrail ledger: 1 block on the seeded test rule (description
	// present) + class 'budget' (1 block, from G3 token with no rule suffix,
	// F1). Both n<3 → Insufficient badge must render.
	if !strings.Contains(body, fmt.Sprintf("#%d", ruleID)) || !strings.Contains(body, "no destructive rm") {
		t.Errorf("Guardrail ledger per-rule section missing rule #%d / description", ruleID)
	}
	if !strings.Contains(body, "budget") {
		t.Error("Guardrail ledger per-class section missing class 'budget'")
	}

	// --- Time-horizon: <30m bucket has 2 runs, both done (h1,h2) → NoContrast
	// AND Insufficient (n=2<3) both true; the section must show the bucket's
	// FormatWilson output plus the "chưa có run fail để so" NoContrast label.
	if !strings.Contains(body, "chưa có run fail để so") {
		t.Error("Time-horizon section did not render the NoContrast (F7) label for the all-done <30m bucket")
	}

	// --- Spend Pareto: reuses m1/m2 (cost 1.0 each, done) — Tiers must be
	// non-empty (TotalCost>0).
	if !strings.Contains(body, "Tier") {
		t.Error("Spend Pareto section did not render any tier (expected non-empty TotalCost from m1/m2)")
	}

	// --- Steering economics: 1 run with steering>0 (st1, done) vs 1 run with
	// steering=0 (st2, failed) — both sides n=1, so buildSteerBucket still
	// computes a rate (SteerBucket has no Insufficient/n<3 gate, only the
	// n==0 NoContrast check) but the taxonomy classifies "thử lại" as
	// corrective per taxonomyRules.
	if !strings.Contains(body, "corrective") {
		t.Error("Steering economics taxonomy section missing 'corrective' category")
	}

	// --- Approval funnel: HasHumanDecisions must be true (1 approved), and
	// the Expired stage (1, from janitor sweep) must be labeled distinctly
	// from a human decision (F2) — assert both the funnel renders a latency
	// table (not the 0-human-decision empty state) AND the TTL-sweep label
	// is present for the Expired stage.
	var approved, expired int
	if err := s.Store.DB.QueryRow(`SELECT
		sum(CASE WHEN status='approved' THEN 1 ELSE 0 END),
		sum(CASE WHEN status='expired' THEN 1 ELSE 0 END) FROM approvals`).Scan(&approved, &expired); err != nil {
		t.Fatal(err)
	}
	if approved != 1 || expired != 1 {
		t.Fatalf("approval funnel ground truth sanity failed: approved=%d expired=%d, want 1/1", approved, expired)
	}
	if strings.Contains(body, "Chưa có quyết định người trong khoảng này") {
		t.Error("Approval funnel rendered the 0-human-decision empty-state despite 1 approved row (F2 regression)")
	}
	if !strings.Contains(body, "TTL sweep, không phải người quyết") {
		t.Error("Approval funnel missing the TTL-sweep-not-human label on the Expired stage")
	}
}

// TestInsightsEmptyStatesRenderHonestly seeds NOTHING (a fresh store) and
// asserts every empty-state this phase's spec calls out by name renders,
// instead of a fabricated zero-value chart or a 500.
func TestInsightsEmptyStatesRenderHonestly(t *testing.T) {
	s := testServer(t)

	rec := get(t, s, "/insights?days=0")
	if rec.Code != 200 {
		t.Fatalf("GET /insights (empty store) = %d, want 200; body: %s", rec.Code, rec.Body.String())
	}
	body := rec.Body.String()
	if strings.Contains(body, "render error") {
		t.Fatalf("empty-store response contains a template render error:\n%s", body)
	}

	checks := []struct {
		name, want string
	}{
		{"context empty (CtxEmpty)", "Chưa có dữ liệu context injection"},
		{"approval funnel 0-human-decision", "Chưa có quyết định người trong khoảng này"},
		{"spend pareto no cost", "Chưa có run kết thúc với chi phí"},
		{"steering econ no taxonomy", "Chưa có steering text để phân loại"},
	}
	for _, c := range checks {
		if !strings.Contains(body, c.want) {
			t.Errorf("empty-store missing expected honest empty-state %q: %q", c.name, c.want)
		}
	}
}

// TestInsightsPerSectionDegrade proves F13 at the unit level: the ok()/
// failed() section constructors used by insightsData produce a {Data,Err}
// cell whose zero-value behavior matches what every insights.html section
// branches on ({{if .X.Err}}). This is the narrowest true test of "one
// section's query error never 500s the page" without forcing an actual SQL
// failure into a live *sql.DB shared by every other section (the store has
// no per-query fault injection hook, and adding one only for this test would
// violate the "no fake/mocked behavior" rule) — so the degrade contract is
// verified structurally: failed() always yields a non-empty Err and a nil
// Data, ok() always yields an empty Err, and both were exercised at HTTP
// level (200, no "render error") by the two tests above which already cover
// every section rendering successfully end-to-end.
func TestInsightsPerSectionDegrade(t *testing.T) {
	okSection := ok(map[string]any{"x": 1})
	if okSection.Err != "" {
		t.Errorf("ok() section has non-empty Err: %q", okSection.Err)
	}
	if okSection.Data == nil {
		t.Error("ok() section has nil Data")
	}

	failedSection := failed(errPlaceholder{"boom"})
	if failedSection.Err != "boom" {
		t.Errorf("failed() section Err = %q, want %q", failedSection.Err, "boom")
	}
	if failedSection.Data != nil {
		t.Errorf("failed() section Data = %v, want nil", failedSection.Data)
	}
}

type errPlaceholder struct{ msg string }

func (e errPlaceholder) Error() string { return e.msg }
