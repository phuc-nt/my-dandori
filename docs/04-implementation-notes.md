# Implementation Notes — Dandori MVP

> Kiến trúc THẬT sau khi build (260702), quyết định chốt, giới hạn đã biết. Bổ sung cho [01-vision.md](01-vision.md) / [03-features.md](03-features.md) — không thay chúng.

## Kiến trúc

Một binary `dandori` (Go 1.26, pure-Go, no CGO), một file SQLite (WAL) tại `~/.dandori/dandori.db`.

```
cmd/dandori            entry
internal/config        defaults → YAML → env → .env (env thắng .env)
internal/store         SQLite + migrations (PRAGMA user_version) + testseed
internal/capture       hook ingest · transcript parse · attribution · watcher
internal/govern        guardrail engine · budget · gate · audit hash-chain · kill
internal/learn         4 metric · grade percentile · ROI · leaderboard · provenance · quality gate
internal/integrations  guard (DRY_RUN/AGENT_WRITE_DISABLED) · jira · slack · ghub
internal/web           chi + html/template + go:embed + HTMX console
internal/cli           cobra commands + serve workers
```

### CAPTURE — cách hoạt động thật

- `dandori init` merge 4 hooks vào `.claude/settings.json` (idempotent, giữ hooks sẵn có) + ghi `.dandori-agent` (agent/project attribution).
- `dandori hook <event>` đọc JSON stdin của Claude Code. **Fail-open**: lỗi nội bộ → stderr + exit 0, không bao giờ phá phiên người dùng.
- Token/cost: parse transcript JSONL (`~/.claude/projects/<cwd-encoded>/<session>.jsonl`), dòng `type=assistant` → `message.usage`, **dedup theo `message.id`, full-reparse + SET** (không cộng dồn incremental) → không thể đếm đôi. Reconcile throttle 10s/run ở post-tool, chốt ở Stop.
- Watcher (C7): quét transcript mtime mới, session chưa có run → tạo `source=watcher`; đã có → chỉ reconcile usage. Chạy trong serve (ticker 60s) + `dandori watch`.
- Cost = tokens × bảng giá trong config (`pricing`, USD/MTok, prefix-match model id, fallback `default`). Giá lệch thực tế → sửa config, không sửa code.

### GOVERN — thứ tự check (cố định)

`kill switch → sandbox (G2) → block rules (G1) → budget (G3) → gate (G4)` — hit đầu tiên thắng. **Fail-close** khi engine không eval được rule (ngược với capture). Mọi decision → event + audit entry (`hash = sha256(prev||ts||actor||action||subject||detail)`, `dandori audit verify` walk chain).

- Gate G4: tạo approval pending, poll DB 2s tối đa `gate_wait_seconds` (30s; hook timeout cài 40s). Approve trong lúc chờ → allow + consume; timeout/reject → deny kèm id. Web và Slack cùng quyết: **first-writer-wins** (UPDATE … WHERE status='pending').
- Sandbox G2 chỉ enforce cứng cho Write/Edit/NotebookEdit; path token trong Bash là heuristic (miss thì lọt — block rules đỡ các case nguy hiểm). Giới hạn ghi nhận, chấp nhận MVP.

### LEARN — proxy và giới hạn

| Metric | Công thức | Giới hạn proxy |
|---|---|---|
| Acceptance | 1 − rejected_edits/edits | chưa có revert-detection GitHub; reject signal chỉ từ tool_result ok=0 + guardrail block |
| Success | done/ended; có task_key → status Jira | run tương tác không link Jira dễ 100% |
| Autonomy | 1 − intervened_runs/runs (ask hoặc >1 user msg) | phiên interactive của người luôn bị tính intervened — đúng định nghĩa "supervised" nhưng làm autonomy=0 hàng loạt |
| Reliability | 1 − mean(err, block, kill rates) | — |

Grade: percentile composite trên fleet, A≥p80…F<p20; fleet <5 → static band + cờ `uncalibrated` (dấu `*`). ROI: 3 bucket loại trừ lẫn nhau (failed/killed → flagged → phần acceptance của clean) — không đếm đôi. Mọi metric trả `Formula` + run/event ids → `/provenance` drill về raw.

### Integrations

- **Cổng ghi duy nhất** `integrations.Guard`: `AGENT_WRITE_DISABLED` > `DRY_RUN` (mặc định true) > live. Skip → event `write_skipped`.
- Jira REST v3, basic auth; `/search/jql` fallback `/search`; site name nhận cả `phucnt0` lẫn `phucnt0.atlassian.net`. Flag→ticket: issuetype Task, label `dandori`, ADF description, key ghi ngược vào flag.
- Slack browser-token (xoxc bearer + cookie d=xoxd giữ nguyên URL-encode). Duyệt qua **reaction** (✅/❌, poll `reactions.get` 5s) vì browser-token không làm được interactive button. Reaction đầu tiên thắng, actor resolve qua `users.info`, ghi audit "via slack reaction". **Chưa có whitelist người react** — single-user MVP, RBAC [Sau].
- Alerts: events `budget_warn|guardrail_block|kill|flag` → post channel, dedup 2 tầng (per-event + per-topic-per-day) qua bảng `notifications`.
- GitHub: `gh pr list --json` → work_items (best effort). GWS: chưa dùng ở MVP.

## Quyết định chốt (từ câu hỏi mở của 03-features.md)

- Multi-runtime: chỉ Claude Code. G7: chạy lệnh check có sẵn (`gate_checks` config, chỉ đọc từ config local — không nhận từ web UI, tránh RCE). Kill switch: làm ở MVP. Điều khiển runtime: THẬT qua PreToolUse deny. RBAC: chưa, bind 127.0.0.1, actor = `user_name` config.

