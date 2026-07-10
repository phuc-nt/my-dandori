# Dandori

[![CI](https://github.com/phuc-nt/my-dandori/actions/workflows/ci.yml/badge.svg)](https://github.com/phuc-nt/my-dandori/actions/workflows/ci.yml)
[![License: Apache 2.0](https://img.shields.io/badge/License-Apache_2.0-blue.svg)](LICENSE)
[![Go 1.26](https://img.shields.io/badge/Go-1.26-00ADD8.svg)](go.mod)

**Outer harness quản trị đội AI** — một binary Go bọc ngoài Claude Code, biến đám agent chạy lẻ thành đội ngũ được quản trị. Ba trụ: **CAPTURE** (ghi mọi run) · **GOVERN** (guardrail realtime + audit) · **LEARN** (grade A–F, ROI, leaderboard).

Vision đầy đủ: [docs/01-product-vision.md](docs/01-product-vision.md) · Tính năng: [docs/03-features.md](docs/03-features.md) · Ghi chú hiện thực: [docs/04-implementation-notes.md](docs/04-implementation-notes.md)

> **📘 Hướng dẫn sử dụng tiếng Việt: [docs/guide/](docs/guide/)** — bắt đầu, guide theo vai (engineer/manager/admin), luồng tri thức, và bảng tra cứu đầy đủ.

Ngoài ba trụ nền, Dandori nay còn: **đăng nhập + phân quyền** (admin/viewer, principal thật vào audit — v10) · **insights hiệu suất chi phí** theo model/project với Wilson CI (v11) · **luồng tri thức** cá nhân→tổ chức→cá nhân: mining practice, AI-draft, phân phối skill/agent-kit hash-pinned (v12–v13) · **guardrail secret/PII + risk-score, budget downgrade** (v14) · **central-mode parity**: audit co-signed, distribution ký, compliance export (v15).

Chi tiết vận hành theo vai ở [docs/guide/](docs/guide/) — riêng bật **audit signing** & central-mode xem [guide Admin](docs/guide/04-admin.md).

## Cài đặt

```bash
go build ./cmd/dandori          # 1 binary pure-Go, không CGO
./dandori serve                 # console tại http://127.0.0.1:4777
```

Mở console lần đầu sẽ thấy **trình hướng dẫn thiết lập** (`/welcome`) với 3 bước:

1. **Kết nối một dự án**: chạy `dandori init --project ~/code/my-app --agent backend-agent`. Lệnh này cài 4 hooks (SessionStart / PreToolUse / PostToolUse / Stop) vào `.claude/settings.json` của project — merge, không phá hooks sẵn có, idempotent. Từ đó mọi phiên Claude Code trong project được tự động ghi lại, và **mọi tool-call đi qua guardrail engine**.
2. **Nối tích hợp**: mở `/settings/integrations`, dán token Jira/Slack/OpenRouter và bấm **Kiểm tra**. **Sau khi nối lần đầu, khởi động lại `dandori serve`** để các tác vụ nền (cảnh báo Slack, đồng bộ) hoạt động.
3. **Chạy thử**: chạy một phiên AI trong project đã kết nối. Khi có run đầu tiên, thiết lập hoàn tất.

## Vòng đời một tool-call

```
Claude Code ──PreToolUse──▶ dandori hook pre-tool
   kill switch → sandbox scope → block rules → secret/PII → budget → risk score → gate
   allow: im lặng · deny/ask: JSON permissionDecision (+ event + audit hash-chain)
```

- **Block** (G1): `rm -rf /`, đụng `.env`, force-push, đụng `~/.dandori/` (chống agent tự tắt guardrail)… — deny thẳng.
- **Sandbox** (G2): Write/Edit ngoài working dir của run — deny.
- **Secret/PII** (G1.5): secret (sk-/AKIA/PEM/Bearer non-env-ref) trong lệnh hoặc nội dung file → deny (không echo giá trị); PII (thẻ Luhn / email hàng loạt) → gate. Bật độc lập G1/G2 (`secrets_guard_enabled`).
- **Budget** (G3): vượt trần tháng → **downgrade-gate** (chỉ deny run đang chạy model đắt, model rẻ chạy tiếp + gợi ý `/model`); `budget.mode: hard` giữ hard-stop cũ. Cảnh báo 50/75/90% → Slack.
- **Risk score** (G5): tích điểm rủi ro theo cửa sổ trượt; mặc định **log-only** (quan sát), `risk_score.mode: gate` để chặn sau khi calibrate.
- **Gate** (G4): `git push`, merge PR, deploy — tạo approval, chờ người duyệt (web hoặc Slack reaction ✅/❌) tối đa `gate_wait_seconds`, rồi deny kèm hướng dẫn retry.
- **Break-glass**: nếu DB hỏng khiến guardrail fail-closed chặn mọi lệnh, đặt env `DANDORI_GOVERN_FAIL_OPEN=1` để tạm mở (log mỗi lần active).

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
| `dandori operator add\|list\|set-password\|disable` | Tài khoản đăng nhập console (v10) |
| `dandori token create\|list\|revoke` | Ingest token per-operator cho central-mode (v10) |
| `dandori knowledge detect\|import` | Detector tri thức · import memory/journal (v12–13) |
| `dandori skill list\|pull` · `kit list\|nominate\|pull` | Kéo skill / agent-kit hash-pinned về `.claude/` (v13) |

## Central mode (nhiều máy → 1 server)

Server: `dandori serve` với `DANDORI_INGEST_TOKEN` set → mở thêm listener `0.0.0.0:4778` (Bearer token, tách hẳn console 127.0.0.1). Máy dev: `dandori connect http://server:4778 --token X` — hook parse transcript tại chỗ, chỉ gửi **số liệu đã redact** (không gửi transcript), offline thì spool rồi relay. Pre-tool guardrail chạy local qua policy snapshot.

**Parity central↔local (v15):** central-mode run giờ tạo **audit hash-chain co-signed** trên server (Ed25519 + signed checkpoint neo ngoài box — chống rebuild/truncation); G5 risk-score + G3 budget chạy trong snapshot (scoped per-operator, không rò fleet data; budget vượt trần → Ask vì central không biết model để downgrade); `skill/kit pull` kéo qua server, verify `sha256(bytes)==approve-hash` + chữ ký per-unit (chống server độc tráo bytes); `dandori export compliance` gồm cả central run + disclose giới hạn client-attested. Ký bằng `DANDORI_AUDIT_SIGNING_KEY` (`dandori audit keygen`); auditor pin **fingerprint** (`dandori audit pubkey`) out-of-band. Central guardrail vẫn là **advisory** (verdict chạy máy dev) — audit là bằng chứng từ server-receipt trở đi, không claim tuyệt đối.

## Chế độ CEO vs Kỹ thuật

Console có 2 mặt (nút chuyển ở thanh nav, nhớ bằng cookie):
- **Điều hành** (`/`) — trang tiếng Việt cho CEO: giá trị AI mang lại + trend, thẻ đội đèn giao thông, hộp "Việc cần bạn" (Duyệt/Bỏ qua), trợ lý chat.
- **Kỹ thuật** — 13 trang operator gốc cho trưởng nhóm.

`/chat` — trợ lý điều hành tiếng Việt (OpenRouter tool-calling): hỏi đáp số liệu thật; hành động nhạy cảm chỉ **tạo yêu cầu chờ duyệt**, không tự thực thi.

## Console (http://127.0.0.1:4777)

- `/` — **Morning standup**: đêm qua chạy gì, cái gì cần bạn hôm nay.
- `/dash/org` — leaderboard calibrate theo fleet, cost trend, grade distribution, ROI.
- `/runs`, `/runs/{id}` — drilldown timeline từng tool-call; kill / flag→Jira / sửa task key.
- `/risk` — tổng quan rủi ro một trang: cần duyệt, agent hạng D/F, cảnh báo tồn, ngân sách nóng.
- `/reviews` — hàng đợi duyệt live-poll; approve/reject kèm lý do → audit bất biến. Mỗi item hiện ước lượng tác động từ lịch sử.
- `/budgets` — đặt trần chi tiêu bằng form; breaker áp dụng ngay tool-call kế tiếp.
- `/provenance` — mọi con số lần ngược về đúng run/event sinh ra nó.
- `/rules` — bật/tắt guardrail rule.

## Config & credential

Config: `~/.dandori/config.yaml` (DB path, listen, pricing, budget, gate checks). Secret **chỉ** ở env / `.env` (gitignored): `ATLASSIAN_*`, `SLACK_XOXC_TOKEN`/`SLACK_XOXD_TOKEN`, `SLACK_REPORT_CHANNEL`. GitHub qua `gh` CLI keyring.

**Nhập credential qua UI:** trang `/settings/integrations` cho phép dán token và bấm Kiểm tra ngay trong trình duyệt — ghi thẳng vào `./.env` (atomic, mode 0600). Chỉ ghi được đúng các key của tích hợp; không bao giờ ghi được `DRY_RUN`/`AGENT_WRITE_DISABLED`/`SLACK_APPROVERS`. Sau khi lưu credential lần đầu, **khởi động lại `dandori serve`** để worker nhận cấu hình mới.

**An toàn ghi:** `DRY_RUN=true` mặc định (mọi write Jira/Slack chỉ log); `AGENT_WRITE_DISABLED=true` chặn tuyệt đối. Chạy thật: `DRY_RUN=false dandori serve`.

## Test

```bash
go test ./...            # unit (fixture từ transcript thật)
./scripts/e2e_local.sh   # E2E local: full hook cycle + 4 guardrail + kill + audit + gate
```
