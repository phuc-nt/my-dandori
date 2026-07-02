package learn

import (
	"fmt"
	"strconv"

	"github.com/phuc-nt/dandori/internal/store"
)

const editTools = `'Write','Edit','NotebookEdit'`

// acceptance: share of edit tool calls whose work survived. Rejected =
// (a) edit tool_result ok=0 / guardrail block on an edit tool, plus
// (b) every edit belonging to a run whose commits were later reverted
// (revert_detected events from git — a real signal, not a proxy).
func acceptance(st *store.Store, agentID string, days int) (Metric, error) {
	edits, err := eventIDs(st, agentID, days, `'tool_use'`, ` AND e.tool_name IN (`+editTools+`)`)
	if err != nil {
		return Metric{}, err
	}
	rejected, err := eventIDs(st, agentID, days, `'tool_result','guardrail_block'`,
		` AND e.tool_name IN (`+editTools+`) AND e.ok = 0`)
	if err != nil {
		return Metric{}, err
	}
	revertedEdits, err := eventIDs(st, agentID, days, `'tool_use'`,
		` AND e.tool_name IN (`+editTools+`) AND EXISTS (
			SELECT 1 FROM events rv WHERE rv.run_id = e.run_id AND rv.kind = 'revert_detected')`)
	if err != nil {
		return Metric{}, err
	}
	rejectedSet := map[int64]bool{}
	for _, id := range rejected {
		rejectedSet[id] = true
	}
	for _, id := range revertedEdits {
		rejectedSet[id] = true
	}
	m := Metric{Name: "acceptance", Value: 100, EventIDs: edits,
		Formula: "no edit tool calls in window (neutral 100)"}
	if len(edits) > 0 {
		m.Value = 100 * (1 - float64(len(rejectedSet))/float64(len(edits)))
		m.Formula = fmt.Sprintf("100 × (1 − %d rejected / %d edits) — rejected = %d user/guardrail rejections ∪ %d edits in git-reverted runs",
			len(rejectedSet), len(edits), len(rejected), len(revertedEdits))
		m.EventIDs = append(edits, rejected...)
		m.EventIDs = append(m.EventIDs, revertedEdits...)
	}
	return m, nil
}

// success: done runs over ended runs. Runs with a Jira task key count as done
// only when the linked work item reached a done status.
func success(st *store.Store, agentID string, days int) (Metric, error) {
	rows, err := st.DB.Query(`SELECT r.id, r.status, COALESCE(r.task_key,''),
			COALESCE((SELECT count(*) FROM flags f WHERE f.run_id = r.id AND f.status = 'open'), 0),
			COALESCE((SELECT w.status FROM work_items w WHERE w.source='jira' AND w.key = r.task_key), '')
		FROM runs r WHERE r.agent_id = ? AND r.started_at >= `+windowClause(days)+
		` AND r.status != 'running'`, agentID)
	if err != nil {
		return Metric{}, err
	}
	defer rows.Close()
	var ended, done int
	var ids []string
	for rows.Next() {
		var id, status, taskKey, jiraStatus string
		var openFlags int
		if err := rows.Scan(&id, &status, &taskKey, &openFlags, &jiraStatus); err != nil {
			return Metric{}, err
		}
		ended++
		ids = append(ids, id)
		switch {
		case taskKey != "" && jiraStatus != "":
			if isDoneStatus(jiraStatus) {
				done++
			}
		case status == "done" && openFlags == 0:
			done++
		}
	}
	m := Metric{Name: "success", Value: 100, RunIDs: ids,
		Formula: "no ended runs in window (neutral 100)"}
	if ended > 0 {
		m.Value = 100 * float64(done) / float64(ended)
		m.Formula = fmt.Sprintf("100 × %d done / %d ended runs (Jira status when task linked, else clean finish without open flags)", done, ended)
	}
	return m, rows.Err()
}

func isDoneStatus(s string) bool {
	switch s {
	case "Done", "done", "Closed", "closed", "Resolved", "resolved":
		return true
	}
	return false
}

// autonomy: share of runs that finished without human intervention.
// Intervention = a permission ask, or a user message sent MID-RUN (after the
// agent already started working). The opening prompt does not count.
func autonomy(st *store.Store, agentID string, days int) (Metric, error) {
	rows, err := st.DB.Query(`SELECT r.id,
			COALESCE((SELECT count(*) FROM events e WHERE e.run_id = r.id AND e.kind = 'permission_ask'), 0),
			COALESCE((SELECT e.payload FROM events e WHERE e.run_id = r.id AND e.kind = 'user_msg'), '0')
		FROM runs r WHERE r.agent_id = ? AND r.started_at >= `+windowClause(days), agentID)
	if err != nil {
		return Metric{}, err
	}
	defer rows.Close()
	var total, intervened int
	var ids []string
	for rows.Next() {
		var id, midRunMsgs string
		var asks int
		if err := rows.Scan(&id, &asks, &midRunMsgs); err != nil {
			return Metric{}, err
		}
		total++
		ids = append(ids, id)
		n, _ := strconv.Atoi(midRunMsgs)
		if asks > 0 || n > 0 {
			intervened++
		}
	}
	m := Metric{Name: "autonomy", Value: 100, RunIDs: ids,
		Formula: "no runs in window (neutral 100)"}
	if total > 0 {
		m.Value = 100 * (1 - float64(intervened)/float64(total))
		m.Formula = fmt.Sprintf("100 × (1 − %d intervened / %d runs) — intervention = permission ask or mid-run user message (opening prompt not counted)", intervened, total)
	}
	return m, rows.Err()
}

// reliability: 100 × (1 − mean(tool error rate, guardrail block rate, kill rate)).
func reliability(st *store.Store, agentID string, days int) (Metric, error) {
	var results, errors, uses, blocks, runs, kills int
	w := windowClause(days)
	base := `FROM events e JOIN runs r ON r.id = e.run_id WHERE r.agent_id = ? AND r.started_at >= ` + w
	if err := st.DB.QueryRow(`SELECT count(*), COALESCE(sum(CASE WHEN e.ok=0 THEN 1 ELSE 0 END),0) `+
		base+` AND e.kind='tool_result'`, agentID).Scan(&results, &errors); err != nil {
		return Metric{}, err
	}
	if err := st.DB.QueryRow(`SELECT count(*) `+base+` AND e.kind='tool_use'`, agentID).Scan(&uses); err != nil {
		return Metric{}, err
	}
	if err := st.DB.QueryRow(`SELECT count(*) `+base+` AND e.kind='guardrail_block' AND e.ok=0`, agentID).Scan(&blocks); err != nil {
		return Metric{}, err
	}
	if err := st.DB.QueryRow(`SELECT count(*), COALESCE(sum(CASE WHEN status='killed' THEN 1 ELSE 0 END),0)
		FROM runs WHERE agent_id = ? AND started_at >= `+w, agentID).Scan(&runs, &kills); err != nil {
		return Metric{}, err
	}
	rate := func(n, d int) float64 {
		if d == 0 {
			return 0
		}
		return float64(n) / float64(d)
	}
	er, br, kr := rate(errors, results), rate(blocks, uses), rate(kills, runs)
	ids, err := eventIDs(st, agentID, days, `'tool_result','guardrail_block','kill'`, ``)
	if err != nil {
		return Metric{}, err
	}
	return Metric{
		Name:  "reliability",
		Value: 100 * (1 - (er+br+kr)/3),
		Formula: fmt.Sprintf("100 × (1 − mean(err %d/%d, block %d/%d, kill %d/%d))",
			errors, results, blocks, uses, kills, runs),
		EventIDs: ids,
	}, nil
}
