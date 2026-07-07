package learn

import "github.com/phuc-nt/dandori/internal/store"

// DigestData is the shared fleet snapshot consumed by every delivery
// channel (Sheets export, Slack digest, Gmail digest) so the numbers are
// computed exactly once and never drift between channels.
type DigestData struct {
	WindowDays int
	Board      []LeaderboardRow
	FleetROI   *ROI
	CFR        Metric
	Spiked     []string // agent ids that triggered a cost-spike today
	TotalCost  float64
	TotalRuns  int

	// KnowledgePublishedCount/Titles (P4, §5): "team publish N practice mới
	// this week" — count + titles ONLY, deliberately NO actor/contributor
	// ranking table (spec §5.1: attribution = source name only, never a
	// leaderboard of who-published-most).
	KnowledgePublishedCount  int
	KnowledgePublishedTitles []string
}

// BuildDigestData composes the existing LEARN query surface (leaderboard,
// fleet-wide ROI, CFR, spike detection) into one struct. Reused by both the
// Sheets rows and the Slack/Gmail text — no duplicated aggregation SQL. An
// empty fleet yields zero totals and nil/empty slices, never a panic.
func BuildDigestData(st *store.Store, windowDays int) (*DigestData, error) {
	board, err := Leaderboard(st, windowDays)
	if err != nil {
		return nil, err
	}
	var totalCost float64
	var totalRuns int
	var sumAcceptance float64
	for _, row := range board {
		totalCost += row.CostUSD
		totalRuns += row.Runs
		sumAcceptance += row.Metrics.Acceptance.Value
	}
	avgAcceptance := 100.0
	if len(board) > 0 {
		avgAcceptance = sumAcceptance / float64(len(board))
	}
	fleetROI, err := ComputeROI(st, "", windowDays, avgAcceptance)
	if err != nil {
		return nil, err
	}
	cfr, err := CFR(st, windowDays)
	if err != nil {
		return nil, err
	}
	spiked, err := DetectSpikes(st)
	if err != nil {
		return nil, err
	}
	pubCount, pubTitles, err := KnowledgePublishedThisWeek(st, windowDays)
	if err != nil {
		return nil, err
	}
	return &DigestData{
		WindowDays: windowDays,
		Board:      board,
		FleetROI:   fleetROI,
		CFR:        cfr,
		Spiked:     spiked,
		TotalCost:  totalCost,
		TotalRuns:  totalRuns,

		KnowledgePublishedCount:  pubCount,
		KnowledgePublishedTitles: pubTitles,
	}, nil
}

// KnowledgePublishedThisWeek counts knowledge_units that transitioned INTO
// state=published within the last `days` days, plus their titles for the
// digest line "Tuần này team publish N practice mới." Reads knowledge_
// transitions (to_state='published') rather than knowledge_units.updated_at
// directly — a unit can move published→adopted→measured afterward, and
// updated_at would then reflect that LATER transition, not the original
// publish moment, silently dropping it out of the week it actually
// published in. NO actor/contributor breakdown (spec §5): count + titles
// only, ever.
func KnowledgePublishedThisWeek(st *store.Store, days int) (count int, titles []string, err error) {
	rows, err := st.Read().Query(`
		SELECT u.title FROM knowledge_transitions t
		JOIN knowledge_units u ON u.id = t.unit_id
		WHERE t.to_state = 'published'`+insightWindowClauseCol("t.at", days)+`
		ORDER BY t.at DESC`)
	if err != nil {
		return 0, nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var title string
		if err := rows.Scan(&title); err != nil {
			return 0, nil, err
		}
		titles = append(titles, title)
		count++
	}
	return count, titles, rows.Err()
}
