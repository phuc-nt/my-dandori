package runner

import "fmt"

// argvFor returns the LOCKED argv for launching an agent. The user's prompt
// is ALWAYS the VALUE of a fixed flag (a single following argv element), so a
// leading "--"/"-" in it is inert — never re-parsed as a flag. This is the
// core defense against flag-injection RCE (e.g. a prompt of
// "--dangerously-bypass-approvals-and-sandbox" must be text, not a flag).
//
// v6 enables `claude` ONLY. A launched claude run goes through Claude Code's
// hooks → pre-tool guardrails + Context Hub injection (governed). Other
// runtimes (codex, aider) have no per-tool interception under wrap, so
// launching them from the browser would be ungoverned — deliberately [Sau].
func argvFor(agent, absBin, prompt string) ([]string, error) {
	switch agent {
	case "claude":
		// prompt is the value of -p; never a positional, never bare "--".
		return []string{absBin, "-p", prompt}, nil
	default:
		return nil, fmt.Errorf("agent %q is not enabled for console launch (v6 = claude only)", agent)
	}
}
