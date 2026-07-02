package chat

import (
	"encoding/json"
	"fmt"

	"github.com/phuc-nt/dandori/internal/config"
	"github.com/phuc-nt/dandori/internal/govern"
	"github.com/phuc-nt/dandori/internal/learn"
	"github.com/phuc-nt/dandori/internal/observer"
	"github.com/phuc-nt/dandori/internal/store"
)

// Tool registry. Read tools call internal Go functions over the read pool —
// the model never writes SQL. Action tools create an approval REQUEST via
// observer.RequestAction and nothing else; there is deliberately NO tool
// that approves, decides, or consumes an approval — deciding is a human
// click on the console. Tool results are UNTRUSTED input to the model.

type Tool struct {
	Name        string
	Description string
	Params      map[string]param
	Action      bool
	run         func(st *store.Store, cfg *config.Config, args map[string]any, principal string) (any, int64, error)
}

type param struct {
	Type     string // string | number
	Desc     string
	Required bool
}

// Registry returns the CEO toolset.
func Registry() []Tool {
	return []Tool{
		{
			Name:        "get_fleet_stats",
			Description: "Tổng quan đội AI: số run, chi phí, tỉ lệ hoàn thành trong N ngày gần nhất.",
			Params:      map[string]param{"days": {Type: "number", Desc: "cửa sổ ngày, mặc định 7"}},
			run:         runFleetStats,
		},
		{
			Name:        "get_leaderboard",
			Description: "Bảng xếp hạng agent: hạng A-F, chi phí, số run của từng agent.",
			Params:      map[string]param{},
			run:         runLeaderboard,
		},
		{
			Name:        "compare_teams",
			Description: "So sánh các team: số run, chi phí, tỉ lệ hoàn thành, mức can thiệp.",
			Params:      map[string]param{},
			run:         runCompareTeams,
		},
		{
			Name:        "get_run_detail",
			Description: "Chi tiết một run: trạng thái, chi phí, hành vi (lỗi, can thiệp).",
			Params:      map[string]param{"run_id": {Type: "string", Desc: "mã run", Required: true}},
			run:         runRunDetail,
		},
		{
			Name:        "list_insights",
			Description: "Các phát hiện & đề xuất đang chờ trong hộp hành động của CEO.",
			Params:      map[string]param{},
			run:         runListInsights,
		},
		{
			Name:        "request_kill",
			Description: "TẠO YÊU CẦU dừng một run (không tự dừng — cần CEO bấm duyệt trong console).",
			Params:      map[string]param{"run_id": {Type: "string", Desc: "mã run cần dừng", Required: true}},
			Action:      true,
			run:         runRequestKill,
		},
		{
			Name:        "request_set_budget",
			Description: "TẠO YÊU CẦU đổi ngân sách tháng (không tự đổi — cần CEO duyệt trong console).",
			Params: map[string]param{
				"limit_usd": {Type: "number", Desc: "hạn mức mới (USD)", Required: true},
			},
			Action: true,
			run:    runRequestBudget,
		},
		{
			Name:        "request_set_band",
			Description: "TẠO YÊU CẦU đổi mức tự chủ của agent: supervised, gated hoặc trusted (cần CEO duyệt).",
			Params: map[string]param{
				"agent_id": {Type: "string", Desc: "mã agent", Required: true},
				"band":     {Type: "string", Desc: "supervised | gated | trusted", Required: true},
			},
			Action: true,
			run:    runRequestBand,
		},
	}
}

// Dispatch validates args and runs one tool. Returns (JSON result string,
// approval id if an action request was created).
func Dispatch(st *store.Store, cfg *config.Config, name string, rawArgs string, principal string) (string, int64) {
	var tool *Tool
	reg := Registry()
	for i := range reg {
		if reg[i].Name == name {
			tool = &reg[i]
			break
		}
	}
	if tool == nil {
		return errJSON("tool không tồn tại: " + name), 0
	}
	var args map[string]any
	if rawArgs != "" {
		if err := json.Unmarshal([]byte(rawArgs), &args); err != nil {
			return errJSON("tham số không phải JSON hợp lệ"), 0
		}
	}
	if msg := validateArgs(tool, args); msg != "" {
		return errJSON(msg), 0
	}
	out, approvalID, err := tool.run(st, cfg, args, principal)
	if err != nil {
		return errJSON(err.Error()), 0
	}
	b, err := json.Marshal(out)
	if err != nil {
		return errJSON("không mã hoá được kết quả"), 0
	}
	return string(b), approvalID
}

