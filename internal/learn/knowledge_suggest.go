package learn

import (
	"sort"

	"github.com/phuc-nt/dandori/internal/store"
)

// Cross-agent knowledge suggest (P4, F16): surfaces published knowledge_units
// an agent has not yet used, ranked by (present-vs-absent done-rate delta) ×
// (keyword overlap with the agent's own task history). Reuses extractKeywords
// / the SuggestAgents matching pattern (assignment_suggest.go) — no new ML,
// KISS. F11: present/absent stats are RECOMPUTED live from the events/runs
// tables on every call, never read off the unit's nominate-time snapshot
// (that snapshot is only the review-time audit record, learn/knowledge_units.go
// StatsSnapshot doc comment). F5: only state=published is eligible — retired/
// superseded/adopted/measured units are excluded from suggest entirely.

// UnitSuggestion is one ranked candidate for the agent-detail suggest card.
// No leaderboard/per-person score anywhere on this struct — Delta/Overlap are
// fleet-level correlational signals, not an agent ranking.
type UnitSuggestion struct {
	UnitID      int64
	Kind        string
	Name        string
	Title       string
	Layer       string
	LayerTarget string

	// Live-recomputed present/absent stats (F11) — never the stored snapshot.
	NPresent, NAbsent        int
	DonePresent, DoneAbsent  float64
	CIPresentLo, CIPresentHi int
	CIAbsentLo, CIAbsentHi   int

	Delta          float64 // DonePresent - DoneAbsent, the ranking signal (not a score to compare people)
	KeywordOverlap int
}

// SuggestUnitsForAgent ranks up to n published knowledge units (kind ∈
// context/skill/playbook — F16: tool-pattern is already surfaced as kind
// context, so no separate "tool-pattern" kind exists here) that agentID has
// NOT already used, by keyword overlap between the agent's own task history
// and the unit's provenance/title, further ranked by live-recomputed
// delta×overlap. An agent with zero task history or zero qualifying
// candidates gets an empty slice — the no-data signal (same contract as
// SuggestAgents), never a fabricated entry.
func SuggestUnitsForAgent(st *store.Store, agentID string, n int) ([]UnitSuggestion, error) {
	if n <= 0 {
		n = 5
	}
	if agentID == "" {
		return nil, nil
	}

	keywords, err := agentTaskKeywords(st, agentID)
	if err != nil {
		return nil, err
	}
	if len(keywords) == 0 {
		return nil, nil
	}

	units, err := publishedSuggestableUnits(st)
	if err != nil {
		return nil, err
	}
	if len(units) == 0 {
		return nil, nil
	}

	usedSkills, err := agentUsedSkills(st, agentID)
	if err != nil {
		return nil, err
	}
	usedContextLayers, err := agentInjectedLayers(st, agentID)
	if err != nil {
		return nil, err
	}

	var out []UnitSuggestion
	for _, u := range units {
		if alreadyUsed(u, agentID, usedSkills, usedContextLayers) {
			continue
		}
		overlap := keywordOverlap(keywords, extractKeywords(u.Title+" "+u.Name))
		if overlap == 0 {
			continue
		}

		sug := UnitSuggestion{
			UnitID: u.ID, Kind: u.Kind, Name: u.Name, Title: u.Title,
			Layer: u.Layer, LayerTarget: u.LayerTarget,
			KeywordOverlap: overlap,
		}

		// F11: recompute live for kinds with a present/absent contrast
		// (skill has a direct usage query; context/playbook fall back to the
		// unit's own current stored counts — no separate live re-query shape
		// exists for those kinds yet, same limitation handleKnowledgeUnit
		// already has for the unit-detail page).
		if u.Kind == KindSkill {
			present, absent, err := liveSkillStats(st, u.Name)
			if err != nil {
				return nil, err
			}
			sug.NPresent, sug.DonePresent, sug.CIPresentLo, sug.CIPresentHi = present.n, doneRate(present), wilsonLo(present), wilsonHi(present)
			sug.NAbsent, sug.DoneAbsent, sug.CIAbsentLo, sug.CIAbsentHi = absent.n, doneRate(absent), wilsonLo(absent), wilsonHi(absent)
		} else {
			sug.NPresent, sug.DonePresent = u.NPresent, u.DonePresent
			sug.NAbsent, sug.DoneAbsent = u.NAbsent, u.DoneAbsent
			sug.CIPresentLo, sug.CIPresentHi = u.CIPresentLo, u.CIPresentHi
			sug.CIAbsentLo, sug.CIAbsentHi = u.CIAbsentLo, u.CIAbsentHi
		}
		sug.Delta = sug.DonePresent - sug.DoneAbsent

		out = append(out, sug)
	}
	if len(out) == 0 {
		return nil, nil
	}

	sort.Slice(out, func(i, j int) bool {
		si := out[i].Delta * float64(out[i].KeywordOverlap)
		sj := out[j].Delta * float64(out[j].KeywordOverlap)
		if si != sj {
			return si > sj
		}
		return out[i].UnitID < out[j].UnitID // deterministic tie-break
	})
	if len(out) > n {
		out = out[:n]
	}
	return out, nil
}

