package config

import (
	"bufio"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
)

// envFile is the ONLY file credential writes ever touch. It is a package
// constant, never derived from a request, so no path-traversal input can
// redirect a write.
const envFile = ".env"

// envWriteMu serializes the whole read-merge-write-rename so two concurrent
// saves (HTMX double-submit, two browser tabs) cannot lose each other's keys.
var envWriteMu sync.Mutex

// managedKeys is the CLOSED set of env keys the credential UI may write, keyed
// by integration name. It is deliberately NOT a prefix match: a prefix like
// "SLACK_*" would reach SLACK_APPROVERS (the gate approver allowlist) and a
// value with a newline could inject DRY_RUN / AGENT_WRITE_DISABLED. Only these
// exact keys are writable; everything else is rejected.
var managedKeys = map[string][]string{
	"jira":       {"ATLASSIAN_SITE_NAME", "ATLASSIAN_USER_EMAIL", "ATLASSIAN_API_TOKEN", "CONFLUENCE_SPACE_ID"},
	"slack":      {"SLACK_XOXC_TOKEN", "SLACK_XOXD_TOKEN", "SLACK_TEAM_DOMAIN"},
	"openrouter": {"OPENROUTER_API_KEY"},
}

// ManagedKeys returns the exact env keys an integration owns, or nil for an
// unknown integration name (caller should 404).
func ManagedKeys(integration string) []string {
	return managedKeys[integration]
}

// allManagedKeys is the flat allowlist used to reject any key not owned by some
// integration — the hard boundary against writing guardrail switches.
var allManagedKeys = func() map[string]bool {
	m := map[string]bool{}
	for _, keys := range managedKeys {
		for _, k := range keys {
			m[k] = true
		}
	}
	return m
}()

// SaveEnvKeys writes the given key/value pairs into ./.env, preserving every
// other line (comments, unmanaged keys) byte-for-byte. It is atomic (temp +
// rename), durable (fsync before rename), and 0600 from creation.
//
// Every key MUST be in the managed allowlist and every value MUST be free of
// control characters — otherwise the write is refused entirely (no partial
// write). This is the boundary that stops the credential form from flipping
// DRY_RUN / AGENT_WRITE_DISABLED / SLACK_APPROVERS.
func SaveEnvKeys(kv map[string]string) error {
	for k, v := range kv {
		if !allManagedKeys[k] {
			return fmt.Errorf("refusing to write unmanaged env key %q", k)
		}
		if err := validateEnvValue(v); err != nil {
			return fmt.Errorf("invalid value for %q: %w", k, err)
		}
	}

	envWriteMu.Lock()
	defer envWriteMu.Unlock()

	lines, err := readEnvLines(envFile)
	if err != nil {
		return err
	}
	lines = mergeEnvKeys(lines, kv)
	return atomicWriteLines(envFile, lines)
}

// validateEnvValue rejects values that could break out of a single .env line or
// smuggle a second assignment. Newlines are the injection vector.
func validateEnvValue(v string) error {
	if strings.ContainsAny(v, "\n\r") {
		return fmt.Errorf("value contains a newline")
	}
	// A leading quote with no matching trailing quote (or vice versa) would be
	// stripped inconsistently by the loader; reject the ambiguity.
	if len(v) >= 1 {
		lead := v[0] == '"' || v[0] == '\''
		trail := v[len(v)-1] == '"' || v[len(v)-1] == '\''
		if lead != trail {
			return fmt.Errorf("value has an unbalanced leading/trailing quote")
		}
	}
	return nil
}

func readEnvLines(path string) ([]string, error) {
	f, err := os.Open(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil // fresh file
		}
		return nil, err
	}
	defer f.Close()
	var lines []string
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 64*1024), 64*1024)
	for sc.Scan() {
		lines = append(lines, sc.Text())
	}
	return lines, sc.Err()
}

// mergeEnvKeys replaces existing assignments of managed keys in place and
// appends any that were absent, leaving all other lines untouched.
func mergeEnvKeys(lines []string, kv map[string]string) []string {
	written := map[string]bool{}
	for i, line := range lines {
		key := envLineKey(line)
		if key == "" {
			continue
		}
		if _, ok := kv[key]; ok {
			lines[i] = key + "=" + kv[key]
			written[key] = true
		}
	}
	// Deterministic append order for the ones not already present.
	for _, k := range sortedKeys(kv) {
		if !written[k] {
			lines = append(lines, k+"="+kv[k])
		}
	}
	return lines
}

// envLineKey extracts the KEY from a "KEY=VALUE" line (honoring "export "),
// or "" for comments/blanks.
func envLineKey(line string) string {
	t := strings.TrimSpace(line)
	if t == "" || strings.HasPrefix(t, "#") {
		return ""
	}
	t = strings.TrimPrefix(t, "export ")
	eq := strings.Index(t, "=")
	if eq <= 0 {
		return ""
	}
	return strings.TrimSpace(t[:eq])
}

func sortedKeys(kv map[string]string) []string {
	keys := make([]string, 0, len(kv))
	for k := range kv {
		keys = append(keys, k)
	}
	// small n; simple insertion sort avoids importing sort for one call
	for i := 1; i < len(keys); i++ {
		for j := i; j > 0 && keys[j-1] > keys[j]; j-- {
			keys[j-1], keys[j] = keys[j], keys[j-1]
		}
	}
	return keys
}

// atomicWriteLines writes lines to a temp file in the same directory with mode
// 0600 from creation (no world-readable window), fsyncs it, then renames over
// the target so a crash never leaves a half-written .env.
func atomicWriteLines(path string, lines []string) error {
	dir := filepath.Dir(path)
	tmp, err := os.CreateTemp(dir, ".env-*.tmp")
	if err != nil {
		return err
	}
	tmpName := tmp.Name()
	defer os.Remove(tmpName) // no-op after a successful rename
	if err := tmp.Chmod(0o600); err != nil {
		tmp.Close()
		return err
	}
	w := bufio.NewWriter(tmp)
	for _, line := range lines {
		if _, err := w.WriteString(line + "\n"); err != nil {
			tmp.Close()
			return err
		}
	}
	if err := w.Flush(); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Sync(); err != nil { // durability: data hits disk before rename
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return os.Rename(tmpName, path)
}
