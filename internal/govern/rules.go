package govern

import (
	"fmt"
	"regexp"
	"sync"
)

// Rule is one guardrail row with its compiled pattern.
type Rule struct {
	ID          int64
	Kind        string // block | gate
	Pattern     string
	Description string
	Critical    bool   // gates even trusted-band agents
	ScopeType   string // global | agent | project
	ScopeID     string
	re          *regexp.Regexp
}

// appliesTo checks the rule's scope against the calling run's context.
func (r *Rule) appliesTo(tc ToolCall) bool {
	switch r.ScopeType {
	case "agent":
		return r.ScopeID == tc.AgentID
	case "project":
		return r.ScopeID == tc.Project
	}
	return true // global
}

var (
	reCacheMu sync.Mutex
	reCache   = map[string]*regexp.Regexp{}
)

func compileCached(pattern string) (*regexp.Regexp, error) {
	reCacheMu.Lock()
	defer reCacheMu.Unlock()
	if re, ok := reCache[pattern]; ok {
		return re, nil
	}
	re, err := regexp.Compile("(?i)" + pattern)
	if err != nil {
		return nil, err
	}
	reCache[pattern] = re
	return re, nil
}

// loadRules reads enabled rules and compiles their patterns.
func (e *Engine) loadRules() ([]Rule, error) {
	rows, err := e.St.DB.Query(`SELECT id, kind, pattern, COALESCE(description,''), critical, scope_type, scope_id
		FROM guardrail_rules WHERE enabled = 1 ORDER BY id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var rules []Rule
	for rows.Next() {
		var r Rule
		var crit int
		if err := rows.Scan(&r.ID, &r.Kind, &r.Pattern, &r.Description, &crit, &r.ScopeType, &r.ScopeID); err != nil {
			return nil, err
		}
		r.Critical = crit == 1
		if r.re, err = compileCached(r.Pattern); err != nil {
			return nil, fmt.Errorf("rule #%d bad pattern: %w", r.ID, err)
		}
		rules = append(rules, r)
	}
	return rules, rows.Err()
}

// matches tests a rule against the command and every touched path.
func (r *Rule) matches(tc ToolCall) bool {
	if tc.Command != "" && r.re.MatchString(tc.Command) {
		return true
	}
	for _, p := range tc.Paths {
		if r.re.MatchString(p) {
			return true
		}
	}
	return false
}

// checkBlockRules denies on the first matching block rule (G1).
func (e *Engine) checkBlockRules(tc ToolCall, rules []Rule) (Decision, bool) {
	for _, r := range rules {
		if r.Kind == "block" && r.appliesTo(tc) && r.matches(tc) {
			return Decision{Deny, fmt.Sprintf("[dandori G1] blocked: %s (rule #%d)", r.Description, r.ID)}, true
		}
	}
	return Decision{}, false
}