func validateArgs(t *Tool, args map[string]any) string {
	for name, p := range t.Params {
		v, ok := args[name]
		if !ok {
			if p.Required {
				return "thiếu tham số bắt buộc: " + name
			}
			continue
		}
		switch p.Type {
		case "string":
			if _, ok := v.(string); !ok {
				return "tham số " + name + " phải là chuỗi"
			}
		case "number":
			if _, ok := v.(float64); !ok {
				return "tham số " + name + " phải là số"
			}
		}
	}
	for name := range args {
		if _, ok := t.Params[name]; !ok {
			return "tham số lạ: " + name
		}
	}
	return ""
}

func errJSON(msg string) string {
	b, _ := json.Marshal(map[string]string{"error": msg})
	return string(b)
}

// ---- read tools ----

func runFleetStats(st *store.Store, cfg *config.Config, args map[string]any, _ string) (any, int64, error) {
	days := 7
	if d, ok := args["days"].(float64); ok && d > 0 && d <= 365 {
		days = int(d)
	}
	var runs, agents int
	var cost float64
	var doneRate float64
	err := st.Read().QueryRow(fmt.Sprintf(`SELECT count(*), COALESCE(sum(cost_usd),0), count(DISTINCT agent_id),
		COALESCE(avg(CASE WHEN status IN ('done','failed','killed') THEN CASE WHEN status='done' THEN 1.0 ELSE 0.0 END END), 0)
		FROM runs WHERE started_at >= datetime('now', '-%d days')`, days)).
		Scan(&runs, &cost, &agents, &doneRate)
	if err != nil {
		return nil, 0, err
	}
	return map[string]any{"window_days": days, "runs": runs, "cost_usd": cost,
		"active_agents": agents, "done_rate_pct": doneRate * 100}, 0, nil
}

func runLeaderboard(st *store.Store, cfg *config.Config, _ map[string]any, _ string) (any, int64, error) {
	board, err := learn.LeaderboardCalibrated(st, cfg.LearnWindowDays, cfg.CalibrateWithHumans)
	if err != nil {
		return nil, 0, err
	}
	type row struct {
		Agent   string  `json:"agent"`
		Grade   string  `json:"grade"`
		Runs    int     `json:"runs"`
		CostUSD float64 `json:"cost_usd"`
		LowData bool    `json:"low_confidence"`
	}
	out := make([]row, 0, len(board))
	for _, r := range board {
		out = append(out, row{r.AgentName, r.Grade.Letter, r.Runs, r.CostUSD, r.Grade.LowConfidence})
	}
	return out, 0, nil
}

func runCompareTeams(st *store.Store, cfg *config.Config, _ map[string]any, _ string) (any, int64, error) {
	cmp, err := learn.TeamCompare(st, cfg.LearnWindowDays)
	if err != nil {
		return nil, 0, err
	}
	if len(cmp) == 0 {
		return map[string]string{"note": "chưa khai báo team nào — vào mục Teams để tạo"}, 0, nil
	}
	return cmp, 0, nil
}

func runRunDetail(st *store.Store, cfg *config.Config, args map[string]any, _ string) (any, int64, error) {
	runID, _ := args["run_id"].(string)
	var status, agent string
	var cost float64
	err := st.Read().QueryRow(`SELECT r.status, COALESCE(a.name, r.agent_id, ''), r.cost_usd
		FROM runs r LEFT JOIN agents a ON a.id = r.agent_id WHERE r.id = ?`, runID).
		Scan(&status, &agent, &cost)
	if err != nil {
		return nil, 0, fmt.Errorf("không tìm thấy run %s", runID)
	}
	b, err := learn.ComputeBehavior(st, runID)
	if err != nil {
		return nil, 0, err
	}
	return map[string]any{"run_id": runID, "agent": agent, "status": status, "cost_usd": cost,
		"steering": b.Steering, "tool_errors": b.ToolErrors, "perm_asks": b.PermAsks,
		"prompt_band": b.PromptBand}, 0, nil
}

