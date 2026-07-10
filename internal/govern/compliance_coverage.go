package govern

import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/phuc-nt/dandori/internal/store"
)

// guardrailDecisionEventKinds is the set of events.kind values that mark a
// guardrail DECISION on the events table. This deliberately does NOT equal the
// audit_log.action vocabulary: engine.record() collapses every deny action
// (kill_block/secrets_block/budget_block/sandbox_block/...) into the single
// events.kind "guardrail_block", and every gate into "permission_ask", while
// the audit row keeps the granular action. So a decision run is identified by
// these two collapsed event kinds, and its audit coverage is checked by the
// presence of ANY audit row for the run — not by matching action to kind
// (which would falsely flag every non-guardrail_block local decision).
var guardrailDecisionEventKinds = []string{ActionGuardrailBlock, ActionPermissionAsk}

// coverageFlagPrefixes duplicates ingest.coverageFlagPrefix/
// staleSnapshotFlagPrefix as string literals (govern cannot import ingest —
// ingest already imports govern) so the export can filter the shared flags
// table for exactly the detector's rows without recomputing the detection
// logic itself.
var coverageFlagPrefixes = []string{"central coverage gap", "central stale snapshot"}

// CoverageReport is the real coverage signal for central mode: it does NOT
// compute "runs minus audited-runs" (that would flag the majority of clean
// runs — the false positive this type exists to avoid). It reports only
// runs that had SOME guardrail-decision signal (an events row, or a P4
// detector flag) that is missing its expected audit_log counterpart.
type CoverageReport struct {
	// DetectorFlags mirrors the ingest package's coverage-gap and
	// stale-snapshot flags (prefixes "central coverage gap" / "central stale
	// snapshot") — these are the review signals a running fleet already
	// produces for exactly this concern; the export surfaces them rather
	// than recomputing anything.
	DetectorFlags []FlagEntry `json:"detector_flags"`

	// MissingAudit lists runs where a guardrail-decision EVENT exists (the
	// engine/central client recorded a kill/block/secrets/budget/ask
	// decision) but no audit_log row with matching action+subject was ever
	// written — a genuine integrity gap, not a clean run.
	MissingAudit []MissingAuditEntry `json:"missing_audit"`
}

type MissingAuditEntry struct {
	RunID   string `json:"run_id"`
	Kind    string `json:"kind"` // the events.kind that had no matching audit row
	EventTS string `json:"event_ts"`
}

// pubkeyTrustNote is shipped inside the bundle so the verification procedure
// travels with the data. It must never be softened into a claim that the
// pubkey field above is itself trustworthy — see PubkeyFingerprint's doc
// comment: confidentiality (pubkeys are public, fine to expose) is a
// different property from trust-rootedness (a pubkey shipped BY the party
// being audited, IN the bundle being audited, proves nothing about its own
// authenticity — the party could ship a different keypair alongside forged
// data and every check in this bundle would still pass against ITS OWN key).
const pubkeyTrustNote = "PubkeyFingerprint is included for convenience only — it is NOT the trust root. " +
	"This bundle is produced by the same party being audited, so a fingerprint shipped inside it proves nothing " +
	"by itself: an attacker controlling the export could ship a different keypair alongside forged data and every " +
	"signature check would still pass against THAT key. The auditor must independently hold the fingerprint pinned " +
	"at onboarding time (via `dandori audit pubkey`, communicated out-of-band — e.g. read aloud on a call, or " +
	"pinned in a runbook signed separately) and compare it byte-for-byte against this field. A mismatch means the " +
	"export was signed by an untrusted key, regardless of whether every other check in this bundle reports OK."

