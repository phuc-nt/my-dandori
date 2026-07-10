package govern

import (
	"crypto/ed25519"
	"encoding/base64"
	"strings"
	"testing"

	"github.com/phuc-nt/dandori/internal/store"
)

// seedExportRun inserts the minimal agents+runs rows a foreign-key-enforced
// events/flags/audit_log row needs to reference.
func seedExportRun(t *testing.T, st *store.Store, runID string) {
	t.Helper()
	if _, err := st.DB.Exec(`INSERT INTO agents(id, name, created_at) VALUES('agent1','agent1',?)
		ON CONFLICT(name) DO NOTHING`, store.Now()); err != nil {
		t.Fatalf("seed agent: %v", err)
	}
	if _, err := st.DB.Exec(`INSERT INTO runs(id, session_id, agent_id, project, cwd, started_at, status)
		VALUES(?, ?, 'agent1', 'proj', '/work/proj', ?, 'ended')`, runID, runID, store.Now()); err != nil {
		t.Fatalf("seed run %s: %v", runID, err)
	}
}

// TestBuildComplianceBundleIncludesCentralAuditWithSignatureAndCheckpoint:
// after a central-mode guardrail decision is audited (signed, since a key is
// configured), the export must include that audit row with its signed flag
// set, the latest checkpoint, and Verify must report intact.
func TestBuildComplianceBundleIncludesCentralAuditWithSignatureAndCheckpoint(t *testing.T) {
	privB64, _ := genTestKey(t)
	t.Setenv(signingKeyEnv, privB64)
	st := openTestStore(t)
	seedExportRun(t, st, "run-central-1")

	a := &Audit{St: st, Actor: "server"}
	detail := `{"tool":"Bash","verdict":"deny","reason":"blocked","machine":"ci-runner-1","snapshot_fetched_at":"2026-01-01T00:00:00Z"}`
	if _, err := a.Append(ActionGuardrailBlock, "run-central-1", detail); err != nil {
		t.Fatalf("append central audit row: %v", err)
	}

	b, err := BuildComplianceBundle(st, "auditor")
	if err != nil {
		t.Fatalf("BuildComplianceBundle: %v", err)
	}
	if !b.Verify.OK || b.Verify.Reason != "" {
		t.Errorf("Verify = %+v, want OK with empty reason", b.Verify)
	}

	var found *AuditEntry
	for i := range b.AuditLog {
		if b.AuditLog[i].Subject == "run-central-1" {
			found = &b.AuditLog[i]
		}
	}
	if found == nil {
		t.Fatal("central audit row not found in exported AuditLog")
	}
	if !found.Signed {
		t.Error("central audit row Signed = false, want true (signing key was configured)")
	}
	if found.KeyID != DefaultKeyID {
		t.Errorf("central audit row KeyID = %d, want %d", found.KeyID, DefaultKeyID)
	}
	if found.Machine != "ci-runner-1" {
		t.Errorf("central audit row Machine = %q, want ci-runner-1 (attribution parsed from detail)", found.Machine)
	}
	if b.SignedCount != 1 || b.UnsignedCount != 0 {
		t.Errorf("SignedCount/UnsignedCount = %d/%d, want 1/0", b.SignedCount, b.UnsignedCount)
	}
	if b.Checkpoint == nil {
		t.Fatal("bundle Checkpoint is nil, want the latest signed checkpoint anchored at export time")
	}
}

