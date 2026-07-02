# Dandori

**Outer harness quản trị đội AI** — một binary Go bọc ngoài Claude Code, biến đám agent chạy lẻ thành đội ngũ được quản trị. Ba trụ: **CAPTURE** (ghi mọi run) · **GOVERN** (guardrail realtime + audit) · **LEARN** (grade A–F, ROI, leaderboard).

Vision đầy đủ: [docs/01-vision.md](docs/01-vision.md) · Tính năng: [docs/03-features.md](docs/03-features.md) · Ghi chú hiện thực: [docs/04-implementation-notes.md](docs/04-implementation-notes.md)

## Cài đặt

```bash
go build ./cmd/dandori          # 1 binary pure-Go, không CGO
./dandori init --project ~/code/my-app --agent backend-agent
./dandori serve                 # console tại http://127.0.0.1:4777
```

`init` cài 4 hooks (SessionStart / PreToolUse / PostToolUse / Stop) vào `.claude/settings.json` của project — merge, không phá hooks sẵn có, idempotent. Từ đó mọi phiên Claude Code trong project được tự động ghi lại, và **mọi tool-call đi qua guardrail engine**.

## Vòng đời một tool-call

```
Claude Code ──PreToolUse──▶ dandori hook pre-tool
   kill switch → sandbox scope → block rules → budget breaker → permission gate
   allow: im lặng · deny/ask: JSON permissionDecision (+ event + audit hash-chain)
```

- **Block** (G1): `rm -rf /`, đụng `.env`, force-push… — deny thẳng.
- **Sandbox** (G2): Write/Edit ngoài working dir của run — deny.
- **Budget** (G3): vượt trần tháng theo global/agent/project — deny; cảnh báo 50/75/90% → Slack.
- **Gate** (G4): `git push`, merge PR, deploy — tạo approval, chờ người duyệt (web hoặc Slack reaction ✅/❌) tối đa `gate_wait_seconds`, rồi deny kèm hướng dẫn retry.

## Lệnh chính

| Lệnh | Việc |
|---|---|
| `dandori serve` | Web console + watcher + Jira sync + Slack worker |
| `dandori init [--project DIR] [--agent NAME]` | Cài hooks capture/guardrail vào project |
| `dandori watch` | Quét transcript `~/.claude/projects` bắt run lọt hook (C7) |
| `dandori stats` / `leaderboard` | Grade, ROI, cost per agent (bản terminal của dashboard) |
| `dandori runs [--status] [-n]` | Danh sách run |
| `dandori budget` | Spend vs limit theo scope |
| `dandori approvals [--pending]` | Hàng đợi duyệt |
| `dandori kill <session>` / `--all` / `--off` | Kill switch |
| `dandori audit verify` | Kiểm tra hash-chain audit |
| `dandori gate --checks "go test ./..."` | Quality gate độc lập (G7) |
| `dandori sync jira\|github` | Kéo work items vào unified schema |

## Console (http://127.0.0.1:4777)

- `/` — **Morning standup**: đêm qua chạy gì, cái gì cần bạn hôm nay.
- `/dash/org` — leaderboard calibrate theo fleet, cost trend, grade distribution, ROI.
- `/runs`, `/runs/{id}` — drilldown timeline từng tool-call; kill / flag→Jira / sửa task key.
- `/reviews` — hàng đợi duyệt live-poll; approve/reject kèm lý do → audit bất biến.
- `/budgets` — đặt trần chi tiêu bằng form; breaker áp dụng ngay tool-call kế tiếp.
- `/provenance` — mọi con số lần ngược về đúng run/event sinh ra nó.
- `/rules` — bật/tắt guardrail rule.

## Config & credential

Config: `~/.dandori/config.yaml` (DB path, listen, pricing, budget, gate checks). Secret **chỉ** ở env / `.env` (gitignored): `ATLASSIAN_*`, `SLACK_XOXC_TOKEN`/`SLACK_XOXD_TOKEN`, `SLACK_REPORT_CHANNEL`. GitHub qua `gh` CLI keyring.

**An toàn ghi:** `DRY_RUN=true` mặc định (mọi write Jira/Slack chỉ log); `AGENT_WRITE_DISABLED=true` chặn tuyệt đối. Chạy thật: `DRY_RUN=false dandori serve`.

## Test

```bash
go test ./...            # unit (fixture từ transcript thật)
./scripts/e2e_local.sh   # E2E local: full hook cycle + 4 guardrail + kill + audit + gate
```
