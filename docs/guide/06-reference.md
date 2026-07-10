# Tra cứu

> Bảng đầy đủ CLI, trang console, guardrail, và công thức grade/ROI. Mọi mục lấy từ code thật. Chạy `dandori <lệnh> --help` để xem cờ.

---

## Lệnh CLI

### Chạy & thiết lập

| Lệnh | Việc |
|---|---|
| `dandori serve` | Web console + watcher + Jira sync + Slack worker |
| `dandori init [--project DIR] [--agent NAME]` | Cài hook capture/guardrail vào project (idempotent) |
| `dandori watch [--full]` | Quét transcript bắt run lọt hook (`--full` = backfill) |
| `dandori run` | Phóng agent-run từ CLI |
| `dandori wrap [flags] -- <command...>` | Bọc một lệnh bất kỳ thành run được ghi |
| `dandori hook [session-start\|pre-tool\|post-tool\|stop]` | Hook nội bộ (Claude Code gọi, không gọi tay) |

### Danh tính & phân quyền (v10)

| Lệnh | Việc |
|---|---|
| `dandori operator add <username> --role admin\|viewer` | Tạo tài khoản login |
| `dandori operator list` | Liệt kê tài khoản |
| `dandori operator set-password <username>` | Đổi mật khẩu (huỷ session cũ) |
| `dandori operator disable <username>` | Off-board (huỷ session + token) |
| `dandori token create <username> --name <nhãn>` | Cấp ingest token (in 1 lần) |
| `dandori token list <username>` | Liệt kê token (không in plaintext) |
| `dandori token revoke <token-id>` | Thu hồi token |
| `dandori connect <server-url>` | Nối máy này vào central-mode |

### LEARN — chấm điểm & phân tích

| Lệnh | Việc |
|---|---|
| `dandori stats` | Grade/ROI/cost per agent (terminal) |
| `dandori leaderboard` | Xếp hạng fleet |
| `dandori attribution` | Cost attribution theo chiều |
| `dandori review <agent>` | Chi tiết đánh giá một agent |
| `dandori runs` / `dandori kill [session-id]` | Liệt kê / kill run |

### GOVERN — guardrail & audit

| Lệnh | Việc |
|---|---|
| `dandori rules [simulate]` | Xem rule / thử rule trên lịch sử |
| `dandori band <agent> [supervised\|gated\|trusted]` | Đổi autonomy band |
| `dandori budget` | Xem/đặt trần budget |
| `dandori gate [run]` | Quality gate |
| `dandori audit keygen` | Tạo cặp Ed25519 để ký audit (v15) |
| `dandori audit pubkey` | In public key + fingerprint (v15) |
| `dandori audit verify` | Verify hash-chain + kiểu break (v15: chain\|signature\|truncated\|checkpoint) |
| `dandori audit list` | Liệt kê 50 entry audit gần nhất |
| `dandori export [compliance]` | Bundle compliance (v15: include central runs + signatures) |

### Tri thức (v12–v13)

| Lệnh | Việc |
|---|---|
| `dandori knowledge detect` | Chạy detector (nominate-only) |
| `dandori knowledge import [--memory] [--journals] [--project X]` | Import memory/journal → context unit |
| `dandori skill list` / `skill pull <name\|unit-id>` | Xem / kéo skill về `.claude/skills/` |
| `dandori kit list` / `kit nominate <name>` / `kit pull <name\|unit-id>` | Đề cử / kéo agent-kit trọn bộ |
| `dandori flywheel` · `promote <run-id>` · `adoption <playbook-id>` | Playbook flywheel |

### Tích hợp & khác

| Lệnh | Việc |
|---|---|
| `dandori sync [jira\|github\|reverts]` | Đồng bộ thủ công |
| `dandori report [confluence]` / `digest` | Đăng report / gửi digest |
| `dandori context show [--confluence PAGE_ID \| --effective AGENT_ID]` | Xem context hiệu lực |
| `dandori team` · `assign <team>` · `approvals` | Team & approval |
| `dandori observe` · `loop [run]` · `relay` · `gate` | Vận hành nâng cao |

---

## Trang console (web)

