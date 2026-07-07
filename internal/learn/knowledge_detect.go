package learn

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"

	"github.com/phuc-nt/dandori/internal/store"
)

// Knowledge detection: five deterministic SQL/Go detectors over ALREADY
// captured events, each NOMINATE-only (writes knowledge_units via
// NominateUnit, state=nominated) — NEVER publish, NEVER an external write.
// A human reviews and decides at /reviews (P3). No LLM-judge, no hidden
// thresholds: every gate here is MinSampleForKnowledge (F17) or a disjoint
// Wilson CI, and every number that ships also carries its CI + n.

// bucket accumulates done/n/cost for one side (present or absent, or a
// tool's pass/fail) of a contrast.
type bucket struct {
	done int
	n    int
	cost float64
}

func (b *bucket) add(ok bool, cost float64) {
	b.n++
	b.cost += cost
	if ok {
		b.done++
	}
}

func (b bucket) avgCost() float64 {
	if b.n == 0 {
		return 0
	}
	return b.cost / float64(b.n)
}

// ciDisjoint reports whether two Wilson CIs [aLo,aHi] and [bLo,bHi] (whole
// percent) do not overlap — the honesty gate for "this looks like a real
// difference, not noise" (docs/07 §3).
func ciDisjoint(aLo, aHi, bLo, bHi int) bool {
	return aLo > bHi || bLo > aHi
}

// ---------------------------------------------------------------------------
// Detector 1: skill usage
// ---------------------------------------------------------------------------

// skillRunRow is one distinct (run, skill) pair from tool_use/Skill events,
// joined to the run's outcome. Payload shape VERIFIED on the fleet DB:
// {"skill":"<name>","args":"..."}.
type skillRunRow struct {
	runID   string
	skill   string
	project string
	status  string
	cost    float64
}

