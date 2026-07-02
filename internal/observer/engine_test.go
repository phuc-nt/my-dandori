package observer

import (
	"path/filepath"
	"strconv"
	"sync"
	"testing"

	"github.com/phuc-nt/dandori/internal/config"
	"github.com/phuc-nt/dandori/internal/govern"
	"github.com/phuc-nt/dandori/internal/store"
)

func testStore(t *testing.T) (*store.Store, *config.Config) {
	t.Helper()
	st, err := store.Open(filepath.Join(t.TempDir(), "t.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	cfg := &config.Config{LearnWindowDays: 30, ApprovalTTLMinutes: 30}
	cfg.Budget.GlobalMonthlyUSD = 50
	return st, cfg
}

func seedRun(t *testing.T, st *store.Store, id, agent, status string, cost float64, toolErrs int) {
	t.Helper()
	st.DB.Exec(`INSERT INTO agents(id, name, created_at) VALUES(?, ?, ?) ON CONFLICT(name) DO NOTHING`,
		agent, agent, store.Now())
	if _, err := st.DB.Exec(`INSERT INTO runs(id, session_id, agent_id, project, status, started_at, ended_at, cost_usd)
		VALUES(?, ?, ?, 'proj', ?, ?, ?, ?)`, id, id, agent, status, store.Now(), store.Now(), cost); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < toolErrs; i++ {
		st.DB.Exec(`INSERT INTO events(run_id, ts, kind, tool_name, ok, payload) VALUES(?, ?, 'tool_result', 'Edit', 0, 'err')`,
			id, store.Now())
	}
}

// Budget scenario: month-to-date spend projecting way past the limit must
// yield an approval-class insight; approving it applies the limit ONCE from
// evidence params, audited.
func TestBudgetOvershootProposeApproveApply(t *testing.T) {
	st, cfg := testStore(t)
	// Enough runs+cost that any projection blows the $50 limit.
	for i, id := range []string{"b1", "b2", "b3", "b4", "b5"} {
		seedRun(t, st, id, "agent-a", "done", 40+float64(i), 0)
	}
	res, err := RunObserver(st, cfg)
	if err != nil {
		t.Fatal(err)
	}
	if res.Proposed < 1 {
		t.Fatalf("no budget proposal: %+v", res)
	}
	// The action string carries only the insight id — params live in evidence.
	var action string
	var insightID int64
	st.DB.QueryRow(`SELECT action FROM approvals WHERE action LIKE 'observer:budget:%'`).Scan(&action)
	st.DB.QueryRow(`SELECT id FROM insights WHERE type='budget_overshoot_trend'`).Scan(&insightID)
	if action == "" || insightID == 0 {
		t.Fatalf("missing approval/insight: action=%q insight=%d", action, insightID)
	}
	// No budget mutated before approval (no-bypass).
	var budgets int
	st.DB.QueryRow(`SELECT count(*) FROM budgets WHERE scope_type='global'`).Scan(&budgets)
	if budgets != 0 {
		t.Fatal("budget mutated before human approval")
	}
	var approvalID int64
	st.DB.QueryRow(`SELECT id FROM approvals WHERE action = ?`, action).Scan(&approvalID)
	if _, err := govern.Decide(st, approvalID, true, "phucnt", "ok"); err != nil {
		t.Fatal(err)
	}
	res2, err := RunObserver(st, cfg)
	if err != nil {
		t.Fatal(err)
	}
	if res2.Applied != 1 {
		t.Fatalf("applied: %d", res2.Applied)
	}
	var limit float64
	st.DB.QueryRow(`SELECT limit_usd FROM budgets WHERE scope_type='global'`).Scan(&limit)
	if limit <= 50 {
		t.Errorf("limit not raised: %v", limit)
	}
	var status string
	st.DB.QueryRow(`SELECT status FROM insights WHERE id = ?`, insightID).Scan(&status)
	if status != "resolved" {
		t.Errorf("insight status: %s", status)
	}
	var audits int
	st.DB.QueryRow(`SELECT count(*) FROM audit_log WHERE action LIKE 'observer%'`).Scan(&audits)
	if audits < 2 {
		t.Errorf("observer audits: %d", audits)
	}
}

func TestObserverDedupAndAutoIsInternalOnly(t *testing.T) {
	st, cfg := testStore(t)
	// A-grade fleet with one clean underused agent → auto insights only.
	for i := 0; i < 20; i++ {
		seedRun(t, st, "m"+itoa(i), "workhorse", "done", 0.1, 0)
	}
	seedRun(t, st, "u1", "gem", "done", 0.1, 0)

	before := governanceFingerprint(t, st)
	res, err := RunObserver(st, cfg)
	if err != nil {
		t.Fatal(err)
	}
	if res.Surfaced == 0 {
		t.Fatal("expected at least one auto insight (underused or playbook candidate)")
	}
	if res.Proposed != 0 {
		t.Fatalf("auto scenario proposed approvals: %+v", res.Details)
	}
	after := governanceFingerprint(t, st)
	if before != after {
		t.Errorf("auto path changed governance state: %s → %s", before, after)
	}
	// Second cycle: everything dedups, nothing new.
	res2, err := RunObserver(st, cfg)
	if err != nil {
		t.Fatal(err)
	}
	if res2.Surfaced != 0 || res2.Deduped == 0 {
		t.Errorf("dedup failed: %+v", res2)
	}
}

// governanceFingerprint hashes the mutable GOVERN state the observer must
// never touch without approval.
func governanceFingerprint(t *testing.T, st *store.Store) string {
	t.Helper()
	var budgets, bands, rules, kills int
	st.DB.QueryRow(`SELECT count(*) FROM budgets`).Scan(&budgets)
	st.DB.QueryRow(`SELECT count(*) FROM agent_bands`).Scan(&bands)
	st.DB.QueryRow(`SELECT count(*) FROM guardrail_rules`).Scan(&rules)
	st.DB.QueryRow(`SELECT count(*) FROM runs WHERE status='killed'`).Scan(&kills)
	return itoa(budgets) + "/" + itoa(bands) + "/" + itoa(rules) + "/" + itoa(kills)
}

func itoa(n int) string { return strconv.Itoa(n) }

// Two workers racing on the same approved action must apply it exactly once.
func TestObserverApplierConsumeOnceRace(t *testing.T) {
	st, cfg := testStore(t)
	_ = cfg
	st.DB.Exec(`INSERT INTO insights(type, subject, summary, evidence, class, surface, created_at)
		VALUES('budget_overshoot_trend','global','s','{"scope_type":"global","scope_id":"","suggested_limit":80}','approval','ceo', ?)`, store.Now())
	st.DB.Exec(`INSERT INTO approvals(run_id, action, reason, requested_at, status, decided_by)
		VALUES(NULL, 'observer:budget:1', 'r', ?, 'approved', 'phucnt')`, store.Now())

	var wg sync.WaitGroup
	applied := make([]int, 2)
	for i := 0; i < 2; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			n, _ := RunObserverApplier(st)
			applied[i] = n
		}(i)
	}
	wg.Wait()
	if applied[0]+applied[1] != 1 {
		t.Errorf("applied %d+%d, want exactly 1", applied[0], applied[1])
	}
}