// suggestableUnit is the minimal projection of knowledge_units this file
// needs — narrower than the full KnowledgeUnit (keeps the suggest query
// self-contained without pulling in body/hash/provenance columns it never
// uses).
type suggestableUnit struct {
	ID                       int64
	Kind, Name, Title        string
	Layer, LayerTarget       string
	NPresent, NAbsent        int
	DonePresent, DoneAbsent  float64
	CIPresentLo, CIPresentHi int
	CIAbsentLo, CIAbsentHi   int
}

// publishedSuggestableUnits loads state=published units of kind
// context/skill/playbook (F16 — rule is a compliance mechanism, not a
// "suggest to try" surface, so it is excluded here). F5: published only —
// adopted/measured/retired/superseded/rejected/nominated/in_review are never
// suggest candidates.
func publishedSuggestableUnits(st *store.Store) ([]suggestableUnit, error) {
	rows, err := st.Read().Query(`
		SELECT id, kind, name, title, COALESCE(layer,''), COALESCE(layer_target,''),
			COALESCE(n_present,0), COALESCE(n_absent,0),
			COALESCE(done_present,0), COALESCE(done_absent,0),
			COALESCE(ci_present_lo,0), COALESCE(ci_present_hi,0),
			COALESCE(ci_absent_lo,0), COALESCE(ci_absent_hi,0)
		FROM knowledge_units
		WHERE state = 'published' AND kind IN ('context','skill','playbook')`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []suggestableUnit
	for rows.Next() {
		var u suggestableUnit
		if err := rows.Scan(&u.ID, &u.Kind, &u.Name, &u.Title, &u.Layer, &u.LayerTarget,
			&u.NPresent, &u.NAbsent, &u.DonePresent, &u.DoneAbsent,
			&u.CIPresentLo, &u.CIPresentHi, &u.CIAbsentLo, &u.CIAbsentHi); err != nil {
			return nil, err
		}
		out = append(out, u)
	}
	return out, rows.Err()
}

// agentTaskKeywords extracts keywords from every work_items title joined to
// this agent's runs via task_key — the "agent's own task history" half of
// the match (reuses extractKeywords, assignment_suggest.go).
func agentTaskKeywords(st *store.Store, agentID string) ([]string, error) {
	rows, err := st.Read().Query(`
		SELECT DISTINCT wi.title FROM runs r
		JOIN work_items wi ON wi.key = r.task_key
		WHERE r.agent_id = ? AND r.task_key IS NOT NULL AND r.task_key != ''`, agentID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	seen := map[string]bool{}
	var out []string
	for rows.Next() {
		var title string
		if err := rows.Scan(&title); err != nil {
			return nil, err
		}
		for _, kw := range extractKeywords(title) {
			if !seen[kw] {
				seen[kw] = true
				out = append(out, kw)
			}
		}
	}
	return out, rows.Err()
}

// keywordOverlap counts keywords common to both sets (order-independent).
func keywordOverlap(agentKeywords, unitKeywords []string) int {
	set := map[string]bool{}
	for _, k := range agentKeywords {
		set[k] = true
	}
	n := 0
	for _, k := range unitKeywords {
		if set[k] {
			n++
		}
	}
	return n
}

// agentUsedSkills reports which skill names this agent has already invoked
// (tool_use/Skill event, any run, any time) — used to exclude a skill unit
// the agent already uses from suggest (spec: "Agent đã dùng S → KHÔNG suggest").
func agentUsedSkills(st *store.Store, agentID string) (map[string]bool, error) {
	rows, err := st.Read().Query(`
		SELECT DISTINCT json_extract(e.payload,'$.skill') FROM events e
		JOIN runs r ON r.id = e.run_id
		WHERE r.agent_id = ? AND e.kind = 'tool_use' AND e.tool_name = 'Skill'
		  AND json_extract(e.payload,'$.skill') IS NOT NULL`, agentID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[string]bool{}
	for rows.Next() {
		var skill string
		if err := rows.Scan(&skill); err != nil {
			return nil, err
		}
		out[skill] = true
	}
	return out, rows.Err()
}

// agentInjectedLayers reports which (layer,layer_target) pairs are already
// part of this agent's effective context (company always-on, its resolved
// team, and its own agent layer) — a context-kind unit targeting a layer the
// agent already receives is "already used," never suggested (avoids
// double-counting what context_roi already measures, F4/Q1 same rationale as
// the "no adopt button for context" UI rule).
func agentInjectedLayers(st *store.Store, agentID string) (map[[2]string]bool, error) {
	out := map[[2]string]bool{
		{contexthubLayerCompany, contexthubCompanyTarget}: true,
		{contexthubLayerAgent, agentID}:                   true,
	}
	var teamID string
	err := st.Read().QueryRow(`SELECT team_id FROM team_members
		WHERE member_type = 'agent' AND member_id = ?
		ORDER BY CAST(team_id AS INTEGER) ASC LIMIT 1`, agentID).Scan(&teamID)
	if err == nil && teamID != "" {
		out[[2]string{contexthubLayerTeam, teamID}] = true
	}
	return out, nil
}

// contexthub layer literals duplicated here (2 short constants) rather than
// importing internal/contexthub — that package does not import learn (learn
// already sits above contexthub in the dependency graph via other packages),
// duplicating the literal is far cheaper than reversing the import direction
// for three constant strings.
const (
	contexthubLayerCompany  = "company"
	contexthubLayerTeam     = "team"
	contexthubLayerAgent    = "agent"
	contexthubCompanyTarget = "*"
)

// alreadyUsed applies the per-kind "agent already has this" exclusion (F16):
// skill → any Skill tool-use event naming it; context → the unit's target
// layer is already part of the agent's effective context; playbook → no
// direct "used" signal exists yet (adoptions table only records explicit
// clicks, not implicit "already knows this" state), so a playbook is never
// excluded on this basis alone.
func alreadyUsed(u suggestableUnit, agentID string, usedSkills map[string]bool, injectedLayers map[[2]string]bool) bool {
	switch u.Kind {
	case KindSkill:
		return usedSkills[u.Name]
	case KindContext:
		if u.Layer == "" {
			return false
		}
		target := u.LayerTarget
		if u.Layer == contexthubLayerCompany {
			target = contexthubCompanyTarget
		}
		return injectedLayers[[2]string{u.Layer, target}]
	default:
		return false
	}
}

// liveSkillStats recomputes present (runs that used skillName) vs absent
// (runs in the same window that did not) buckets FRESH from events/runs —
// F11: the suggest card must never trust the unit's nominate-time snapshot.
// Mirrors the query shape of detectSkillUsage (knowledge_detect.go) but
// collapsed across all projects (the suggest card is agent-facing, not
// project-segmented) and with no time window (an agent's own accumulated
// signal — a windowed floor is not part of the F11 contract, unlike the
// detector's MinSampleForKnowledge nominate-gate).
func liveSkillStats(st *store.Store, skillName string) (present, absent bucket, err error) {
	rows, err := st.Read().Query(`
		WITH used AS (
			SELECT DISTINCT e.run_id FROM events e
			WHERE e.kind='tool_use' AND e.tool_name='Skill'
			  AND json_extract(e.payload,'$.skill') = ?
		)
		SELECT r.id, r.status, r.cost_usd, (r.id IN (SELECT run_id FROM used)) AS is_present
		FROM runs r WHERE r.ended_at IS NOT NULL`, skillName)
	if err != nil {
		return bucket{}, bucket{}, err
	}
	defer rows.Close()
	for rows.Next() {
		var id, status string
		var cost float64
		var isPresent int
		if err := rows.Scan(&id, &status, &cost, &isPresent); err != nil {
			return bucket{}, bucket{}, err
		}
		if isPresent != 0 {
			present.add(status == "done", cost)
		} else {
			absent.add(status == "done", cost)
		}
	}
	return present, absent, rows.Err()
}

func doneRate(b bucket) float64 {
	if b.n == 0 {
		return 0
	}
	return float64(b.done) / float64(b.n)
}

func wilsonLo(b bucket) int { lo, _ := WilsonPct(b.done, b.n); return lo }
func wilsonHi(b bucket) int { _, hi := WilsonPct(b.done, b.n); return hi }
