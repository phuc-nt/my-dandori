package ingest

import (
	"crypto/ed25519"
	"encoding/base64"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/phuc-nt/dandori/internal/govern"
	"github.com/phuc-nt/dandori/internal/store"
)

// guardrailBatch builds one guardrail-decision record for run runID.
func guardrailBatch(runID, ulid, action, reason string) Batch {
	return Batch{Records: []Record{{
		Type: "event", SessionID: runID, AgentName: "agent", Project: "proj",
		ULID: ulid, Kind: action, Tool: "Bash", Payload: reason, ClientTS: store.Now(),
		Action: action, Machine: "dev-mac", SnapshotFetchedAt: store.Now(),
	}}}
}

// TestCentralGuardrailDenyCreatesAtomicAuditRow proves the happy path: a
// central Deny/Ask guardrail record produces exactly one audit_log row,
// co-signed when a signing key is configured, actor = the AUTHENTICATED
// operator (never any client-supplied field), same batch/tx as the event.
func TestCentralGuardrailDenyCreatesAtomicAuditRow(t *testing.T) {
	s, st := testServer(t)
	_, priv, err := ed25519.GenerateKey(nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Setenv("DANDORI_AUDIT_SIGNING_KEY", base64.StdEncoding.EncodeToString(priv))
	// Signing turns on for this test, which triggers AppendTx's
	// first-signed-row checkpoint write — point it at a per-test temp dir so
	// it never lands under the process CWD's relative default (docs/audit-checkpoints).
	t.Setenv("DANDORI_AUDIT_CHECKPOINT_DIR", t.TempDir())

	token := seedOperatorToken(t, s, "alice", "laptop")
	batch := guardrailBatch("run-a", "g-1", govern.ActionGuardrailBlock, "[dandori G1] blocked: no nukes")
	w := postBatch(t, s.Handler(), token, batch)
	if w.Code != http.StatusOK {
		t.Fatalf("post: %d %s", w.Code, w.Body)
	}

	var n int
	var actor, action, subject string
	var sig []byte
	if err := st.DB.QueryRow(`SELECT count(*) FROM audit_log WHERE subject = 'run-a'`).Scan(&n); err != nil {
		t.Fatal(err)
	}
	if n != 1 {
		t.Fatalf("audit rows for run-a: %d, want 1", n)
	}
	if err := st.DB.QueryRow(`SELECT actor, action, subject, signature FROM audit_log WHERE subject='run-a'`).
		Scan(&actor, &action, &subject, &sig); err != nil {
		t.Fatal(err)
	}
	if actor != "alice" {
		t.Errorf("actor = %q, want the authenticated operator %q, not any client-supplied field", actor, "alice")
	}
	if action != govern.ActionGuardrailBlock {
		t.Errorf("action = %q, want %q", action, govern.ActionGuardrailBlock)
	}
	if len(sig) == 0 {
		t.Error("audit row not co-signed despite a signing key being configured")
	}

	// Same batch tx: the event row for this decision exists too.
	var events int
	st.DB.QueryRow(`SELECT count(*) FROM events WHERE run_id='run-a' AND kind=?`, govern.ActionGuardrailBlock).Scan(&events)
	if events != 1 {
		t.Errorf("guardrail event rows: %d, want 1", events)
	}
}

// TestCentralGuardrailDenyDetailIsStructured proves machine/snapshot metadata
// land in detail as parseable structured data, not raw string concatenation
// that could collide field boundaries.
func TestCentralGuardrailDenyDetailIsStructured(t *testing.T) {
	s, st := testServer(t)
	token := seedOperatorToken(t, s, "alice", "laptop")
	batch := guardrailBatch("run-b", "g-2", govern.ActionPermissionAsk, "needs approval")
	if w := postBatch(t, s.Handler(), token, batch); w.Code != http.StatusOK {
		t.Fatalf("post: %d %s", w.Code, w.Body)
	}
	var detail string
	st.DB.QueryRow(`SELECT detail FROM audit_log WHERE subject='run-b'`).Scan(&detail)
	if detail == "" {
		t.Fatal("empty detail")
	}
	for _, want := range []string{`"machine":"dev-mac"`, `"tool":"Bash"`, `"verdict":"ask"`} {
		if !strings.Contains(detail, want) {
			t.Errorf("detail missing %s: %s", want, detail)
		}
	}
}

// TestCentralGuardrailCrashMidBatchRollsBackBoth proves atomicity: when the
// batch transaction fails to commit, NEITHER the event row NOR the audit row
// is visible afterward — applyGuardrailAuditTx runs inside applyBatch's one
// tx (govern.AppendTx, no second Begin), so a rollback undoes both together.
// The failure is forced deterministically by closing the write connection
// right after resolving the operator (auth already done), so the batch's own
// tx.Begin/Exec calls fail and defer tx.Rollback() fires.
func TestCentralGuardrailCrashMidBatchRollsBackBoth(t *testing.T) {
	s, st := testServer(t)
	operatorID, ok := s.authenticate("secret-token")
	if !ok {
		t.Fatal("legacy token failed to authenticate")
	}

	if err := st.DB.Close(); err != nil {
		t.Fatal(err)
	}

	batch := guardrailBatch("run-c", "g-3", govern.ActionGuardrailBlock, "blocked")
	if _, err := s.applyBatch(operatorID, batch.Records); err == nil {
		t.Fatal("expected applyBatch to error once the write connection is closed")
	}

	// Reopen a fresh connection to the SAME file to inspect post-crash state
	// (the original *sql.DB is closed, but the on-disk file is untouched by
	// the failed/rolled-back transaction).
	st2, err := store.Open(st.Path)
	if err != nil {
		t.Fatal(err)
	}
	defer st2.Close()
	var events, audits int
	st2.DB.QueryRow(`SELECT count(*) FROM events WHERE run_id='run-c'`).Scan(&events)
	st2.DB.QueryRow(`SELECT count(*) FROM audit_log WHERE subject='run-c'`).Scan(&audits)
	if events != 0 || audits != 0 {
		t.Errorf("post-crash state: events=%d audit_log=%d, want 0/0 (no event-without-audit split)", events, audits)
	}
}

// TestCentralGuardrailRunOwnerSpoofRejected proves anti-spoof: operator B
// cannot get an audit row written against a run owned by operator A just by
// POSTing a guardrail-decision record naming that run_id.
func TestCentralGuardrailRunOwnerSpoofRejected(t *testing.T) {
	s, st := testServer(t)
	tokenA := seedOperatorToken(t, s, "alice", "laptop")
	tokenB := seedOperatorToken(t, s, "bob", "laptop")

	// Alice's own run/event establishes ownership.
	own := guardrailBatch("run-owned-by-alice", "g-own", govern.ActionGuardrailBlock, "alice's own deny")
	if w := postBatch(t, s.Handler(), tokenA, own); w.Code != http.StatusOK {
		t.Fatalf("seed alice's run: %d %s", w.Code, w.Body)
	}
	var before int
	st.DB.QueryRow(`SELECT count(*) FROM audit_log WHERE subject='run-owned-by-alice'`).Scan(&before)

	// Bob spools a guardrail decision claiming the SAME run id.
	spoof := guardrailBatch("run-owned-by-alice", "g-spoof", govern.ActionGuardrailBlock, "bob's fabricated deny")
	w := postBatch(t, s.Handler(), tokenB, spoof)
	if w.Code != http.StatusOK {
		t.Fatalf("spoof post: %d %s", w.Code, w.Body)
	}

	var after int
	st.DB.QueryRow(`SELECT count(*) FROM audit_log WHERE subject='run-owned-by-alice'`).Scan(&after)
	if after != before {
		t.Errorf("audit rows for alice's run: before=%d after=%d — bob's spoofed record must be rejected, not appended",
			before, after)
	}
	// The run's operator must still be alice — bob's batch must not have
	// stolen ownership via ensureRunTx's COALESCE (which only fills NULL).
	var owner string
	st.DB.QueryRow(`SELECT COALESCE(operator_id,'') FROM runs WHERE id='run-owned-by-alice'`).Scan(&owner)
	if owner != "alice" {
		t.Errorf("run owner = %q, want alice (must not be reassigned by bob's request)", owner)
	}
}

// TestCentralGuardrailActionWhitelistRejectsUnknown proves a client cannot
// get an arbitrary action string written into audit_log.action — only
// govern.AuditActionSet members are honored.
func TestCentralGuardrailActionWhitelistRejectsUnknown(t *testing.T) {
	s, st := testServer(t)
	token := seedOperatorToken(t, s, "alice", "laptop")
	batch := Batch{Records: []Record{{
		Type: "event", SessionID: "run-bad-action", AgentName: "agent",
		ULID: "g-bad", Kind: "totally_made_up", Tool: "Bash", Payload: "x", ClientTS: store.Now(),
		Action: "grant_admin_access", // not in govern.AuditActionSet
	}}}
	w := postBatch(t, s.Handler(), token, batch)
	if w.Code != http.StatusOK {
		t.Fatalf("post: %d %s", w.Code, w.Body)
	}
	var n int
	st.DB.QueryRow(`SELECT count(*) FROM audit_log WHERE subject='run-bad-action'`).Scan(&n)
	if n != 0 {
		t.Errorf("audit rows for unrecognized action: %d, want 0", n)
	}
	var actionInDB string
	err := st.DB.QueryRow(`SELECT action FROM audit_log WHERE action='grant_admin_access'`).Scan(&actionInDB)
	if err == nil {
		t.Errorf("client-supplied action %q was written verbatim into audit_log — whitelist not enforced", actionInDB)
	}
}

// TestCentralGuardrailULIDSuppressionStillAudits is THE anti-suppression
// test: a client precomputes a ULID, POSTs a benign event under it first,
// then POSTs the REAL deny under the SAME ULID. The deny must still be
// audited — dedup is server-derived (run+action+detail hash), not the
// client-minted ULID, so the events.ulid ON CONFLICT DO NOTHING cannot be
// used to make a real deny disappear.
func TestCentralGuardrailULIDSuppressionStillAudits(t *testing.T) {
	s, st := testServer(t)
	token := seedOperatorToken(t, s, "alice", "laptop")
	sharedULID := "precomputed-ulid-x"

	benign := Batch{Records: []Record{{
		Type: "event", SessionID: "run-suppress", AgentName: "agent",
		ULID: sharedULID, Kind: "tool_use", Tool: "Read", Payload: "benign read", ClientTS: store.Now(),
	}}}
	if w := postBatch(t, s.Handler(), token, benign); w.Code != http.StatusOK {
		t.Fatalf("benign post: %d %s", w.Code, w.Body)
	}

	realDeny := Batch{Records: []Record{{
		Type: "event", SessionID: "run-suppress", AgentName: "agent",
		ULID: sharedULID, Kind: govern.ActionGuardrailBlock, Tool: "Bash", Payload: "rm -rf /", ClientTS: store.Now(),
		Action: govern.ActionGuardrailBlock, Machine: "dev-mac", SnapshotFetchedAt: store.Now(),
	}}}
	if w := postBatch(t, s.Handler(), token, realDeny); w.Code != http.StatusOK {
		t.Fatalf("deny post: %d %s", w.Code, w.Body)
	}

	var n int
	st.DB.QueryRow(`SELECT count(*) FROM audit_log WHERE subject='run-suppress'`).Scan(&n)
	if n != 1 {
		t.Errorf("audit rows for run-suppress after ULID-suppression attempt: %d, want 1 (deny must not be dropped)", n)
	}
}

// TestCentralGuardrailCoverageDetectorFlagsMissingAuditEvent proves the
// coverage-gap half of the detector: a tool_use matching one of the
// migration-002 default block rules (rm -rf /), posted with NO Action set
// (i.e. no guardrail-decision event alongside it), gets an open review flag.
// This is the signature of a central client evaluating against a stale or
// tampered policy cache instead of the real snapshot.
func TestCentralGuardrailCoverageDetectorFlagsMissingAuditEvent(t *testing.T) {
	s, st := testServer(t)
	token := seedOperatorToken(t, s, "alice", "laptop")
	batch := Batch{Records: []Record{{
		Type: "event", SessionID: "run-gap", AgentName: "agent",
		ULID: "g-gap", Kind: "tool_use", Tool: "Bash", Payload: `{"command":"rm -rf /tmp/x"}`, ClientTS: store.Now(),
		// Action deliberately left empty: the client reported the tool_use
		// but never reported a guardrail decision for it.
	}}}
	if w := postBatch(t, s.Handler(), token, batch); w.Code != http.StatusOK {
		t.Fatalf("post: %d %s", w.Code, w.Body)
	}
	var n int
	st.DB.QueryRow(`SELECT count(*) FROM flags WHERE run_id='run-gap' AND status='open' AND reason LIKE 'central coverage gap%'`).Scan(&n)
	if n != 1 {
		t.Errorf("coverage-gap flags: %d, want 1", n)
	}
	// Flag-only: the detector must never touch run status or band.
	var status string
	st.DB.QueryRow(`SELECT status FROM runs WHERE id='run-gap'`).Scan(&status)
	if status == "killed" {
		t.Error("coverage detector must never auto-kill/block a run — flag-only contract violated")
	}
}

// TestCentralGuardrailCoverageDetectorIgnoresCleanRuns proves the detector
// does NOT flag runs that either (a) never touched a block-rule pattern, or
// (b) matched a block rule but DID get a guardrail-decision event recorded —
// the coverage gap is specifically "matched a block rule with NO decision on
// record," not "matched a block rule" alone.
func TestCentralGuardrailCoverageDetectorIgnoresCleanRuns(t *testing.T) {
	s, st := testServer(t)
	token := seedOperatorToken(t, s, "alice", "laptop")

	// (a) benign tool_use, no block-rule match at all.
	clean := Batch{Records: []Record{{
		Type: "event", SessionID: "run-clean", AgentName: "agent",
		ULID: "g-clean", Kind: "tool_use", Tool: "Bash", Payload: `{"command":"go test ./..."}`, ClientTS: store.Now(),
	}}}
	if w := postBatch(t, s.Handler(), token, clean); w.Code != http.StatusOK {
		t.Fatalf("clean post: %d %s", w.Code, w.Body)
	}

	// (b) matches a block rule, but a guardrail-decision event for the SAME
	// run already exists — covered, must not be flagged as a gap.
	covered := Batch{Records: []Record{
		{Type: "event", SessionID: "run-covered", AgentName: "agent",
			ULID: "g-covered-decision", Kind: govern.ActionGuardrailBlock, Tool: "Bash", Payload: "blocked earlier", ClientTS: store.Now(),
			Action: govern.ActionGuardrailBlock, Machine: "dev-mac", SnapshotFetchedAt: store.Now()},
		{Type: "event", SessionID: "run-covered", AgentName: "agent",
			ULID: "g-covered-toolonly", Kind: "tool_use", Tool: "Bash", Payload: `{"command":"rm -rf /tmp/y"}`, ClientTS: store.Now()},
	}}
	if w := postBatch(t, s.Handler(), token, covered); w.Code != http.StatusOK {
		t.Fatalf("covered post: %d %s", w.Code, w.Body)
	}

	var n int
	st.DB.QueryRow(`SELECT count(*) FROM flags WHERE run_id IN ('run-clean','run-covered') AND status='open' AND reason LIKE 'central coverage gap%'`).Scan(&n)
	if n != 0 {
		t.Errorf("coverage-gap flags for clean/covered runs: %d, want 0", n)
	}
}

// TestCentralGuardrailFreshnessDetectorFlagsStaleSnapshot proves the
// freshness half of the detector: a guardrail decision echoing a
// SnapshotFetchedAt far in the past gets a review flag, not a block.
func TestCentralGuardrailFreshnessDetectorFlagsStaleSnapshot(t *testing.T) {
	s, st := testServer(t)
	token := seedOperatorToken(t, s, "alice", "laptop")
	stale := time.Now().Add(-2 * time.Hour).UTC().Format(time.RFC3339)
	batch := Batch{Records: []Record{{
		Type: "event", SessionID: "run-stale", AgentName: "agent",
		ULID: "g-stale", Kind: govern.ActionGuardrailBlock, Tool: "Bash", Payload: "blocked", ClientTS: store.Now(),
		Action: govern.ActionGuardrailBlock, Machine: "dev-mac", SnapshotFetchedAt: stale,
	}}}
	if w := postBatch(t, s.Handler(), token, batch); w.Code != http.StatusOK {
		t.Fatalf("post: %d %s", w.Code, w.Body)
	}
	var n int
	st.DB.QueryRow(`SELECT count(*) FROM flags WHERE run_id='run-stale' AND status='open' AND reason LIKE 'central stale snapshot%'`).Scan(&n)
	if n != 1 {
		t.Errorf("stale-snapshot flags: %d, want 1", n)
	}
	// Flag-only: run status/band must be untouched by the detector.
	var status string
	st.DB.QueryRow(`SELECT status FROM runs WHERE id='run-stale'`).Scan(&status)
	if status == "killed" {
		t.Error("detector must never auto-kill/block a run — flag-only contract violated")
	}
}
