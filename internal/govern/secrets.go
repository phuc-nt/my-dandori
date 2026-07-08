package govern

import (
	"context"
	"fmt"
	"time"

	"github.com/phuc-nt/dandori/internal/redact"
)

// scanCap bounds how much of Command+Content checkSecrets scans. Secrets
// planted mid-heredoc or at the tail of a large generated file would be
// missed by a head-only scan, so both the first and last scanCap bytes are
// checked when the payload exceeds 2*scanCap.
//
// Documented limitation: this is a LITERAL regex scan. It does not catch a
// secret split across lines/tokens, base64-encoded, or assembled via shell
// env-expansion at runtime (e.g. `echo $A$B` where A/B are innocuous halves)
// — those require semantic/runtime analysis this check does not attempt.
const scanCap = 512 * 1024

// piiCardThreshold/piiEmailThreshold are the PII-gate trigger counts:
// requirements call for "≥1 Luhn-valid card OR ≥5 emails" — a single email
// is common and noisy (support address in a comment), but any valid card
// number is sensitive enough to gate on its own.
const (
	piiCardThreshold  = 1
	piiEmailThreshold = 5
)

// checkSecrets is the G1.5 guardrail: strict secret patterns (provider API
// keys, AWS keys, PEM private-key headers, non-env-ref Bearer tokens) DENY;
// PII (Luhn-valid card numbers, or ≥5 email addresses) GATEs on human
// approval. Scans tc.Command and tc.Content only — never tc.Paths, which are
// file locations, not payload.
//
// FailureMode (contract.go): the secret-Deny path is a pure regex match, so
// there is no runtime error to fail on (compile-time only, covered by the
// package's own tests). The PII-gate path calls into the same
// approval-create machinery as checkGate; an error creating/tracking that
// approval fails CLOSED (Deny) for the same reason checkGate's FailClosed
// entry does — a safety control that cannot prove its state was recorded
// must not silently allow.
func (e *Engine) checkSecrets(ctx context.Context, tc ToolCall) (Decision, bool) {
	if e.Cfg != nil && e.Cfg.SecretsGuardEnabled != nil && !*e.Cfg.SecretsGuardEnabled {
		return Decision{}, false
	}

	for _, window := range scanWindows(tc.Command, tc.Content) {
		if kind, ok := secretKind(window); ok {
			return Decision{Deny, fmt.Sprintf("[dandori G1.5] %s detected — value withheld from logs/audit", kind)}, true
		}
	}

	cards, emails := 0, 0
	for _, window := range scanWindows(tc.Command, tc.Content) {
		c, m := countPII(window)
		cards += c
		emails += m
	}
	if cards >= piiCardThreshold || emails >= piiEmailThreshold {
		return e.gatePII(ctx, tc, cards, emails)
	}

	return Decision{}, false
}

// scanWindows returns the head+tail scanCap-byte slices of command and
// content, deduplicated when the payload is small enough that head==tail.
func scanWindows(command, content string) []string {
	var out []string
	for _, s := range []string{command, content} {
		if s == "" {
			continue
		}
		if len(s) <= 2*scanCap {
			out = append(out, s)
			continue
		}
		out = append(out, s[:scanCap], s[len(s)-scanCap:])
	}
	return out
}

// secretKind reports the human-readable TYPE of the first strict-secret
// pattern matched, WITHOUT ever returning the matched value itself — callers
// must not be able to echo the secret via this return.
func secretKind(s string) (string, bool) {
	switch {
	case redact.BearerSecretMatch(s):
		return "Bearer token", true
	case redact.SecretStrictRe.MatchString(s):
		return secretStrictKind(s), true
	default:
		return "", false
	}
}

// secretStrictKind names which strict pattern matched for a clearer deny
// message (still never echoes the value).
func secretStrictKind(s string) string {
	switch {
	case matchesAny(s, `(?i)AKIA[A-Z0-9]{16}`):
		return "AWS access key"
	case matchesAny(s, `-----BEGIN [A-Z ]*PRIVATE KEY-----`):
		return "private key"
	case matchesAny(s, `(?i)sk-[\w-]{20,}`):
		return "API key (sk-)"
	case matchesAny(s, `(?i)ghp_\w{20,}`):
		return "GitHub token"
	case matchesAny(s, `(?i)xox[a-z]-[\w-]{10,}`):
		return "Slack token"
	case matchesAny(s, `(?i)ATATT\S{20,}`):
		return "Atlassian token"
	default:
		return "secret"
	}
}

