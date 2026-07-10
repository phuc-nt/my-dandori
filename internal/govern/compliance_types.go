package govern

// ComplianceBundle is the auditor-facing export: the full tamper-evident
// trail with its live verification result, plus governance state.
//
// Honesty fields (do not remove without re-reading the doc comments below —
// each one closes a specific way this bundle could otherwise overclaim):
// Coverage, PubkeyFingerprint, TrustNote, Disclosures, SignedCount/
// UnsignedCount, Checkpoint.
type ComplianceBundle struct {
	GeneratedAt string          `json:"generated_at"`
	GeneratedBy string          `json:"generated_by"`
	Verify      VerifyResult    `json:"verify"`
	AuditLog    []AuditEntry    `json:"audit_log"`
	Approvals   []ApprovalEntry `json:"approvals"`
	Flags       []FlagEntry     `json:"flags"`
	RunsSummary []RunSummary    `json:"runs_summary"`

	// SignedCount / UnsignedCount: cutover honesty — an install that turned
	// on signing partway through its history has both. Neither count implies
	// the unsigned rows are untrustworthy; it tells the auditor exactly where
	// the tamper-evidence upgrade took effect.
	SignedCount   int `json:"signed_count"`
	UnsignedCount int `json:"unsigned_count"`

	// Checkpoint is the latest signed checkpoint on disk (if any), included
	// so the auditor can independently confirm the exported AuditLog's tail
	// is not behind the last externally-anchored point. Empty/zero when no
	// signing key has ever been configured.
	Checkpoint *Checkpoint `json:"checkpoint,omitempty"`

	// PubkeyFingerprint is the sha256 hex fingerprint of the CURRENTLY
	// configured signing public key (via PubkeyFingerprint(), the same helper
	// `dandori audit pubkey` uses). See TrustNote: this field is convenience
	// only, NOT the trust root — the bundle is produced by the same party
	// being audited, so a fingerprint shipped inside it proves nothing by
	// itself (an attacker controlling the export could ship their own keypair
	// alongside forged data and this field would "match" perfectly). The
	// auditor must compare it against a value pinned independently, out of
	// band, at onboarding.
	PubkeyFingerprint string `json:"pubkey_fingerprint,omitempty"`

	// TrustNote spells out the out-of-band verification procedure in the
	// bundle itself, so it travels with the data rather than living only in
	// a separate runbook an auditor might not have open.
	TrustNote string `json:"trust_note"`

	// Disclosures are plain-language caveats about what this bundle can and
	// cannot prove — chain-order vs timestamp-order, and the client-attested
	// nature of central-mode audit rows. See buildDisclosures.
	Disclosures []string `json:"disclosures"`

	// Coverage surfaces the ingest package's coverage-gap/stale-snapshot
	// flags (prefix-matched, see coverageFlagPrefixes) plus any run that has
	// a guardrail-decision EVENT but no matching audit row. A run with no
	// guardrail decision at all (the common case — most runs never trip a
	// rule) is intentionally NOT reported here: audit rows are created only
	// on guardrail decisions, so "no audit row" alone is not evidence of
	// anything for a clean run.
	Coverage CoverageReport `json:"coverage"`
}

type VerifyResult struct {
	OK       bool   `json:"ok"`
	BrokenAt int64  `json:"broken_at,omitempty"`
	Reason   string `json:"reason,omitempty"` // "chain" | "signature" | "checkpoint" | "truncated"
}

type AuditEntry struct {
	ID       int64  `json:"id"`
	TS       string `json:"ts"`
	Actor    string `json:"actor"`
	Action   string `json:"action"`
	Subject  string `json:"subject"`
	Detail   string `json:"detail"`
	PrevHash string `json:"prev_hash"`
	Hash     string `json:"hash"`
	Signed   bool   `json:"signed"`
	KeyID    int    `json:"key_id,omitempty"`
	// Machine is parsed out of Detail when it carries the central guardrail
	// canonical JSON shape ({"tool","verdict","reason","machine",
	// "snapshot_fetched_at"} — see ingest/guardrail_audit.go). Empty for
	// local (non-central) audit rows or any detail that isn't that shape.
	Machine string `json:"machine,omitempty"`
}

type ApprovalEntry struct {
	ID        int64  `json:"id"`
	RunID     string `json:"run_id"`
	Action    string `json:"action"`
	Status    string `json:"status"`
	DecidedBy string `json:"decided_by"`
	Note      string `json:"note"`
	Channel   string `json:"channel"`
	Requested string `json:"requested_at"`
	Decided   string `json:"decided_at"`
}

type FlagEntry struct {
	ID      int64  `json:"id"`
	RunID   string `json:"run_id"`
	Reason  string `json:"reason"`
	Status  string `json:"status"`
	JiraKey string `json:"jira_key"`
}

type RunSummary struct {
	ID      string  `json:"id"`
	AgentID string  `json:"agent_id"`
	Project string  `json:"project"`
	Status  string  `json:"status"`
	CostUSD float64 `json:"cost_usd"`
	Started string  `json:"started_at"`
}