// Observer approvals are review-queue items — the gate TTL must spare them.
func TestObserverApprovalSurvivesTTL(t *testing.T) {
	st, cfg := testStore(t)
	old := "2026-01-01T00:00:00Z"
	st.DB.Exec(`INSERT INTO approvals(run_id, action, reason, requested_at) VALUES(NULL, 'observer:budget:9', 'r', ?)`, old)
	st.DB.Exec(`INSERT INTO approvals(run_id, action, reason, requested_at) VALUES(NULL, 'git push', 'gate', ?)`, old)

	govern.NewEngine(cfg, st).ExpireStale()

	var obs, gate string
	st.DB.QueryRow(`SELECT status FROM approvals WHERE action LIKE 'observer:%'`).Scan(&obs)
	st.DB.QueryRow(`SELECT status FROM approvals WHERE action = 'git push'`).Scan(&gate)
	if obs != "pending" || gate != "expired" {
		t.Errorf("observer=%s gate=%s, want pending/expired", obs, gate)
	}
}

// Malformed observer actions are consumed + audited, never retried forever.
func TestObserverApplierMalformed(t *testing.T) {
	st, _ := testStore(t)
	st.DB.Exec(`INSERT INTO approvals(run_id, action, reason, requested_at, status, decided_by)
		VALUES(NULL, 'observer:budget:notanumber', 'r', ?, 'approved', 'x')`, store.Now())
	n, err := RunObserverApplier(st)
	if err != nil || n != 0 {
		t.Fatalf("applied=%d err=%v", n, err)
	}
	var consumed int
	st.DB.QueryRow(`SELECT count(*) FROM approvals WHERE consumed_at IS NOT NULL`).Scan(&consumed)
	if consumed != 1 {
		t.Errorf("malformed not consumed: %d", consumed)
	}
	var audits int
	st.DB.QueryRow(`SELECT count(*) FROM audit_log WHERE action='observer_malformed'`).Scan(&audits)
	if audits != 1 {
		t.Errorf("malformed audit: %d", audits)
	}
}
