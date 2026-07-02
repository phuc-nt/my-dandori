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
var secretRe = regexp.MustCompile(
	`(?i)(Bearer\s+\S+|xox[a-z]-[\w%.-]+|ATATT\S+|sk-[\w-]{8,}|ghp_\w+|(token|secret|password|api[_-]?key)["']?\s*[=:]\s*\S+)`)

// String replaces secret-shaped substrings with [REDACTED].
func String(s string) string {
	return secretRe.ReplaceAllString(s, "[REDACTED]")
}

// Bytes is the []byte variant used on raw JSON payloads.
func Bytes(b []byte) []byte {
	return secretRe.ReplaceAll(b, []byte("[REDACTED]"))
}
