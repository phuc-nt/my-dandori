// Package redact strips token-shaped strings from free text before it is
// persisted or exported. Agents type secrets into commands (bearer headers,
// provider keys); those commands flow into event payloads, audit details and
// compliance bundles — redaction happens at INGEST so the database never
// holds the raw value, and again at export as defense in depth.
package redact

import "regexp"

// Notes: xox tokens may be URL-encoded (%2B etc.) — include %; kv-style keys
// may be JSON-quoted (`"api_key": "v"`) — allow a closing quote before :=.
// Bearer/kv values may over-consume trailing quotes/braces: over-redaction is
// the safe failure direction.
//
// secretRe is intentionally a SUPERSET of SecretStrictRe and PiiRe below: it
// is the redactor used at capture ingest and export, so it must catch every
// pattern the guardrail (govern.checkSecrets) treats as Deny- or Gate-worthy,
// plus the broader over-matching kv-generic set that would be too noisy for
// a hard Deny. Adding a new Deny/Gate pattern to SecretStrictRe/PiiRe must
// also extend secretRe — the invariant (see redact_test.go) is
// String(x) != x for every x that SecretStrictRe or PiiRe matches.
var secretRe = regexp.MustCompile(
	`(?i)(Bearer\s+\S+|xox[a-z]-[\w%.-]+|ATATT\S+|sk-[\w-]{8,}|ghp_\w+|AKIA[A-Z0-9]{16}|-----BEGIN [A-Z ]*PRIVATE KEY-----|(token|secret|password|api[_-]?key)["']?\s*[=:]\s*\S+)`)

// cardRe matches candidate card-number spans: 13-16 digits, optionally
// grouped by spaces or dashes. Luhn validation (not this regex) decides
// whether a span is really a card — see govern.LuhnValid.
var cardRe = regexp.MustCompile(`\b(?:\d[ -]?){13,16}\b`)

// emailRe matches a standard local@domain email address.
var emailRe = regexp.MustCompile(`[\w.+-]+@[\w-]+\.[\w.-]+`)

// SecretStrictRe is the high-confidence secret set used by the guardrail's
// hard Deny (govern.checkSecrets): provider API keys, AWS access keys and
// PEM private-key headers. Deliberately narrower than secretRe (which also
// over-matches kv-generic pairs like `password=...` for redaction purposes,
// not denial) to keep the false-positive rate low enough for a Deny.
//
// Bearer tokens are handled separately by BearerSecretMatch — RE2 has no
// lookahead, so the "$VAR / ${VAR} env-ref must not deny" exclusion is
// implemented as a post-match check rather than in the pattern itself.
var SecretStrictRe = regexp.MustCompile(
	`(?i)(sk-[\w-]{20,}|xox[a-z]-[\w-]{10,}|ATATT\S{20,}|ghp_\w{20,}|AKIA[A-Z0-9]{16}|-----BEGIN [A-Z ]*PRIVATE KEY-----)`)

// bearerRe captures the token following "Bearer " so the caller can reject
// env-var references ($VAR, ${VAR}) — the sanctioned pattern for wiring a
// secret into a command without it appearing literally.
var bearerRe = regexp.MustCompile(`Bearer\s+([A-Za-z0-9._\-+/=]{16,})`)

// BearerSecretMatch reports whether s contains a Bearer token that is NOT an
// env-var reference. `Bearer $OPENROUTER_API_KEY` / `Bearer ${TOKEN}` must
// NOT match — CLAUDE.md requires env-var refs as the sanctioned way to pass
// a secret into a command.
func BearerSecretMatch(s string) bool {
	m := bearerRe.FindStringSubmatch(s)
	if m == nil {
		return false
	}
	token := m[1]
	return token[0] != '$'
}

// PiiRe matches PII detection surface: email addresses and candidate card
// spans (Luhn validation happens in Go, not the regex — see govern package).
var PiiRe = regexp.MustCompile(emailRe.String() + `|` + cardRe.String())

// String replaces secret-shaped substrings with [REDACTED].
func String(s string) string {
	s = secretRe.ReplaceAllString(s, "[REDACTED]")
	s = cardRe.ReplaceAllStringFunc(s, maskCard)
	s = emailRe.ReplaceAllStringFunc(s, maskEmail)
	return s
}

// Bytes is the []byte variant used on raw JSON payloads.
func Bytes(b []byte) []byte {
	return []byte(String(string(b)))
}

// MaskPII masks card/email spans for text that a human approver needs to
// see the STRUCTURE of (an approval reason), without exposing the raw value
// — distinct from String's full [REDACTED], which would erase all context.
// Card → `4111********1111` (first4+last4); email → local part masked
// (`j***@example.com`).
func MaskPII(s string) string {
	s = cardRe.ReplaceAllStringFunc(s, maskCard)
	s = emailRe.ReplaceAllStringFunc(s, maskEmail)
	return s
}

// maskCard keeps the first 4 and last 4 digits, masking the rest — enough
// structure for a human reviewer to recognize "this is a card number"
// without exposing enough to use it.
func maskCard(span string) string {
	digits := make([]byte, 0, len(span))
	for i := 0; i < len(span); i++ {
		if span[i] >= '0' && span[i] <= '9' {
			digits = append(digits, span[i])
		}
	}
	if len(digits) < 8 {
		return "[REDACTED]"
	}
	first4 := digits[:4]
	last4 := digits[len(digits)-4:]
	mask := make([]byte, len(digits)-8)
	for i := range mask {
		mask[i] = '*'
	}
	return string(first4) + string(mask) + string(last4)
}

// maskEmail keeps the first character of the local part, masking the rest,
// and keeps the domain intact (structure without the identifying local part).
func maskEmail(email string) string {
	at := -1
	for i := 0; i < len(email); i++ {
		if email[i] == '@' {
			at = i
			break
		}
	}
	if at <= 0 {
		return "[email]"
	}
	local := email[:at]
	domain := email[at:]
	if len(local) <= 1 {
		return "*" + domain
	}
	mask := make([]byte, len(local)-1)
	for i := range mask {
		mask[i] = '*'
	}
	return local[:1] + string(mask) + domain
}
