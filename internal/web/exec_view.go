package web

import (
	"fmt"

	"github.com/phuc-nt/dandori/internal/learn"
	"github.com/phuc-nt/dandori/internal/store"
)

// Executive view assembly: turn the technical metrics into a Vietnamese,
// 5-second-glanceable picture for a non-technical CEO. All jargon is
// translated here so templates stay plain.

// Traffic-light thresholds on a team composite C ∈ [0,1]:
//
//	C = 0.5*ROI_norm + 0.3*doneRate + 0.2*budgetHealth
//
// green ≥ 0.66 · amber ≥ 0.40 · red below (or any hard breach) · grey = no data.
const (
	lightGreenMin = 0.66
	lightAmberMin = 0.40
)

type ExecView struct {
	ROIHeadline string
	ROITrend    string // "▲ tăng" | "▼ giảm" | "→ ổn định"
	ROIUp       bool
	TeamCards   []TeamCard
	Inbox       []InboxItem
	Insights    []string
	InboxCount  int
}

type TeamCard struct {
	Name    string
	Light   string // green | amber | red | grey
	OneLine string
	URL     string
}

type InboxItem struct {
	Kind       string // insight | approval
	ID         int64
	Summary    string
	ApproveURL string
	DismissURL string
}

// BuildExecView assembles everything the executive home renders.
func BuildExecView(st *store.Store, windowDays int) (*ExecView, error) {
	v := &ExecView{}
	v.ROIHeadline, v.ROITrend, v.ROIUp = fleetROIHeadline(st, windowDays)

	teams, err := learn.TeamCompare(st, windowDays)
	if err != nil {
		return nil, err
	}
	for _, t := range teams {
		v.TeamCards = append(v.TeamCards, teamCard(t))
	}

	// CEO inbox: business approvals + surfaced ceo insights ONLY. Operator-
	// surface (technical) items never appear here.
	rows, err := st.Read().Query(`SELECT id, type, subject, summary, class, COALESCE(approval_id,0)
		FROM insights WHERE surface = 'ceo' AND status IN ('open','surfaced') ORDER BY id DESC LIMIT 20`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var id, approvalID int64
		var typ, subject, summary, class string
		if err := rows.Scan(&id, &typ, &subject, &summary, &class, &approvalID); err != nil {
			return nil, err
		}
		if class == "approval" && approvalID > 0 {
			v.Inbox = append(v.Inbox, InboxItem{
				Kind: "approval", ID: approvalID, Summary: summary,
				ApproveURL: fmt.Sprintf("/exec/approve/%d", approvalID),
				DismissURL: fmt.Sprintf("/exec/insight/%d/dismiss", id),
			})
		} else {
			v.Insights = append(v.Insights, summary)
		}
	}
	v.InboxCount = len(v.Inbox)
	return v, nil
}

// fleetROIHeadline sums useful value across agents and phrases it plainly.
func fleetROIHeadline(st *store.Store, windowDays int) (headline, trend string, up bool) {
	board, err := learn.Leaderboard(st, windowDays)
	if err != nil || len(board) == 0 {
		return "Chưa đủ dữ liệu để tính giá trị AI mang lại.", "→ ổn định", true
	}
	var useful, total, trendDelta float64
	for _, r := range board {
		total += r.CostUSD
		if r.ROI != nil {
			useful += r.ROI.UsefulPct / 100 * r.CostUSD
		}
		trendDelta += r.TrendDelta
	}
	headline = fmt.Sprintf("Tuần qua công ty chi $%.0f cho AI; ước tính $%.0f tạo ra giá trị giữ lại được.", total, useful)
	switch {
	case trendDelta > 1:
		return headline, "▲ Hiệu quả đang tăng", true
	case trendDelta < -1:
		return headline, "▼ Hiệu quả đang giảm", false
	default:
		return headline, "→ Ổn định", true
	}
}

// teamCard computes the composite + traffic light + a one-line VI summary.
func teamCard(t learn.TeamBehavior) TeamCard {
	url := fmt.Sprintf("/dash/org?team=%d", t.TeamID)
	if t.Runs == 0 {
		return TeamCard{Name: t.Name, Light: "grey", URL: url,
			OneLine: "Chưa có hoạt động AI trong kỳ — chưa đủ dữ liệu."}
	}
	// budgetHealth left at 1 (no per-team budget in MVP); ROI_norm from done rate proxy.
	roiNorm := t.DoneRate
	budgetHealth := 1.0
	c := 0.5*roiNorm + 0.3*t.DoneRate + 0.2*budgetHealth

	light := "red"
	switch {
	case c >= lightGreenMin:
		light = "green"
	case c >= lightAmberMin:
		light = "amber"
	}
	pct := int(t.DoneRate * 100)
	one := fmt.Sprintf("%d run · %d%% hoàn thành · $%.0f", t.Runs, pct, t.CostUSD)
	switch light {
	case "green":
		one = "Đang chạy tốt — " + one
	case "amber":
		one = "Ổn nhưng cần theo dõi — " + one
	case "red":
		one = "Cần chú ý — " + one
	}
	return TeamCard{Name: t.Name, Light: light, OneLine: one, URL: url}
}
