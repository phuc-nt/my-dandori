package capture

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"gopkg.in/yaml.v3"
)

// agentMarker is the optional per-project file written by `dandori init`
// mapping a working directory to an agent name and project label.
type agentMarker struct {
	Agent   string `yaml:"agent"`
	Project string `yaml:"project"`
}

var taskKeyRe = regexp.MustCompile(`\b[A-Z][A-Z0-9]{1,9}-\d+\b`)

// ResolveAttribution decides agent name and project for a run rooted at cwd.
// Precedence: .dandori-agent file → directory basename for both.
func ResolveAttribution(cwd string) (agent, project string) {
	base := filepath.Base(cwd)
	agent, project = base, base
	b, err := os.ReadFile(filepath.Join(cwd, ".dandori-agent"))
	if err != nil {
		return
	}
	var m agentMarker
	if yaml.Unmarshal(b, &m) == nil {
		if m.Agent != "" {
			agent = m.Agent
		}
		if m.Project != "" {
			project = m.Project
		}
	}
	return
}

// FindTaskKey extracts the first Jira-style issue key (e.g. SCRUM-42) from text.
func FindTaskKey(text string) string {
	return taskKeyRe.FindString(text)
}

// TaskKeySource names where a linked key came from, stored as provenance so a
// reader can tell a branch-named link (high trust) from a transcript-scanned
// one (lower trust).
type TaskKeySource string

const (
	TaskKeyFromBranch     TaskKeySource = "branch"
	TaskKeyFromCommit     TaskKeySource = "commit"
	TaskKeyFromTranscript TaskKeySource = "transcript"
)

// FindTaskKeyChain resolves a run's task key by trying the most intentional
// signal first: the branch name a human chose, then commit trailers in the
// run's range, then any key mentioned in the transcript. Each candidate must
// pass `validate` (does it name a work item we actually track?) — an
// unvalidated match is dropped rather than guessed, because a wrong run↔task
// link is worse than an absent one. Returns ("", "") when nothing validates.
func FindTaskKeyChain(branch string, commitMsgs, transcriptTexts []string, validate func(string) bool) (key string, source TaskKeySource) {
	if k := firstValidKey([]string{branch}, validate); k != "" {
		return k, TaskKeyFromBranch
	}
	if k := firstValidKey(commitMsgs, validate); k != "" {
		return k, TaskKeyFromCommit
	}
	if k := firstValidKey(transcriptTexts, validate); k != "" {
		return k, TaskKeyFromTranscript
	}
	return "", ""
}

// firstValidKey returns the first issue key found across texts that passes
// validate. Order within a source is document order (branch has one string;
// commits/transcript are chronological).
func firstValidKey(texts []string, validate func(string) bool) string {
	for _, t := range texts {
		for _, k := range taskKeyRe.FindAllString(t, -1) {
			if validate == nil || validate(k) {
				return k
			}
		}
	}
	return ""
}

// AgentID turns an agent name into its stable id (exported for the ingest
// server, which receives names from remote clients and must map them the
// same way local capture does).
func AgentID(name string) string { return slugify(name) }

// slugify turns an agent name into a stable id.
func slugify(s string) string {
	s = strings.ToLower(strings.TrimSpace(s))
	s = strings.Map(func(r rune) rune {
		switch {
		case r >= 'a' && r <= 'z', r >= '0' && r <= '9':
			return r
		default:
			return '-'
		}
	}, s)
	return strings.Trim(strings.Join(strings.FieldsFunc(s, func(r rune) bool { return r == '-' }), "-"), "-")
}
