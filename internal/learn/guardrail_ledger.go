package learn

import (
	"regexp"

	"github.com/phuc-nt/dandori/internal/store"
)

// ruleSuffixRE extracts the rule id from a block payload that names a real
// guardrail_rules row, e.g. "[dandori G1] blocked: no rm -rf (rule #7)" or
// "[dandori G4] deploy needs approval (rule #3) — cần người duyệt tại máy
// này". This MUST be tried before any class-level parsing: the leading
// "[dandori GN]" token is a CHECK CLASS (G1=regex rule engine, G2=sandbox,
// G3=budget, G4=gate/approval), not a rule id by itself (F1) — only a
// "(rule #N)" suffix names an actual guardrail_rules row.
var ruleSuffixRE = regexp.MustCompile(`\(rule #(\d+)\)`)

// classTokenRE extracts the bare "[dandori GN]" class token for blocks that
// carry no "(rule #N)" suffix — G2 (sandbox) and G3 (budget) never carry a
// rule id at all (govern/sandbox.go, govern/budget.go), and G1/G4 sometimes
// fire from non-rule paths (e.g. policy_snapshot.go's flat budget-exhausted
// or supervised-band messages). These must NEVER be joined to
// guardrail_rules — doing so would misattribute e.g. a G3 budget block to
// whatever row happens to share that number as an id (F1, verified: G3 could
// alias rule id=3 "DROP TABLE" by coincidence of numbering).
var classTokenRE = regexp.MustCompile(`\[dandori (G\d)\]`)

// classNames maps the bare check-class token to a stable, human label. G1
// with no rule suffix is the regex engine itself (an internal-error branch,
// not a specific rule), hence "regex" rather than "rule".
var classNames = map[string]string{
	"G1": "regex",
	"G2": "sandbox",
	"G3": "budget",
	"G4": "gate",
}

// RuleRow is the ledger for one concrete guardrail_rules row — only blocks
// whose payload carries a "(rule #N)" suffix land here, joined by id.
type RuleRow struct {
	RuleID             int
	Description        string
	Blocks             int // total block events (not runs — F10)
	RunsDone           int // distinct runs that went on to finish
	RunsKilledOrFailed int
	RunsUnfinished     int // still running — excluded from outcome denominator
}

// outcomeRuns is the distinct-run sample the outcome rate is judged over
// (F10): finished runs only, running excluded from the denominator.
func (r RuleRow) outcomeRuns() int { return r.RunsDone + r.RunsKilledOrFailed }

// Insufficient reports whether too few distinct finished runs exist to read
// an outcome rate from this rule (F10 — blocks count is not the sample size).
func (r RuleRow) Insufficient() bool { return r.outcomeRuns() < MinSampleForInsight }

// ClassRow is the ledger for a check CLASS with no per-rule identity (G2
// sandbox, G3 budget, G4 gate-without-suffix, G1 regex-engine-without-suffix)
// — never joined to guardrail_rules (F1).
type ClassRow struct {
	Class              string // sandbox|budget|gate|regex
	Blocks             int
	RunsDone           int
	RunsKilledOrFailed int
	RunsUnfinished     int
}

func (c ClassRow) outcomeRuns() int { return c.RunsDone + c.RunsKilledOrFailed }

// Insufficient mirrors RuleRow.Insufficient — see F10.
func (c ClassRow) Insufficient() bool { return c.outcomeRuns() < MinSampleForInsight }

// LedgerResult is the two-tier guardrail effectiveness ledger (F1): per-rule
// for named rules, per-class for checks that carry no rule identity.
type LedgerResult struct {
	PerRule  []RuleRow
	PerClass []ClassRow
}

// blockEvent is one guardrail_block row joined to its run's final status,
// before payload parsing.
type blockEvent struct {
	RunID     string
	Payload   string
	RunStatus string
}

// runOutcome buckets one distinct run's final status into the three ledger
// buckets (F10: bucketed once per run, not once per block).
type runOutcome struct {
	done, killedOrFailed, unfinished bool
}

func bucketRunStatus(status string) runOutcome {
	switch status {
	case "done":
		return runOutcome{done: true}
	case "failed", "killed":
		return runOutcome{killedOrFailed: true}
	default: // "running" or any other in-flight state
		return runOutcome{unfinished: true}
	}
}