## E2E đã chạy thật (260702)

Chi tiết: `plans/reports/e2e-live-260702-dandori-mvp-report.md`. Tóm tắt: local 11/11 pass; live: Jira sync 21 issues SCRUM, approval Slack round-trip qua reaction thật (approved by phucnt0), flag → **SCRUM-22** tạo thật rồi transition Done, alert post + dedup, audit verify OK, self-capture qua watcher (chính phiên build này thành run có cost).

## Kết quả review (260702)

Code review độc lập: verdict **approve-with-fixes** — 0 CRITICAL. Đã fix ngay: **H1** audit hash-chain race giữa process (DSN `_txlock=immediate` + test 2-connection concurrent); **H2** Host/Origin guard trên console (chặn DNS-rebinding + cross-origin POST vào approval/kill switch — console không auth, bind localhost, guard này là trust boundary); **M1** lỗi ghi audit từ handler giờ log to; **M3** checkKill fail-close khi không đọc được state. Còn mở (cần quyết định sản phẩm): gate approval retry tạo pending mới & không tự expire; Slack reaction chưa whitelist người duyệt; leaderboard N+1 (chấp nhận quy mô nội bộ). Test/race/E2E xanh lại sau fix. Report: `plans/reports/from-code-reviewer-to-controller-260702-dandori-mvp-review-report.md`.

## v2 (260702, plan `260702-1117`) — surpass legacy

Đóng mọi gap mà 2 bản cũ còn thắng:

- **Approval lifecycle:** gate retry tái dùng pending (không spam queue); TTL 60' → `expired` (worker + lazy); Slack approver **whitelist** (`approvers` config / `SLACK_APPROVERS` env; rỗng = ai cũng duyệt, có cảnh báo).
- **Autonomy đo lại:** chỉ tính can thiệp GIỮA phiên (user msg sau lượt assistant đầu); prompt mở đầu không tính.
- **Compliance export** (`dandori export compliance`, `/export/compliance`, JSON/CSV): bundle audit chain + verify + approvals + flags + runs; bản thân export cũng ghi audit.
- **Multi-runtime:** `dandori wrap -- <cmd>` (fork/exec, passthrough IO, exit code giữ nguyên, git delta, adapter usage claude/codex/generic — không bịa số). Kill switch chặn cả wrap. Hooks vẫn là đường chính cho Claude Code (có guardrail per-tool-call; wrap thì không — giới hạn ghi rõ).
- **Git delta per run:** SessionStart snapshot HEAD+dirty → Stop tính `lines_added/deleted` + `head_before/after` → nền cho attribution & revert detection.
- **GitHub intelligence:** sync PR đầy đủ (created/merged/body) + map revert (`Reverts #N`); **AI-CFR** = reverted/merged (revert PR loại khỏi mẫu số); **PR cycle p50/p75**; `dandori sync reverts` quét `git revert` local map về run → event `revert_detected` → **acceptance giờ gồm tín hiệu revert thật** (formula nêu rõ 2 nguồn).
- **Attribution:** `dandori attribution` + số liệu ±lines/%reverted per agent.
- **Confluence 2 chiều:** `context show` (đọc page → text), `report confluence` + nút console (storage HTML: leaderboard/ROI/CFR/flags; dedup ngày; DRY_RUN guard). E2E live: page **3375105** tạo thật trong space MPM.
- **Analytics console:** spike detection (>3× median 7d, dedup/ngày, đẩy Slack) + trang `/spikes` explain; **DORA-lite** panel (lead time Jira, CFR, PR cycle; deploy freq = "n/a" trung thực); **run compare** `/runs/compare?ids=`; pagination runs.
- **Perf:** leaderboard 1-pass (hết N+1). Benchmark (M4 Max): `EvaluateAllow` **82µs/op** (mục tiêu <5ms), `Leaderboard` 50 agents × 1.000 runs **119ms** (mục tiêu <300ms).
- **Test depth:** coverage per package: govern 84 · integrations 100 · ghub 87.5 · learn 79 · jira/slack/confluence 77–79 · config 76 · store 71 · web ~70 · cli 61 · capture 63. Race clean.

### Review v2 (DONE_WITH_CONCERNS → đã fix)

2 HIGH + 7 MEDIUM đã xử lý trong cùng phiên: whitelist Slack chỉ match **user ID** (display name giả được) + quét MỌI reaction thay vì cái đầu tiên (chống griefing); approval **consume-once** cưỡng chế (N waiter chung 1 approval → chỉ 1 được allow); wrap fix misattribution FK + ignore SIGINT để luôn finalize run; compliance export **redact secret** (Bearer/xox/ATATT/sk-/ghp_) + CSV chống formula-injection + route đổi POST (side-effect audit); CFR chỉ tính revert PR đã MERGE + bỏ ref cross-repo; revert scan lọc theo cwd + prefetch dedup + guard hash regex; percentile nearest-rank; compare cap 5 ids; MidRunMsgs bỏ dòng sidechain/meta của subagent; dọn settings key khi run kết thúc.

## Giới hạn đã biết / [Sau]

- Tailwind + HTMX + Chart.js qua CDN — cần mạng lần đầu; có CSS fallback tối giản.
- Trend Δ7d là xấp xỉ (window 7d vs 14d), chưa windowing chính xác.
- `internal/web/viewdata.go` 224 dòng (hơi quá chuẩn 200 — thuần query helpers, tách sẽ vụn).
- Cursor/Codex adapter, Confluence/Sheets write, RBAC/SSO, revert-detection, policy simulator: [Sau].
