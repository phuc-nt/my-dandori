package learn

import (
	"database/sql"
	"fmt"

	"github.com/phuc-nt/dandori/internal/store"
)

// Operator and team rollups. One-pass SQL over runs+events — no per-run
// loop (the v2 leaderboard N+1 lesson).

// OperatorCaveat travels with every operator rollup wherever it is shown.
// These are PRIVATE coaching signals: steering an agent mid-run is often
// exactly the right call, so a high number is a conversation starter for a
// tech lead — never a ranking, never published (red-team H5).
const OperatorCaveat = "Chỉ số riêng tư cho coaching — can thiệp giữa run nhiều khi là điều ĐÚNG, không dùng để xếp hạng người."

// RegressionToMeanCaveat (F10) travels with every before/after adoption
// outcome surface — a single before/after delta can move either direction
// on its own, independent of the knowledge unit's real effect, so it is an
// observation to discuss, never a causal conclusion on its own.
const RegressionToMeanCaveat = "Quan sát — không phải kết luận (có thể do hồi quy về trung bình)."

type OperatorBehavior struct {
	OperatorID  string
	Runs        int
	CostUSD     float64
	DoneRate    float64 // done / ended
	SteeringAvg float64 // mid-run msgs per run
	ErrorAvg    float64 // tool errors per run
	PermAskAvg  float64
	Abandoned   int
	Private     bool // always true — see OperatorCaveat
}

// aggregateBehaviorSQL rolls up runs joined with their numeric events for
// one grouping column. The events subqueries stay 1-row-per-run (SET
// semantics of user_msg guarantees it; counts aggregate the rest).
const aggregateBehaviorSQL = `
SELECT %[1]s,
       count(*),
       COALESCE(sum(r.cost_usd), 0),
       COALESCE(avg(CASE WHEN r.status IN ('done','failed','killed')
                         THEN CASE WHEN r.status = 'done' THEN 1.0 ELSE 0.0 END END), 0),
       COALESCE(avg((SELECT CAST(e.payload AS REAL) FROM events e
                     WHERE e.run_id = r.id AND e.kind = 'user_msg')), 0),
       COALESCE(avg((SELECT count(*) FROM events e
                     WHERE e.run_id = r.id AND e.kind = 'tool_result' AND e.ok = 0)), 0),
       COALESCE(avg((SELECT count(*) FROM events e
                     WHERE e.run_id = r.id AND e.kind = 'permission_ask')), 0),
       COALESCE(sum(CASE WHEN r.status = 'running' AND r.ended_at IS NULL
                          AND r.started_at < datetime('now', '-6 hours') THEN 1 ELSE 0 END), 0)
FROM runs r
WHERE %[2]s AND r.started_at >= %[3]s
GROUP BY %[1]s`

// OperatorBehaviorAgg aggregates one operator's runs over the window.
func OperatorBehaviorAgg(st *store.Store, operatorID string, windowDays int) (*OperatorBehavior, error) {
	q := fmt.Sprintf(aggregateBehaviorSQL, "r.operator_id", "r.operator_id = ?", windowClause(windowDays))
	ob := &OperatorBehavior{OperatorID: operatorID, Private: true}
	err := st.Read().QueryRow(q, operatorID).Scan(&ob.OperatorID, &ob.Runs, &ob.CostUSD,
		&ob.DoneRate, &ob.SteeringAvg, &ob.ErrorAvg, &ob.PermAskAvg, &ob.Abandoned)
	if err == sql.ErrNoRows {
		return ob, nil // no runs in window — zero-valued rollup
	}
	return ob, err
}

// TeamBehavior is a team's side of the team-vs-team comparison. It unions
// the team's operators (runs they drove) and agents (runs they executed).
type TeamBehavior struct {
	TeamID      int64
	Name        string
	Operators   int
	Agents      int
	Runs        int
	CostUSD     float64
	DoneRate    float64
	SteeringAvg float64
	ErrorAvg    float64
	PermAskAvg  float64
	Abandoned   int
	LinesAdded  int
	LinesRmed   int
}

// TeamBehaviorAgg aggregates a team over the window in one pass.
func TeamBehaviorAgg(st *store.Store, teamID int64, windowDays int) (*TeamBehavior, error) {
	tb := &TeamBehavior{TeamID: teamID}
	if err := st.Read().QueryRow(`SELECT name FROM teams WHERE id = ?`, teamID).Scan(&tb.Name); err != nil {
		return nil, err
	}
	st.Read().QueryRow(`SELECT count(*) FROM team_members WHERE team_id = ? AND member_type = 'operator'`, teamID).Scan(&tb.Operators)
	st.Read().QueryRow(`SELECT count(*) FROM team_members WHERE team_id = ? AND member_type = 'agent'`, teamID).Scan(&tb.Agents)

	member := `(r.operator_id IN (SELECT member_id FROM team_members WHERE team_id = ?1 AND member_type = 'operator')
	         OR r.agent_id   IN (SELECT member_id FROM team_members WHERE team_id = ?1 AND member_type = 'agent'))`
	q := fmt.Sprintf(aggregateBehaviorSQL, "?1", member, windowClause(windowDays))
	var group int64
	err := st.Read().QueryRow(q, teamID).Scan(&group, &tb.Runs, &tb.CostUSD,
		&tb.DoneRate, &tb.SteeringAvg, &tb.ErrorAvg, &tb.PermAskAvg, &tb.Abandoned)
	if err == sql.ErrNoRows {
		return tb, nil // team without runs yet
	}
	if err != nil {
		return nil, err
	}
	st.Read().QueryRow(`SELECT COALESCE(sum(lines_added),0), COALESCE(sum(lines_deleted),0) FROM runs r
		WHERE `+member+` AND r.started_at >= `+windowClause(windowDays), teamID).
		Scan(&tb.LinesAdded, &tb.LinesRmed)
	return tb, nil
}

// TeamCompare returns every team's rollup, ready for side-by-side display.
func TeamCompare(st *store.Store, windowDays int) ([]TeamBehavior, error) {
	teams, err := st.ListTeams()
	if err != nil {
		return nil, err
	}
	out := make([]TeamBehavior, 0, len(teams))
	for _, t := range teams {
		tb, err := TeamBehaviorAgg(st, t.ID, windowDays)
		if err != nil {
			return nil, err
		}
		out = append(out, *tb)
	}
	return out, nil
}