// TestBuildComplianceBundleReportsSignedUnsignedCutover: an install that
// appended rows before AND after enabling signing has both counts non-zero —
// cutover honesty, not a false claim that everything is signed.
func TestBuildComplianceBundleReportsSignedUnsignedCutover(t *testing.T) {
	st := openTestStore(t)
	a := &Audit{St: st, Actor: "tester"}
	if _, err := a.Append("band_change", "run-x", "unsigned era"); err != nil {
		t.Fatalf("append unsigned: %v", err)
	}

	privB64, _ := genTestKey(t)
	t.Setenv(signingKeyEnv, privB64)
	if _, err := a.Append("band_change", "run-x", "signed era"); err != nil {
		t.Fatalf("append signed: %v", err)
	}

	b, err := BuildComplianceBundle(st, "auditor")
	if err != nil {
		t.Fatalf("BuildComplianceBundle: %v", err)
	}
	if b.SignedCount != 1 {
		t.Errorf("SignedCount = %d, want 1", b.SignedCount)
	}
	// UnsignedCount includes the compliance_export row Verify's monotonic
	// check would flag as unsigned-after-signing, but that row is appended
	// by THIS call and signed too since a key is configured — the pre-cutover
	// row is the only unsigned one.
	if b.UnsignedCount != 1 {
		t.Errorf("UnsignedCount = %d, want 1", b.UnsignedCount)
	}
}

// TestCoverageFlagsMissingAuditNotCleanRun is THE anti-false-positive test
// for finding O: a run with a guardrail-decision EVENT but no matching audit
// row must appear in Coverage.MissingAudit, while a CLEAN run (no guardrail
// decision at all — the common case) must NOT appear anywhere in Coverage.
func TestCoverageFlagsMissingAuditNotCleanRun(t *testing.T) {
	st := openTestStore(t)
	seedExportRun(t, st, "run-missing-audit")
	seedExportRun(t, st, "run-clean")

	// run-missing-audit: a guardrail_block EVENT was recorded (as engine.go's
	// record() would write) but — simulating a dropped/failed audit append —
	// no corresponding audit_log row exists.
	if _, err := st.DB.Exec(`INSERT INTO events(run_id, ts, kind, tool_name, ok, payload)
		VALUES(?, ?, ?, 'Bash', 0, 'blocked')`, "run-missing-audit", store.Now(), ActionGuardrailBlock); err != nil {
		t.Fatalf("seed guardrail event: %v", err)
	}

	// run-clean: ordinary tool_use events, no guardrail decision ever
	// happened. This must NOT be flagged — that is the false positive finding
	// O closes.
	if _, err := st.DB.Exec(`INSERT INTO events(run_id, ts, kind, tool_name, ok, payload)
		VALUES(?, ?, 'tool_use', 'Read', 1, '')`, "run-clean", store.Now()); err != nil {
		t.Fatalf("seed clean event: %v", err)
	}

	b, err := BuildComplianceBundle(st, "auditor")
	if err != nil {
		t.Fatalf("BuildComplianceBundle: %v", err)
	}

	foundMissing := false
	for _, m := range b.Coverage.MissingAudit {
		if m.RunID == "run-missing-audit" {
			foundMissing = true
		}
		if m.RunID == "run-clean" {
			t.Errorf("clean run run-clean appeared in Coverage.MissingAudit — false positive (finding O)")
		}
	}
	if !foundMissing {
		t.Error("run-missing-audit (guardrail event with no audit row) did not appear in Coverage.MissingAudit")
	}
}

// TestCoverageDoesNotFlagAuditedLocalDecisions guards against a false-positive
// class: engine.record() collapses every local deny action into events.kind
// "guardrail_block" (and every gate into "permission_ask") while the audit row
// keeps the granular action (kill_block/secrets_block/budget_block/...). A
// coverage check that matched audit_log.action to events.kind would report a
// gap for every one of these fully-audited runs. Each row below is a real
// local decision: a collapsed event kind PLUS a granular audit row. None must
// appear in MissingAudit.
func TestCoverageDoesNotFlagAuditedLocalDecisions(t *testing.T) {
	st := openTestStore(t)
	a := &Audit{St: st, Actor: "engine"}
	cases := []struct {
		runID     string
		eventKind string // what engine.record() writes to events.kind
		action    string // the granular audit_log.action for the same decision
	}{
		{"run-kill", ActionGuardrailBlock, ActionKillBlock},
		{"run-secrets", ActionGuardrailBlock, ActionSecretsBlock},
		{"run-budget", ActionGuardrailBlock, ActionBudgetBlock},
		{"run-sandbox", ActionGuardrailBlock, "sandbox_block"},
		{"run-gate", ActionPermissionAsk, "gate_decision"},
	}
	for _, c := range cases {
		seedExportRun(t, st, c.runID)
		if _, err := st.DB.Exec(`INSERT INTO events(run_id, ts, kind, tool_name, ok, payload)
			VALUES(?, ?, ?, 'Bash', 0, 'decided')`, c.runID, store.Now(), c.eventKind); err != nil {
			t.Fatalf("seed event for %s: %v", c.runID, err)
		}
		if _, err := a.Append(c.action, c.runID, "decided"); err != nil {
			t.Fatalf("seed audit for %s: %v", c.runID, err)
		}
	}

	b, err := BuildComplianceBundle(st, "auditor")
	if err != nil {
		t.Fatalf("BuildComplianceBundle: %v", err)
	}
	for _, m := range b.Coverage.MissingAudit {
		for _, c := range cases {
			if m.RunID == c.runID {
				t.Errorf("audited local decision %s (event kind %s, audit action %s) was falsely flagged as a missing-audit gap",
					c.runID, c.eventKind, c.action)
			}
		}
	}
}