// querySkillRuns implements the SQL sketch from phase-02: distinct (run,
// skill) pairs from tool_use/Skill events, joined to run outcome.
func querySkillRuns(st *store.Store, days int) ([]skillRunRow, error) {
	rows, err := st.Read().Query(`
		WITH skill_runs AS (
			SELECT DISTINCT e.run_id, json_extract(e.payload,'$.skill') AS skill
			FROM events e WHERE e.kind='tool_use' AND e.tool_name='Skill'
			  AND json_extract(e.payload,'$.skill') IS NOT NULL
		)
		SELECT sr.run_id, sr.skill, COALESCE(r.project,''), r.status, r.cost_usd
		FROM skill_runs sr JOIN runs r ON r.id = sr.run_id
		WHERE r.ended_at IS NOT NULL` + insightWindowClauseCol("r.started_at", days))
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []skillRunRow
	for rows.Next() {
		var r skillRunRow
		if err := rows.Scan(&r.runID, &r.skill, &r.project, &r.status, &r.cost); err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

// allRunRow is one ended run's outcome, used as the universe for a skill's
// "absent" bucket (all ended runs minus the ones that used the skill).
type allRunRow struct {
	project string
	status  string
	cost    float64
}

func queryAllRuns(st *store.Store, days int) (map[string]allRunRow, error) {
	rows, err := st.Read().Query(`
		SELECT id, COALESCE(project,''), status, cost_usd
		FROM runs WHERE ended_at IS NOT NULL` + insightWindowClauseCol("started_at", days))
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[string]allRunRow{}
	for rows.Next() {
		var id string
		var r allRunRow
		if err := rows.Scan(&id, &r.project, &r.status, &r.cost); err != nil {
			return nil, err
		}
		out[id] = r
	}
	return out, rows.Err()
}

// localSkillBody reads .claude/skills/<name>/SKILL.md from the repo working
// directory, if present. detectSkillUsage measures the value of a skill that
// is ALREADY installed locally (invoked via the Skill tool) — it is not
// authoring new skill content, so the only honest body to attach is the
// skill's own current file. Absent file → "" (caller skips nominate rather
// than fabricate a body, since NominateUnit requires a non-empty body for
// kind=skill — P1 F9 contract).
func localSkillBody(name string) string {
	if !ValidSlug(name) {
		return ""
	}
	path := filepath.Join(".claude", "skills", name, "SKILL.md")
	b, err := os.ReadFile(path)
	if err != nil {
		return ""
	}
	return string(b)
}

// detectSkillUsage compares runs that invoked skill S vs runs in the SAME
// project that did not, over the window. Project-segment FIRST (task-mix
// confound guard): nominate is scoped to the single (skill, project) pair
// with the strongest qualifying signal; the title notes whether ≥2 projects
// independently agree in direction (fleet-wide corroboration) or the signal
// is single-project only — never silently over-claims "fleet-wide" (docs/07
// §3 confound guard). Nominates kind=skill only when n on both sides ≥
// MinSampleForKnowledge AND the two Wilson CIs are disjoint, and only when a
// local SKILL.md body exists to pin (F9 body-required contract).
func detectSkillUsage(st *store.Store, days int) ([]NominateParams, error) {
	presentRows, err := querySkillRuns(st, days)
	if err != nil {
		return nil, err
	}
	if len(presentRows) == 0 {
		return nil, nil // honest empty — no Skill-tool events in window
	}
	allRuns, err := queryAllRuns(st, days)
	if err != nil {
		return nil, err
	}

	type presentKey struct{ skill, project string }
	present := map[presentKey]*bucket{}
	presentRunIDs := map[presentKey][]string{}
	usedBySkill := map[string]map[string]bool{} // skill -> run_id -> true (any project)
	skillSet := map[string]bool{}

	for _, r := range presentRows {
		k := presentKey{r.skill, r.project}
		b, ok := present[k]
		if !ok {
			b = &bucket{}
			present[k] = b
		}
		b.add(r.status == "done", r.cost)
		presentRunIDs[k] = append(presentRunIDs[k], r.runID)

		if usedBySkill[r.skill] == nil {
			usedBySkill[r.skill] = map[string]bool{}
		}
		usedBySkill[r.skill][r.runID] = true
		skillSet[r.skill] = true
	}

	var out []NominateParams
	for skill := range skillSet {
		used := usedBySkill[skill]
		absentByProject := map[string]*bucket{}
		for id, r := range allRuns {
			if used[id] {
				continue
			}
			b, ok := absentByProject[r.project]
			if !ok {
				b = &bucket{}
				absentByProject[r.project] = b
			}
			b.add(r.status == "done", r.cost)
		}

		type qualifying struct {
			project       string
			presentBetter bool
		}
		var agreeing []qualifying
		var chosenProject string
		var chosenPresent, chosenAbsent *bucket

		// Deterministic project order so output is stable across runs.
		var projects []string
		for k := range present {
			if k.skill == skill {
				projects = append(projects, k.project)
			}
		}
		sort.Strings(projects)

		for _, project := range projects {
			pb := present[presentKey{skill, project}]
			ab, ok := absentByProject[project]
			if !ok || pb.n < MinSampleForKnowledge || ab.n < MinSampleForKnowledge {
				continue
			}
			pLo, pHi := WilsonPct(pb.done, pb.n)
			aLo, aHi := WilsonPct(ab.done, ab.n)
			if !ciDisjoint(pLo, pHi, aLo, aHi) {
				continue // CI overlap → "chưa kết luận", do not nominate (honest)
			}
			presentRate := float64(pb.done) / float64(pb.n)
			absentRate := float64(ab.done) / float64(ab.n)
			agreeing = append(agreeing, qualifying{project, presentRate > absentRate})
			if chosenProject == "" {
				chosenProject = project
				chosenPresent, chosenAbsent = pb, ab
			}
		}
		if chosenProject == "" {
			continue
		}

		agreeCount := 0
		firstDir := agreeing[0].presentBetter
		for _, d := range agreeing {
			if d.presentBetter == firstDir {
				agreeCount++
			}
		}
		fleetWide := agreeCount >= 2

		body := localSkillBody(skill)
		if body == "" {
			continue // no local SKILL.md to pin — cannot satisfy body-required contract (F9); skip, don't fabricate
		}

		pLo, pHi := WilsonPct(chosenPresent.done, chosenPresent.n)
		aLo, aHi := WilsonPct(chosenAbsent.done, chosenAbsent.n)
		title := fmt.Sprintf("Skill dùng nhiều: %s", skill)
		if fleetWide {
			title += " (≥2 project đồng thuận chiều — vẫn là tương quan quan sát, không phải thí nghiệm đối chứng)"
		} else {
			title += fmt.Sprintf(" (project %s — tương quan quan sát)", chosenProject)
		}

		prov := presentRunIDs[presentKey{skill, chosenProject}]
		if len(prov) > 50 {
			prov = prov[:50]
		}
		out = append(out, NominateParams{
			Kind:  KindSkill,
			Name:  skill,
			Title: title,
			Body:  body,
			Stats: StatsSnapshot{
				NPresent: chosenPresent.n, NAbsent: chosenAbsent.n,
				DonePresent: float64(chosenPresent.done) / float64(chosenPresent.n),
				DoneAbsent:  float64(chosenAbsent.done) / float64(chosenAbsent.n),
				CIPresentLo: pLo, CIPresentHi: pHi,
				CIAbsentLo: aLo, CIAbsentHi: aHi,
				CostPresent: chosenPresent.avgCost(), CostAbsent: chosenAbsent.avgCost(),
			},
			ProvenanceRun: prov,
			NominatedBy:   "dandori-observer",
		})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out, nil
}

// ---------------------------------------------------------------------------
// Detector 2: tool pattern (nominates kind=context, F16 — no first-class kind)
// ---------------------------------------------------------------------------

// clusterTool is a (keyword, tool) pair — the join key between a run's task
// keyword cluster (reusing extractKeywords, assignment_suggest.go) and a tool
// it invoked.
type clusterTool struct{ keyword, tool string }

// detectToolPattern nominates kind=context (F16): finds the single strongest
// (keyword cluster, tool) pair — most DONE-run occurrences among pairs
// meeting MinSampleForKnowledge on both total cluster occurrences and the
// tool's own pass-rate sample — and nominates ONE context unit suggesting
// "task dạng K: cân nhắc tool X." Human polishes wording at review (P3).
//
// This proposes genuinely NEW context text (no existing context_versions row
// to reference yet — that would require actually writing the doc, which is a
// publish action, forbidden here), so it carries Body instead of RefID, per
// the KindContext RefID==0-with-Body exception added to NominateUnit for
// exactly this case.
func detectToolPattern(st *store.Store, days int) ([]NominateParams, error) {
	rows, err := st.Read().Query(`
		SELECT e.tool_name, e.ok, COALESCE(r.task_key,''), r.status
		FROM events e JOIN runs r ON r.id = e.run_id
		WHERE e.kind='tool_use' AND e.tool_name IS NOT NULL AND e.tool_name != ''
		  AND r.ended_at IS NOT NULL` + insightWindowClauseCol("r.started_at", days))
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	doneCount := map[clusterTool]int{}  // tool used in a DONE run of this keyword cluster
	totalCount := map[clusterTool]int{} // tool used in ANY ended run of this keyword cluster
	toolStats := map[string]*bucket{}   // per-tool pass/fail sample (events.ok non-null)

	for rows.Next() {
		var tool, taskKey, status string
		var okVal any
		if err := rows.Scan(&tool, &okVal, &taskKey, &status); err != nil {
			return nil, err
		}
		if okInt, isInt := okVal.(int64); isInt {
			b, exists := toolStats[tool]
			if !exists {
				b = &bucket{}
				toolStats[tool] = b
			}
			b.add(okInt != 0, 0)
		}
		for _, kw := range extractKeywords(taskKey) {
			ct := clusterTool{kw, tool}
			totalCount[ct]++
			if status == "done" {
				doneCount[ct]++
			}
		}
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	if len(totalCount) == 0 {
		return nil, nil // honest empty — no task_key keyword clusters seen yet
	}

	type candidate struct {
		ct   clusterTool
		done int
	}
	var candidates []candidate
	for ct, total := range totalCount {
		if total < MinSampleForKnowledge {
			continue
		}
		tb, ok := toolStats[ct.tool]
		if !ok || tb.n < MinSampleForKnowledge {
			continue
		}
		candidates = append(candidates, candidate{ct, doneCount[ct]})
	}
	if len(candidates) == 0 {
		return nil, nil // honest: not enough data yet for any cluster×tool pair
	}
	// Deterministic pick: highest done-count, tie-broken by name so output is
	// stable across repeated runs on the same data.
	sort.Slice(candidates, func(i, j int) bool {
		if candidates[i].done != candidates[j].done {
			return candidates[i].done > candidates[j].done
		}
		if candidates[i].ct.keyword != candidates[j].ct.keyword {
			return candidates[i].ct.keyword < candidates[j].ct.keyword
		}
		return candidates[i].ct.tool < candidates[j].ct.tool
	})
	top := candidates[0].ct
	tb := toolStats[top.tool]
	passLo, passHi := WilsonPct(tb.done, tb.n)
	passPct := int(float64(tb.done) / float64(tb.n) * 100)

	name := toolPatternSlug(top.keyword, top.tool)
	body := fmt.Sprintf(
		"Quan sát: task dạng \"%s\" — cân nhắc dùng tool %s (pass-rate %d%% CI %d–%d%%, n=%d). "+
			"Tương quan quan sát, không phải thí nghiệm đối chứng — người viết context cần diễn đạt lại cho rõ.",
		top.keyword, top.tool, passPct, passLo, passHi, tb.n)

	return []NominateParams{{
		Kind:  KindContext,
		Name:  name,
		Title: fmt.Sprintf("Tool pattern: %s cho task dạng \"%s\"", top.tool, top.keyword),
		Body:  body,
		Stats: StatsSnapshot{
			NPresent:    tb.n,
			DonePresent: float64(tb.done) / float64(tb.n),
			CIPresentLo: passLo, CIPresentHi: passHi,
		},
		NominatedBy: "dandori-observer",
	}}, nil
}

// toolPatternSlug builds the "tool-<keyword>-<tool>" slug the spec calls
// for, lower-cased and stripped of characters ValidSlug would reject.
func toolPatternSlug(keyword, tool string) string {
	clean := func(s string) string {
		var b []byte
		for i := 0; i < len(s); i++ {
			c := s[i]
			switch {
			case c >= 'a' && c <= 'z', c >= '0' && c <= '9':
				b = append(b, c)
			case c >= 'A' && c <= 'Z':
				b = append(b, c+32)
			default:
				if len(b) > 0 && b[len(b)-1] != '-' {
					b = append(b, '-')
				}
			}
		}
		for len(b) > 0 && b[len(b)-1] == '-' {
			b = b[:len(b)-1]
		}
		return string(b)
	}
	slug := "tool-" + clean(keyword) + "-" + clean(tool)
	if len(slug) > 200 {
		slug = slug[:200]
	}
	return slug
}

// ---------------------------------------------------------------------------
// Detector 3: rule lifecycle (scope-up / retire)
// ---------------------------------------------------------------------------

// ruleLifecycleMinBlocks is the minimum block-event volume before a rule's
// scope-up/retire signal is trusted — reuses MinSampleForKnowledge (F17) as
// the floor, same as every other knowledge nominate.
const ruleLifecycleMinBlocks = MinSampleForKnowledge

// detectRuleLifecycle inspects GuardrailLedger's per-rule rows (reused
// as-is, not rewritten — guardrail_ledger.go owns that math) for two
// lifecycle signals, each gated at MinSampleForKnowledge outcome-runs:
//   - high block rate + low override (runs still finish clean after the
//     block, i.e. RunsKilledOrFailed stays low relative to RunsDone) on a
//     team/agent-scoped rule → nominate "scope-up to company"
//   - the rule's blocked runs mostly end in failed/killed (proxy for
//     "engineers keep working around/failing against it," i.e. the rule
//     doesn't hold) → nominate "retire"
//
// Only rules that carry a scope (guardrail_rules.scope_type != 'global') are
// eligible for scope-up (a global rule has nowhere higher to scope to).
// Any rule (global or scoped) is eligible for retire.
func detectRuleLifecycle(st *store.Store, days int) ([]NominateParams, error) {
	ledger, err := GuardrailLedger(st, days)
	if err != nil {
		return nil, err
	}
	var out []NominateParams
	for _, row := range ledger.PerRule {
		if row.outcomeRuns() < ruleLifecycleMinBlocks {
			continue // honest: too few finished runs to read a lifecycle signal
		}
		scopeType, _, err := ruleScope(st, row.RuleID)
		if err != nil {
			return nil, err
		}

		overrideLo, overrideHi := WilsonPct(row.RunsKilledOrFailed, row.outcomeRuns())
		cleanLo, cleanHi := WilsonPct(row.RunsDone, row.outcomeRuns())

		// Scope-up: rule is scoped (not already global), block rate is
		// substantial, and clean-completion CI is clearly above the
		// override/failure CI (i.e., the block does not stop work — the run
		// still finishes, so tightening it fleet-wide looks safe).
		if scopeType != "" && scopeType != "global" && ciDisjoint(cleanLo, cleanHi, overrideLo, overrideHi) && row.RunsDone > row.RunsKilledOrFailed {
			slugName := fmt.Sprintf("rule-%d-scope-up", row.RuleID)
			out = append(out, NominateParams{
				Kind:    KindRule,
				Name:    slugName,
				Title:   fmt.Sprintf("Nâng scope company: rule #%d (%s)", row.RuleID, row.Description),
				RefKind: "guardrail_rule",
				RefID:   int64(row.RuleID),
				Stats: StatsSnapshot{
					NPresent: row.outcomeRuns(), DonePresent: float64(row.RunsDone) / float64(row.outcomeRuns()),
					CIPresentLo: cleanLo, CIPresentHi: cleanHi,
					NAbsent: row.outcomeRuns(), DoneAbsent: float64(row.RunsKilledOrFailed) / float64(row.outcomeRuns()),
					CIAbsentLo: overrideLo, CIAbsentHi: overrideHi,
				},
				NominatedBy: "dandori-observer",
			})
		}

		// Retire: the rule's blocked runs mostly end failed/killed (it keeps
		// firing but the run dies anyway — the rule isn't preventing a bad
		// outcome, or engineers repeatedly fight it), i.e. failed/killed CI
		// clearly ABOVE the clean-completion CI.
		if ciDisjoint(overrideLo, overrideHi, cleanLo, cleanHi) && row.RunsKilledOrFailed > row.RunsDone {
			slugName := fmt.Sprintf("rule-%d-retire", row.RuleID)
			out = append(out, NominateParams{
				Kind:    KindRule,
				Name:    slugName,
				Title:   fmt.Sprintf("Đề xuất gỡ: rule #%d (%s) — thường bị vượt qua/không giữ được outcome", row.RuleID, row.Description),
				RefKind: "guardrail_rule",
				RefID:   int64(row.RuleID),
				Stats: StatsSnapshot{
					NPresent: row.outcomeRuns(), DonePresent: float64(row.RunsKilledOrFailed) / float64(row.outcomeRuns()),
					CIPresentLo: overrideLo, CIPresentHi: overrideHi,
					NAbsent: row.outcomeRuns(), DoneAbsent: float64(row.RunsDone) / float64(row.outcomeRuns()),
					CIAbsentLo: cleanLo, CIAbsentHi: cleanHi,
				},
				NominatedBy: "dandori-observer",
			})
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out, nil
}

// ruleScope reads a guardrail_rules row's scope_type/scope_id. Returns ("",
// "", nil) if the rule no longer exists (deleted between the ledger scan and
// this lookup) so the caller treats it as "not eligible" rather than erroring
// the whole sweep for one stale row.
func ruleScope(st *store.Store, ruleID int) (scopeType, scopeID string, err error) {
	err = st.Read().QueryRow(`SELECT scope_type, scope_id FROM guardrail_rules WHERE id = ?`, ruleID).
		Scan(&scopeType, &scopeID)
	if err != nil {
		return "", "", nil // deleted/unknown rule — not an error, just ineligible
	}
	return scopeType, scopeID, nil
}

// ---------------------------------------------------------------------------
// Detector 4: context promote (kind=context, on ContextROIPairs ready deltas)
// ---------------------------------------------------------------------------

// detectContextPromote inspects ContextROIPairs' "ready" deltas (both sides
// already meet MinSampleForInsight and have disjoint done-rate CIs — reused
// as-is from context_roi.go) and nominates "promote team→company" for a
// positive delta whose newer version does better. Scoped to team→company
// promotion only (the spec's stated direction); company/agent deltas are
// still computed by ContextROIPairs but are not a promotion candidate here.
func detectContextPromote(st *store.Store, days int) ([]NominateParams, error) {
	ready, _, err := ContextROIPairs(st, days)
	if err != nil {
		return nil, err
	}
	var out []NominateParams
	for _, d := range ready {
		if d.Layer != "team" {
			continue // promote direction is team -> company (spec)
		}
		if d.NoContrast {
			continue
		}
		if !ciDisjoint(d.DoneRateFromLo, d.DoneRateFromHi, d.DoneRateToLo, d.DoneRateToHi) {
			continue // CI overlap -> honest "chưa kết luận", no nominate
		}
		if d.DoneRateTo <= d.DoneRateFrom {
			continue // only a positive delta (newer version better) is a promote candidate
		}
		refID, err := contextVersionRefID(st, d.Layer, d.Target, d.ToV)
		if err != nil {
			return nil, err
		}
		if refID == 0 {
			continue // no matching context_versions row found — skip rather than fabricate a ref
		}
		name := fmt.Sprintf("context-%s-promote-v%d", sanitizeTarget(d.Target), d.ToV)
		out = append(out, NominateParams{
			Kind:        KindContext,
			Name:        name,
			Title:       fmt.Sprintf("Promote team→company: %s v%d (done-rate %.0f%%→%.0f%%)", d.Target, d.ToV, d.DoneRateFrom*100, d.DoneRateTo*100),
			RefKind:     "context_version",
			RefID:       refID,
			Layer:       "company",
			LayerTarget: "*",
			Stats: StatsSnapshot{
				NPresent: d.RunsTo, DonePresent: d.DoneRateTo,
				CIPresentLo: d.DoneRateToLo, CIPresentHi: d.DoneRateToHi,
				NAbsent: d.RunsFrom, DoneAbsent: d.DoneRateFrom,
				CIAbsentLo: d.DoneRateFromLo, CIAbsentHi: d.DoneRateFromHi,
				CostPresent: d.CostPerDoneTo, CostAbsent: d.CostPerDoneFrom,
			},
			NominatedBy: "dandori-observer",
		})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out, nil
}

// contextVersionRefID looks up the context_versions.id for a given
// (layer,target,version_n) bucket, so the nominate carries a real ref
// (P1 contract: context-kind nominate needs an existing row). 0 if not found.
func contextVersionRefID(st *store.Store, layer, target string, versionN int) (int64, error) {
	targetID := target
	if layer == "company" {
		targetID = "*"
	}
	var id int64
	err := st.Read().QueryRow(`
		SELECT v.id FROM context_versions v
		JOIN contexts c ON c.id = v.context_id
		WHERE c.layer = ? AND c.target_id = ? AND v.version_n = ?`, layer, targetID, versionN).Scan(&id)
	if err != nil {
		return 0, nil // no matching row — caller skips this candidate
	}
	return id, nil
}

// sanitizeTarget turns a project/agent_id target into a slug-safe fragment.
func sanitizeTarget(target string) string {
	var b []byte
	for i := 0; i < len(target); i++ {
		c := target[i]
		switch {
		case c >= 'a' && c <= 'z', c >= '0' && c <= '9':
			b = append(b, c)
		case c >= 'A' && c <= 'Z':
			b = append(b, c+32)
		default:
			if len(b) > 0 && b[len(b)-1] != '-' {
				b = append(b, '-')
			}
		}
	}
	for len(b) > 0 && b[len(b)-1] == '-' {
		b = b[:len(b)-1]
	}
	if len(b) == 0 {
		return "target"
	}
	return string(b)
}

// ---------------------------------------------------------------------------
// Orchestrator
// ---------------------------------------------------------------------------

// DetectKnowledgeUnits runs all five detectors (skill, tool-pattern→context,
// rule-lifecycle, context-promote, playbook) and calls NominateUnit for each
// candidate. A duplicate-draft rejection from NominateUnit (P1: "a draft for
// %s %q is already pending review") is treated as a SKIP, not a failure —
// idempotent re-runs of the sweep must not error just because a prior sweep
// already nominated the same (kind,name). Any other error aborts the sweep
// and is returned (a real DB error should not be swallowed).
func DetectKnowledgeUnits(st *store.Store, days int) (nominated int, skipped int, err error) {
	var candidates []NominateParams

	skillCands, err := detectSkillUsage(st, days)
	if err != nil {
		return 0, 0, fmt.Errorf("detectSkillUsage: %w", err)
	}
	candidates = append(candidates, skillCands...)

	toolCands, err := detectToolPattern(st, days)
	if err != nil {
		return 0, 0, fmt.Errorf("detectToolPattern: %w", err)
	}
	candidates = append(candidates, toolCands...)

	ruleCands, err := detectRuleLifecycle(st, days)
	if err != nil {
		return 0, 0, fmt.Errorf("detectRuleLifecycle: %w", err)
	}
	candidates = append(candidates, ruleCands...)

	ctxCands, err := detectContextPromote(st, days)
	if err != nil {
		return 0, 0, fmt.Errorf("detectContextPromote: %w", err)
	}
	candidates = append(candidates, ctxCands...)

	// Playbook: DetectCandidates + PromoteCandidate already wired to
	// NominateUnit(kind=playbook) in P1 (flywheel.go) — this sweep just calls
	// that existing path so DetectKnowledgeUnits is the single orchestrator
	// entry point (spec: "P2 chỉ wire vào sweep", don't duplicate the logic).
	pbCands, err := DetectCandidates(st, days)
	if err != nil {
		return 0, 0, fmt.Errorf("DetectCandidates (playbook): %w", err)
	}

	// M2: only a duplicate-draft is an expected, skip-worthy outcome (another
	// detector run or a human already nominated the same kind+name). Any
	// other error (DB failure, invalid params) is a real bug and must
	// propagate — silently folding it into "skipped" hid genuine failures
	// behind a normal-looking count.
	for _, c := range candidates {
		if _, nomErr := NominateUnit(st, c); nomErr != nil {
			if errors.Is(nomErr, ErrDuplicateDraft) {
				skipped++
				continue
			}
			return nominated, skipped, fmt.Errorf("nominate %s %q: %w", c.Kind, c.Name, nomErr)
		}
		nominated++
	}
	for _, c := range pbCands {
		if _, promoteErr := PromoteCandidate(st, c, "dandori-observer"); promoteErr != nil {
			if errors.Is(promoteErr, ErrDuplicateDraft) {
				skipped++
				continue
			}
			return nominated, skipped, fmt.Errorf("promote candidate %s: %w", c.RunID, promoteErr)
		}
		nominated++
	}

	return nominated, skipped, nil
}