// GuardrailLedger builds the two-tier effectiveness ledger over guardrail
// blocks in the window. Per rule when the block payload names one via
// "(rule #N)"; per check-class otherwise. Outcome buckets count DISTINCT
// runs, never raw block counts (F10) — a single noisy run can fire the same
// check many times (e.g. G2 sandbox retried 11×) without that inflating the
// apparent sample size.
func GuardrailLedger(st *store.Store, days int) (LedgerResult, error) {
	events, err := queryGuardrailBlocks(st, days)
	if err != nil {
		return LedgerResult{}, err
	}

	ruleBlocks := map[ruleKey]int{}
	ruleRunStatus := map[ruleKey]map[string]string{} // ruleKey -> runID -> status
	classBlocks := map[string]int{}
	classRunStatus := map[string]map[string]string{}

	for _, e := range events {
		if m := ruleSuffixRE.FindStringSubmatch(e.Payload); m != nil {
			id := atoiSafe(m[1])
			k := ruleKey{id: id}
			ruleBlocks[k]++
			if ruleRunStatus[k] == nil {
				ruleRunStatus[k] = map[string]string{}
			}
			ruleRunStatus[k][e.RunID] = e.RunStatus
			continue
		}
		class := "regex"
		if m := classTokenRE.FindStringSubmatch(e.Payload); m != nil {
			if name, ok := classNames[m[1]]; ok {
				class = name
			}
		}
		classBlocks[class]++
		if classRunStatus[class] == nil {
			classRunStatus[class] = map[string]string{}
		}
		classRunStatus[class][e.RunID] = e.RunStatus
	}

	perRule, err := buildRuleRows(st, ruleBlocks, ruleRunStatus)
	if err != nil {
		return LedgerResult{}, err
	}
	perClass := buildClassRows(classBlocks, classRunStatus)

	return LedgerResult{PerRule: perRule, PerClass: perClass}, nil
}

// ruleKey identifies one guardrail_rules row inside the in-memory
// accumulation maps.
type ruleKey struct {
	id int
}

func buildRuleRows(st *store.Store, blocks map[ruleKey]int, runStatus map[ruleKey]map[string]string) ([]RuleRow, error) {
	out := make([]RuleRow, 0, len(blocks))
	for k, n := range blocks {
		row := RuleRow{RuleID: k.id, Blocks: n}
		for _, status := range runStatus[k] {
			o := bucketRunStatus(status)
			if o.done {
				row.RunsDone++
			} else if o.killedOrFailed {
				row.RunsKilledOrFailed++
			} else {
				row.RunsUnfinished++
			}
		}
		var desc string
		err := st.DB.QueryRow(`SELECT description FROM guardrail_rules WHERE id = ?`, k.id).Scan(&desc)
		switch {
		case err == nil:
			row.Description = desc
		default:
			row.Description = "(rule deleted or unknown)"
		}
		out = append(out, row)
	}
	return out, nil
}

func buildClassRows(blocks map[string]int, runStatus map[string]map[string]string) []ClassRow {
	out := make([]ClassRow, 0, len(blocks))
	for class, n := range blocks {
		row := ClassRow{Class: class, Blocks: n}
		for _, status := range runStatus[class] {
			o := bucketRunStatus(status)
			if o.done {
				row.RunsDone++
			} else if o.killedOrFailed {
				row.RunsKilledOrFailed++
			} else {
				row.RunsUnfinished++
			}
		}
		out = append(out, row)
	}
	return out
}

func queryGuardrailBlocks(st *store.Store, days int) ([]blockEvent, error) {
	rows, err := st.DB.Query(`
		SELECT e.run_id, e.payload, r.status
		FROM events e JOIN runs r ON r.id = e.run_id
		WHERE e.kind='guardrail_block' AND e.ok=0
		  AND r.id NOT LIKE 'g2-verify%' AND r.id NOT LIKE 'gate-verify%'
		` + insightWindowClauseCol("r.started_at", days))
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []blockEvent
	for rows.Next() {
		var e blockEvent
		if err := rows.Scan(&e.RunID, &e.Payload, &e.RunStatus); err != nil {
			return nil, err
		}
		out = append(out, e)
	}
	return out, rows.Err()
}

// atoiSafe converts a regex-captured digit string to int. The capture group
// is `\d+`, so it can only fail to parse on an implausibly huge number — 0 is
// a safe, visibly-wrong fallback rather than a panic.
func atoiSafe(s string) int {
	n := 0
	for _, c := range s {
		if c < '0' || c > '9' {
			return 0
		}
		n = n*10 + int(c-'0')
	}
	return n
}