| Trang | Nội dung |
|---|---|
| `/welcome` | Wizard thiết lập 3 bước |
| `/exec` | Standup theo vai — "cần bạn hôm nay" |
| `/risk` | Agent D/F + cờ quá hạn |
| `/reviews` | Hàng đợi duyệt (approve/reject + lý do) |
| `/runs` · `/runs/{id}` | Danh sách / chi tiết run |
| `/dash/org` · `/dash/agent/{agent}` · `/dash/project/{project}` | Dashboard 3 tầng |
| `/insights` | Hiệu suất chi phí model/project + Wilson CI |
| `/budgets` · `/gate-thresholds` | Cấu hình budget / ngưỡng gate |
| `/rules` | Guardrail rule builder + simulate |
| `/knowledge` · `/knowledge/mining` · `/knowledge/unit/{id}` | Thư viện + mining + chi tiết unit |
| `/knowledge/draft` · `/knowledge/nominate` | AI-draft · đề cử |
| `/contexts` (+ diff/history/effective/promote/rollback) | Context Hub |
| `/settings/integrations` · `/healthz` | Nối tích hợp · health check |
| `/api/kill` | Kill switch toàn cục |
| `/export/compliance` · `/dash/export-sheets` · `/dash/send-digest` · `/reports/confluence` | Xuất báo cáo |

---

## Guardrail G1–G5

Thứ tự first-hit-wins: **kill → sandbox → block → budget/secret → gate → risk**.

| | Kind | Hành vi | Mặc định |
|---|---|---|---|
| **G1 Block** | `block` | deny thẳng | `rm -rf /~` · đọc `.env` · `DROP TABLE`/xoá migration · `git push --force` |
| **G1.5 Secret/PII** (v14) | engine | Khóa API/PEM/Bearer nguy hiểm → DENY; ≥5 email hay 1 Luhn-card → GATE | `secrets_guard_enabled: true` |
| **G2 Sandbox** | engine | Write/Edit ngoài cwd → deny | cho phép `/tmp`, `~/.claude/projects/` |
| **G3 Budget** (v14 thay đổi) | engine | vượt trần tháng: downgrade-hint (chỉ chặn model đắt) hoặc hard-stop | từ `/budgets` / `budget.mode: downgrade` (mặc định) |
| **G4 Gate** | `gate` | tạo approval, chờ duyệt | `git push` · `gh pr merge` · `deploy`/`kubectl apply`/`terraform apply` |
| **G5 Risk-score** (v14) | engine | Tool quá nhiều / deny quá dày → khóa (sliding window); log-only hay gate | `risk_score.mode: log` (mặc định) hay `gate` |

---

## Công thức grade & ROI

### 4 chỉ số (mỗi cái 0–100)

| Chỉ số | Công thức |
|---|---|
| **Acceptance** | `100 × (1 − edit_bị_loại / tổng_edit)` — loại = bị người/guardrail từ chối ∪ nằm trong run bị git revert |
| **Success** | `100 × run_done / run_kết_thúc` — Jira status khi có task_key, else kết thúc sạch không cờ lỗi |
| **Autonomy** | `100 × (1 − run_bị_can_thiệp / tổng_run)` — can thiệp = xin phép hoặc nhắn giữa run |
| **Reliability** | `100 × (1 − trung_bình(tỉ_lệ tool-lỗi, guardrail-block, kill))` |

**Composite** = trung bình cộng 4 chỉ số (trọng số bằng nhau, không núm ẩn).

### Grade A–F (calibrate theo phân vị fleet)

```
A ≥ phân vị 80 · B ≥ 60 · C ≥ 40 · D ≥ 20 · F dưới đó
```

Fleet < 5 thực thể → thang tĩnh + nhãn "chưa calibrate". Agent < 5 run → cờ "độ tin cậy thấp".

### ROI (3 xô loại trừ nhau)

```
Lãng phí = $run failed/killed + $run done còn cờ lỗi mở + $run sạch × (1 − Acceptance%)
```

---

## Config keys chính (YAML)

**Guardrail v14–v15:**
- `secrets_guard_enabled: true/false` — bật G1.5 secret/PII (mặc định true)
- `budget.mode: downgrade|hard` — v14 thay đổi: downgrade = chỉ từ chối model đắt
- `risk_score.mode: log|gate` — G5 quan sát hay khóa (mặc định log)
- `risk_score.threshold`, `.window_n`, `.tool_points`, `.denial_points`, `.guarded_tools` — chi tiết điểm rủi ro

**Audit (v15):**
- `DANDORI_AUDIT_SIGNING_KEY` (env) — private key Ed25519, set để ký co-sign
- `DANDORI_AUDIT_CHECKPOINT_OFFSITE` (env) — sink bên ngoài checkpoint

**Khác:**
`listen` · `ingest_listen` · `db_path` · `projects_dir` · `global_monthly_usd` · `warn_pcts` · `gate_wait_seconds` · `approval_ttl_minutes` · `learn_window_days` · `calibrate_with_humans` · `openrouter_model` · `chat_daily_token_budget` · `allow_legacy_ingest_token` · `public_base_url` · `digest_recipients` · `post_action_checks`.

Đầy đủ + cách nối integration: [integration-setup.md](../integration-setup.md).