// TestCoverageSurfacesDetectorFlags: the P4 ingest detector's coverage-gap /
// stale-snapshot flags (identified by their reason prefix) are surfaced
// verbatim in Coverage.DetectorFlags, without the export recomputing the
// detection logic itself.
func TestCoverageSurfacesDetectorFlags(t *testing.T) {
	st := openTestStore(t)
	seedExportRun(t, st, "run-flagged")

	if _, err := st.DB.Exec(`INSERT INTO flags(run_id, reason, status, created_at) VALUES(?, ?, 'open', ?)`,
		"run-flagged", "central coverage gap: run run-flagged tool_use (Bash) matched an enabled block rule but no guardrail-decision event was recorded", store.Now()); err != nil {
		t.Fatalf("seed detector flag: %v", err)
	}
	// A non-detector flag (different reason prefix, e.g. a Jira-linked manual
	// review flag) must NOT be pulled into Coverage — Coverage is only the
	// detector's own signal, not every flag in the table.
	if _, err := st.DB.Exec(`INSERT INTO flags(run_id, reason, status, created_at) VALUES(?, ?, 'open', ?)`,
		"run-flagged", "manual review requested", store.Now()); err != nil {
		t.Fatalf("seed unrelated flag: %v", err)
	}

	b, err := BuildComplianceBundle(st, "auditor")
	if err != nil {
		t.Fatalf("BuildComplianceBundle: %v", err)
	}
	if len(b.Coverage.DetectorFlags) != 1 {
		t.Fatalf("Coverage.DetectorFlags length = %d, want 1 (only the detector-prefixed flag)", len(b.Coverage.DetectorFlags))
	}
	if !strings.HasPrefix(b.Coverage.DetectorFlags[0].Reason, "central coverage gap") {
		t.Errorf("Coverage.DetectorFlags[0].Reason = %q, want central-coverage-gap prefix", b.Coverage.DetectorFlags[0].Reason)
	}
	// Full flags list still contains both (Coverage is additive, not a filter
	// on the existing Flags field).
	if len(b.Flags) != 2 {
		t.Errorf("b.Flags length = %d, want 2 (Coverage does not remove flags from the general Flags list)", len(b.Flags))
	}
}

// TestBundlePubkeyFingerprintMatchesFingerprintHelper: the bundle's
// PubkeyFingerprint must equal PubkeyFingerprint(pubB64) for the currently
// configured key — the same computation `dandori audit pubkey` uses, so an
// auditor's independently-pinned value is comparable byte-for-byte.
func TestBundlePubkeyFingerprintMatchesFingerprintHelper(t *testing.T) {
	privB64, _ := genTestKey(t)
	t.Setenv(signingKeyEnv, privB64)
	st := openTestStore(t)

	b, err := BuildComplianceBundle(st, "auditor")
	if err != nil {
		t.Fatalf("BuildComplianceBundle: %v", err)
	}
	pubB64, ok := PublicKeyFromSigningKey()
	if !ok {
		t.Fatal("PublicKeyFromSigningKey reported no key configured")
	}
	want, err := PubkeyFingerprint(pubB64)
	if err != nil {
		t.Fatalf("PubkeyFingerprint: %v", err)
	}
	if b.PubkeyFingerprint != want {
		t.Errorf("bundle PubkeyFingerprint = %q, want %q", b.PubkeyFingerprint, want)
	}
	if b.TrustNote == "" {
		t.Error("bundle TrustNote is empty, want the out-of-band verification procedure")
	}
}

