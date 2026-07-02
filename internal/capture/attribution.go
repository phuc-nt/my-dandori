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