// buildDisclosures returns the plain-language caveats every compliance
// bundle must carry: what the tamper-evident chain does and does not prove.
func buildDisclosures() []string {
	return []string{
		"Chain order is server-RECEIVE order, not event-timestamp order: a dev machine that was offline can " +
			"replay spooled events later, and those rows append at the current chain head with an OLDER `ts` " +
			"field. The tamper-evident guarantee (hash chain + signatures + checkpoint) covers receipt order — " +
			"do not assume audit_log row id order equals the order events actually happened on the dev machine.",
		"Central-mode audit rows are CLIENT-ATTESTED: the server signs and chains what the dev-machine client " +
			"reports, and the tamper-evident guarantee holds from server-receipt onward. It does NOT prove the " +
			"client never suppressed a decision before it reached the server (e.g. a modified/compromised client " +
			"that simply never spools a deny). The Coverage section's detector flags are the mitigation for this " +
			"gap, not a substitute for it — absence of a coverage flag is not proof of absence of a suppressed " +
			"decision, only evidence no known suppression pattern was detected.",
	}
}

// machineFromDetail parses the central guardrail canonical JSON shape (see
// ingest/guardrail_audit.go's guardrailDetail) out of an audit row's detail
// column, so the export surfaces which fleet machine each central decision
// came from without the auditor needing to hand-parse JSON embedded in a
// text column. Returns "" for local (non-central) audit rows or any detail
// that isn't this shape — never an error, since most audit_log rows
// (band changes, exports, kills) are plain strings by design.
func machineFromDetail(detail string) string {
	var d struct {
		Machine string `json:"machine"`
	}
	if err := json.Unmarshal([]byte(detail), &d); err != nil {
		return ""
	}
	return d.Machine
}

// buildCoverageReport assembles CoverageReport per the anti-false-positive
// invariant documented on ComplianceBundle.Coverage: it never treats "run
// has no audit row" alone as a gap (that would flag the majority of clean
// runs, which never trip a guardrail decision at all). It only surfaces (1)
// the ingest package's own detector flags, already scoped to real review
// signals, and (2) runs where a guardrail-decision EVENT exists but its
// audit_log counterpart is missing.
func buildCoverageReport(st *store.Store) (CoverageReport, error) {
	var cov CoverageReport

	likeClauses := make([]string, len(coverageFlagPrefixes))
	args := make([]any, len(coverageFlagPrefixes))
	for i, p := range coverageFlagPrefixes {
		likeClauses[i] = "reason LIKE ? || '%'"
		args[i] = p
	}
	flagQuery := fmt.Sprintf(`SELECT id, COALESCE(run_id,''), COALESCE(reason,''), status, COALESCE(jira_key,'')
		FROM flags WHERE (%s) ORDER BY id`, strings.Join(likeClauses, " OR "))
	rows, err := st.DB.Query(flagQuery, args...)
	if err != nil {
		return cov, err
	}
	for rows.Next() {
		var e FlagEntry
		if err := rows.Scan(&e.ID, &e.RunID, &e.Reason, &e.Status, &e.JiraKey); err != nil {
			rows.Close()
			return cov, err
		}
		cov.DetectorFlags = append(cov.DetectorFlags, e)
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return cov, err
	}
	rows.Close()

	kindPlaceholders := make([]string, len(guardrailDecisionEventKinds))
	kindArgs := make([]any, len(guardrailDecisionEventKinds))
	for i, k := range guardrailDecisionEventKinds {
		kindPlaceholders[i] = "?"
		kindArgs[i] = k
	}
	// A run with a decision event but NO audit row at all for it is the real
	// integrity gap. We check run-level presence (subject = run_id), not
	// action = kind, because events.kind is the collapsed form while
	// audit_log.action is granular — an action-equality join would report a
	// false gap for every kill/secrets/budget/sandbox/gate decision.
	eventQuery := fmt.Sprintf(`SELECT e.run_id, e.kind, e.ts FROM events e
		WHERE e.kind IN (%s) AND e.run_id IS NOT NULL AND e.run_id != ''
		AND NOT EXISTS (
			SELECT 1 FROM audit_log al WHERE al.subject = e.run_id
		)
		ORDER BY e.ts`, strings.Join(kindPlaceholders, ","))
	rows, err = st.DB.Query(eventQuery, kindArgs...)
	if err != nil {
		return cov, err
	}
	defer rows.Close()
	for rows.Next() {
		var m MissingAuditEntry
		if err := rows.Scan(&m.RunID, &m.Kind, &m.EventTS); err != nil {
			return cov, err
		}
		cov.MissingAudit = append(cov.MissingAudit, m)
	}
	return cov, rows.Err()
}
