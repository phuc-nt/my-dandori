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
	return &DigestData{
		WindowDays: windowDays,
		Board:      board,
		FleetROI:   fleetROI,
		CFR:        cfr,
		Spiked:     spiked,
		TotalCost:  totalCost,
		TotalRuns:  totalRuns,
	}, nil
}