// TestFingerprintChangesWithDifferentKey demonstrates the fingerprint is a
// real discriminator (finding N's whole point): re-signing a bundle's
// underlying chain with a DIFFERENT key produces a DIFFERENT fingerprint, so
// an auditor comparing against an out-of-band pinned value would catch a
// forged bundle presenting its own keypair.
func TestFingerprintChangesWithDifferentKey(t *testing.T) {
	privB64A, pubA := genTestKey(t)
	_, privBRaw, err := ed25519.GenerateKey(nil)
	if err != nil {
		t.Fatalf("generate second key: %v", err)
	}
	pubB, ok := privBRaw.Public().(ed25519.PublicKey)
	if !ok {
		t.Fatal("derive public key B")
	}

	fpA, err := PubkeyFingerprint(base64.StdEncoding.EncodeToString(pubA))
	if err != nil {
		t.Fatalf("fingerprint A: %v", err)
	}
	fpB, err := PubkeyFingerprint(base64.StdEncoding.EncodeToString(pubB))
	if err != nil {
		t.Fatalf("fingerprint B: %v", err)
	}
	if fpA == fpB {
		t.Fatal("fingerprints of two different keys are equal — fingerprint is not a discriminator")
	}

	// Sanity: fpA is indeed what a bundle signed with key A would report.
	t.Setenv(signingKeyEnv, privB64A)
	st := openTestStore(t)
	b, err := BuildComplianceBundle(st, "auditor")
	if err != nil {
		t.Fatalf("BuildComplianceBundle: %v", err)
	}
	if b.PubkeyFingerprint != fpA {
		t.Errorf("bundle fingerprint = %q, want %q (key A)", b.PubkeyFingerprint, fpA)
	}
	if b.PubkeyFingerprint == fpB {
		t.Error("bundle fingerprint unexpectedly matches key B — would not be caught by an out-of-band pin")
	}
}

// TestBundleDisclosuresPresent: the chain-order and client-attested caveats
// (findings R and A) must be present in every bundle, not just documented
// externally.
func TestBundleDisclosuresPresent(t *testing.T) {
	st := openTestStore(t)
	b, err := BuildComplianceBundle(st, "auditor")
	if err != nil {
		t.Fatalf("BuildComplianceBundle: %v", err)
	}
	if len(b.Disclosures) < 2 {
		t.Fatalf("Disclosures length = %d, want at least 2 (chain-order + client-attested)", len(b.Disclosures))
	}
	joined := strings.Join(b.Disclosures, " ")
	if !strings.Contains(joined, "receive order") && !strings.Contains(joined, "RECEIVE order") {
		t.Error("Disclosures missing chain-order-vs-timestamp caveat (finding R)")
	}
	if !strings.Contains(joined, "CLIENT-ATTESTED") {
		t.Error("Disclosures missing client-attested caveat (finding A)")
	}
}

// TestMachineFromDetailIgnoresNonCentralDetail: a plain-string audit detail
// (local mode / non-guardrail actions) must not error and must yield an
// empty machine, not a false attribution.
func TestMachineFromDetailIgnoresNonCentralDetail(t *testing.T) {
	if m := machineFromDetail("plain text detail, not JSON"); m != "" {
		t.Errorf("machineFromDetail on plain text = %q, want empty", m)
	}
	if m := machineFromDetail(`{"tool":"Bash","verdict":"deny","reason":"x","machine":"box-7","snapshot_fetched_at":"t"}`); m != "box-7" {
		t.Errorf("machineFromDetail = %q, want box-7", m)
	}
}