func runListInsights(st *store.Store, _ *config.Config, _ map[string]any, _ string) (any, int64, error) {
	rows, err := st.Read().Query(`SELECT id, type, subject, summary, class, status FROM insights
		WHERE surface = 'ceo' AND status IN ('open','surfaced') ORDER BY id DESC LIMIT 20`)
	if err != nil {
		return nil, 0, err
	}
	defer rows.Close()
	type ins struct {
		ID                                   int64  `json:"id"`
		Type, Subject, Summary, Class, State string `json:"-"`
	}
	var out []map[string]any
	for rows.Next() {
		var i ins
		if err := rows.Scan(&i.ID, &i.Type, &i.Subject, &i.Summary, &i.Class, &i.State); err != nil {
			return nil, 0, err
		}
		out = append(out, map[string]any{"id": i.ID, "type": i.Type, "subject": i.Subject,
			"summary": i.Summary, "needs_approval": i.Class == "approval"})
	}
	return out, 0, rows.Err()
}

// ---- action tools: create-request ONLY ----

func runRequestKill(st *store.Store, _ *config.Config, args map[string]any, principal string) (any, int64, error) {
	runID, _ := args["run_id"].(string)
	var exists int
	st.Read().QueryRow(`SELECT count(*) FROM runs WHERE id = ?`, runID).Scan(&exists)
	if exists == 0 {
		return nil, 0, fmt.Errorf("không tìm thấy run %s", runID)
	}
	id, err := observer.RequestAction(st, "kill", runID,
		fmt.Sprintf("Yêu cầu dừng run %s (tạo từ trợ lý chat)", runID),
		map[string]any{"run_id": runID}, principal)
	if err != nil {
		return nil, 0, err
	}
	return pendingResult(id), id, nil
}

func runRequestBudget(st *store.Store, _ *config.Config, args map[string]any, principal string) (any, int64, error) {
	limit, _ := args["limit_usd"].(float64)
	if limit <= 0 || limit > 1_000_000 {
		return nil, 0, fmt.Errorf("hạn mức không hợp lệ")
	}
	id, err := observer.RequestAction(st, "budget", "global",
		fmt.Sprintf("Yêu cầu đổi ngân sách tháng thành $%.0f (tạo từ trợ lý chat)", limit),
		map[string]any{"scope_type": "global", "scope_id": "", "suggested_limit": limit}, principal)
	if err != nil {
		return nil, 0, err
	}
	return pendingResult(id), id, nil
}

func runRequestBand(st *store.Store, _ *config.Config, args map[string]any, principal string) (any, int64, error) {
	agentID, _ := args["agent_id"].(string)
	band, _ := args["band"].(string)
	if !govern.ValidBand(band) {
		return nil, 0, fmt.Errorf("band phải là supervised, gated hoặc trusted")
	}
	var exists int
	st.Read().QueryRow(`SELECT count(*) FROM agents WHERE id = ?`, agentID).Scan(&exists)
	if exists == 0 {
		return nil, 0, fmt.Errorf("không tìm thấy agent %s", agentID)
	}
	id, err := observer.RequestAction(st, "band", agentID,
		fmt.Sprintf("Yêu cầu chuyển agent %s sang mức %s (tạo từ trợ lý chat)", agentID, band),
		map[string]any{"agent_id": agentID, "band": band}, principal)
	if err != nil {
		return nil, 0, err
	}
	return pendingResult(id), id, nil
}

func pendingResult(approvalID int64) map[string]any {
	return map[string]any{
		"status":      "pending_approval",
		"approval_id": approvalID,
		"note":        "Đã tạo yêu cầu — chưa có gì thay đổi. CEO cần bấm duyệt trong mục Cần duyệt.",
	}
}
