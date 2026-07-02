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
| `dandori sync jira\|github\|reverts` | Kéo work items / quét git revert map về run |
| `dandori wrap [--agent X] -- <cmd>` | Chạy MỌI CLI agent (codex, aider…) dưới capture |
| `dandori attribution` | ±lines code per agent + tỉ lệ bị revert |
| `dandori export compliance [--format csv]` | Bundle audit chain + verify cho auditor/SIEM |
| `dandori report confluence` | Post fleet report lên Confluence (DRY_RUN guard) |
| `dandori context show --confluence <id>` | Đọc page Confluence thành text |
| `dandori band <agent> [supervised\|gated\|trusted]` | Autonomy band — grade có hệ quả |
| `dandori loop run` | Closed loop: grade thấp → flag → Jira → band action |
| `dandori review <agent>` | Nhận xét tiếng người (AI-generated, cache tuần) |
| `dandori rules simulate --pattern '...'` | Thử guardrail trên lịch sử trước khi bật |
| `dandori connect <url> --token X` | Nối máy dev vào server trung tâm (central mode) |
| `dandori relay` | Đẩy event tồn (spool) lên server ngay |
| `dandori team add\|assign\|list` | Quản lý đội: gộp operator + agent để so hiệu quả |
| `dandori observe run` | Master Observer: sinh insight + áp dụng action đã duyệt |
| `dandori flywheel detect\|promote\|publish\|adoption` | Phát hiện & chia sẻ cách làm hay |

## Central mode (nhiều máy → 1 server)

Server: `dandori serve` với `DANDORI_INGEST_TOKEN` set → mở thêm listener `0.0.0.0:4778` (Bearer token, tách hẳn console 127.0.0.1). Máy dev: `dandori connect http://server:4778 --token X` — hook parse transcript tại chỗ, chỉ gửi **số liệu đã redact** (không gửi transcript), offline thì spool rồi relay. Pre-tool guardrail chạy local qua policy snapshot.

## Chế độ CEO vs Kỹ thuật

Console có 2 mặt (nút chuyển ở thanh nav, nhớ bằng cookie):
- **Điều hành** (`/`) — trang tiếng Việt cho CEO: giá trị AI mang lại + trend, thẻ đội đèn giao thông, hộp "Việc cần bạn" (Duyệt/Bỏ qua), trợ lý chat.
- **Kỹ thuật** — 13 trang operator gốc cho trưởng nhóm.

`/chat` — trợ lý điều hành tiếng Việt (OpenRouter tool-calling): hỏi đáp số liệu thật; hành động nhạy cảm chỉ **tạo yêu cầu chờ duyệt**, không tự thực thi.

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