func matchesAny(s, pattern string) bool {
	re, err := compileCached(pattern)
	return err == nil && re.MatchString(s)
}

// countPII counts Luhn-valid card spans and email addresses in s.
func countPII(s string) (cards, emails int) {
	for _, m := range redact.PiiRe.FindAllString(s, -1) {
		if isEmailShape(m) {
			emails++
		} else if LuhnValid(m) {
			cards++
		}
	}
	return cards, emails
}

func isEmailShape(s string) bool {
	for i := 0; i < len(s); i++ {
		if s[i] == '@' {
			return true
		}
	}
	return false
}

// LuhnValid implements the Luhn checksum for candidate card-number spans
// (digits only; separators like spaces/dashes are stripped first). Used to
// separate a real card number from an arbitrary 13-16 digit run (a phone
// number, an order id) so the PII gate isn't spurious.
func LuhnValid(span string) bool {
	digits := make([]int, 0, len(span))
	for i := 0; i < len(span); i++ {
		c := span[i]
		if c >= '0' && c <= '9' {
			digits = append(digits, int(c-'0'))
		}
	}
	if len(digits) < 13 || len(digits) > 19 {
		return false
	}
	sum := 0
	double := false
	for i := len(digits) - 1; i >= 0; i-- {
		d := digits[i]
		if double {
			d *= 2
			if d > 9 {
				d -= 9
			}
		}
		sum += d
		double = !double
	}
	return sum%10 == 0
}

// gatePII routes a PII-bearing tool call through the approval machinery,
// bypassing band logic entirely (RT-fix: a trusted agent must NOT skip a
// PII gate the way it skips non-critical rule-based gates in checkGate —
// PII exposure risk does not shrink because the agent has a good track
// record on cost/edits). The approval action+reason are masked summaries
// only ("PII phát hiện: N thẻ, M email") — never the raw card/email value —
// so neither the approvals row nor the Slack mirror can leak it.
func (e *Engine) gatePII(ctx context.Context, tc ToolCall, cards, emails int) (Decision, bool) {
	reason := fmt.Sprintf("[dandori G1.5] PII phát hiện: %d thẻ, %d email — cần người duyệt trước khi tiếp tục", cards, emails)
	action := redact.MaskPII(summarizeAction(tc))

	e.expireStale()
	id, err := e.findOrCreateApproval(tc.RunID, action, reason)
	if err != nil {
		// gate, FailClosed (contract.go): cannot create/track the approval → deny
		return Decision{Deny, "[dandori G1.5] internal error creating PII approval: " + err.Error()}, true
	}

	status, decidedBy := e.waitDecision(ctx, id, time.Duration(e.Cfg.GateWaitSeconds)*time.Second)
	switch status {
	case "approved":
		if !e.consume(id) {
			return Decision{Deny, fmt.Sprintf("[dandori G1.5] approval #%d was already consumed by another call — request approval again", id)}, true
		}
		return Decision{Allow, fmt.Sprintf("PII approval #%d granted by %s", id, decidedBy)}, true
	case "rejected":
		return Decision{Deny, fmt.Sprintf("[dandori G1.5] PII approval #%d REJECTED by %s — do not retry with this data", id, decidedBy)}, true
	default:
		return Decision{Deny, fmt.Sprintf("[dandori G1.5] PII approval #%d still pending after %ds — ask an operator to approve at the Dandori console, then retry",
			id, e.Cfg.GateWaitSeconds)}, true
	}
}

// summarizeAction builds the pre-mask action string the same way checkGate
// does (tool name + command/first path) — masked immediately after by the
// caller so the raw PII value never reaches findOrCreateApproval.
func summarizeAction(tc ToolCall) string {
	if tc.Command != "" {
		return tc.Command
	}
	if len(tc.Paths) > 0 {
		return tc.ToolName + " " + tc.Paths[0]
	}
	return tc.ToolName
}
