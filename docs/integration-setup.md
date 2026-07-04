# Integration & E2E Setup — my-dandori

> Cách kết nối Confluence / Jira / Slack / Google Workspace để test E2E **thật**.
> Credential thật đã nằm ở `../.env` (gitignored). File này giải thích từng cái + cách chạy live.
> Tenant test dùng chung với `my-project-manager` — cùng site Atlassian, cùng Slack workspace, cùng Google account.

## 1. Nguồn credential — ai lưu ở đâu

| Hệ thống | Cơ chế auth | Lưu ở đâu | Biến / lệnh |
|---|---|---|---|
| **OpenRouter** (LLM) | API key | `.env` | `OPENROUTER_API_KEY` (`sk-or-...`) |
| **Jira + Confluence** | 1 API token chung (cùng site Atlassian Cloud) | `.env` | `ATLASSIAN_SITE_NAME` + `ATLASSIAN_USER_EMAIL` + `ATLASSIAN_API_TOKEN` (`ATATT...`) |
| **Slack** | browser-token (2 cookie session) | `.env` | `SLACK_XOXC_TOKEN` (`xoxc-...`) + `SLACK_XOXD_TOKEN` (`xoxd-...`) + `SLACK_TEAM_DOMAIN` |
| **GitHub** | `gh` CLI (PAT do CLI quản) | keyring máy (KHÔNG ở `.env`) | `gh auth login` — kiểm tra `gh auth status` |
| **Google Workspace** | `gws` CLI (OAuth2 do CLI quản) | `~/.config/gws/` + keyring (KHÔNG ở `.env`) | `gws auth status` — không cần token trong env |

> **Quy tắc:** token Atlassian/Slack/OpenRouter sống ở `.env`. GitHub + Google auth do CLI (`gh`/`gws`) tự quản ở keyring máy — chỉ cần 2 CLI đó có trong PATH.

## 2. Chi tiết từng integration

### Atlassian (Jira + Confluence)
- **1 site, 1 token dùng chung** cho cả Jira và Confluence: `https://phucnt0.atlassian.net`.
- Token lấy tại id.atlassian.com → Security → API tokens. Token Atlassian có prefix `ATATT` nhưng **không ổn định** → đừng đưa vào field free-text (bộ phát hiện secret bắt theo key name, không bắt được trong free-text).
- Fixtures seeded sẵn (tái dùng, đừng seed lại):
  - Jira project: `SCRUM` (có epics + issues + sprint).
  - Confluence space: `MPM` (id `65846`), OKR page id `98466`.

### Slack
- Auth bằng **browser-token** (`slack-browser-mcp-server` kiểu), KHÔNG phải bot-token. 2 cookie: `xoxc` (token) + `xoxd` (session cookie, đã URL-encode — giữ nguyên `%2B` v.v.).
- Team domain: `mpm-phucnt`.
- Channel test: `C0BBZN04XPX` (dùng chung cho report + external).
- ⚠️ Browser-token quyền RỘNG hơn bot-token scoped → chỉ post vào channel whitelist, cẩn trọng khi write.

### GitHub
- `gh auth status` phải thấy `Logged in ... account phuc-nt`. Nếu chưa: `gh auth login`.
- Repo test: `phuc-nt/my-project-manager`.

### Google Workspace (gws CLI)
- **`gws`** = Google Workspace CLI (unofficial). Kiểm tra: `command -v gws` + `gws auth status` (phải thấy `auth_method: oauth2`, có `client_config`).
- Đọc Sheet: `gws sheets spreadsheets values get --params '{"spreadsheetId":"<id>","range":"A1:Z1000"}'`
  - Output có 1 dòng banner `Using keyring backend: keyring` TRƯỚC JSON → parse từ ký tự `{` đầu tiên.
  - JSON: `{"values": [[row], ...]}` — hàng đầu = header.
- Tạo/ghi Sheet (nếu cần test data): `gws sheets spreadsheets create` + `... values update`.
- Auth độc lập core: KHÔNG token trong `.env`, dùng chung OAuth máy ở `~/.config/gws/client_secret.json` + keyring.
- Các service khác `gws` hỗ trợ: `drive`, `gmail`, `calendar`, `admin-reports`.

## 3. Chạy E2E thật — checklist

```bash
# 1. Load env (project tự đọc .env, hoặc export thủ công)
#    DRY_RUN=true mặc định → chỉ log "định làm gì", KHÔNG post thật.

# 2. Kiểm tra 4 kết nối trước khi chạy write:
gh auth status                 # GitHub OK?
gws auth status                # Google OK? (bỏ dòng keyring banner)
# Jira/Confluence/Slack: token trong .env — test bằng 1 read trước

# 3. Read-only trước (an toàn):
#    - Jira: đọc issues project SCRUM
#    - Confluence: đọc page 98466 / space MPM
#    - Slack: đọc channel C0BBZN04XPX
#    - Sheet: gws sheets ... values get

# 4. Chạy WRITE thật: đặt DRY_RUN=false (inline, đừng sửa file):
#    DRY_RUN=false <lệnh chạy agent>
#    → post lên Slack/Confluence THẬT. Cẩn trọng: post ngoài = không hoàn tác dễ.
```

## 3.5 v7 write-back — config keys + live script

v7 thêm các key `config.yaml` sau (không key nào có mặt định ghi thẳng — mọi write vẫn qua approval + Guard):

| Key | Kiểu | Mặc định | Ghi chú |
|---|---|---|---|
| `digest_recipients` | `[]string` | rỗng | Đích DUY NHẤT của UG2b digest (Slack channel + Gmail). Rỗng = không gửi. **Config-pinned (C2)** — không route nào nhận đích từ request. |
| `export_spreadsheet_id` | `string` | rỗng | Đích DUY NHẤT của UC8 Sheets export. Rỗng = tự tạo 1 sheet lần đầu rồi lưu id vào `settings` để tái dùng. |
| `sheets_export_enabled` | `bool` | `false` | Bật nút export trên console. |
| `post_action_checks` | `[]string` | **rỗng (opt-in)** | ⚠️ **Mỗi chuỗi ở đây là lệnh `sh -c` thật, chạy trên cwd agent vừa sửa, sau MỌI run.** Đây là RCE theo thiết kế — chỉ điền chuỗi bạn (operator) tự tin, KHÔNG BAO GIỜ nhận từ web UI/DB/agent. Ưu tiên lệnh không-thực-thi-code như `go vet ./...`; tránh `go test ./...` hay bất cứ gì gọi `go generate` trừ khi tin agent tuyệt đối. |
| `gate_min_grade` / `gate_min_pass_pct` | setting (không phải YAML) | `C` / `80` | Ngưỡng UE3, sửa qua form `/gate-thresholds`, không sửa file. |

Biến môi trường mới:

| Biến | Dùng khi | Ghi chú |
|---|---|---|
| `DANDORI_GWS_BIN` | test/E2E | Trỏ `gws` sang binary khác (vd fake cho test offline). Bỏ trống ở production → dùng `gws` thật trên PATH. |
| `DANDORI_LIVE` | chạy live script | Phải `=1` mới cho script `scripts/e2e_v7_writeback.sh` chạy thật (cùng với `DRY_RUN=false`). |

### Chạy live E2E write-back thật

`scripts/e2e_v7_writeback.sh` là script DUY NHẤT thật sự ghi lên Jira/Calendar/Sheets/Gmail thật trong bộ E2E — mặc định **không làm gì** (in "skipping live" rồi exit 0) để không ai vô tình chạy write thật khi CI/dev chạy `go test`.

```bash
# 1. Đảm bảo .env có ATLASSIAN_*, gws auth status OK, gh auth status OK
# 2. Đảm bảo digest_recipients / export_spreadsheet_id đã set nếu muốn test 2 leg đó
#    (thiếu thì script tự SKIP leg đó, không fail)
# 3. Chạy — 2 biến bắt buộc CÙNG LÚC, không cái nào default sang true:
DANDORI_LIVE=1 DRY_RUN=false ./scripts/e2e_v7_writeback.sh
```

Script tự compile binary trong chính nó, tạo issue Jira / event Calendar / sheet **mới** (không đụng fixture `SCRUM` cũ), in ra id vừa tạo kèm dòng `MANUAL_CLEANUP:` cho từng thứ cần tự tay xoá (Dandori không tự xoá — an toàn hơn xoá nhầm). Digest tự gửi tới chính `digest_recipients` đã cấu hình (self-digest), không gửi hộ ai khác.

## 4. An toàn / lưu ý

- **`.env` gitignored** — không commit value thật. `.gitignore` đã set (`.env`, `.env.*`).
- **Kill switch:** `AGENT_WRITE_DISABLED=true` → chặn mọi mutation (chỉ read + log).
- **Budget:** `MONTHLY_BUDGET_USD=50`, cảnh báo ở 80%. LLM call thật tốn tiền (~$0.0006–0.003/report).
- **Tenant dùng chung với my-project-manager** — write test sẽ hiện ở cùng Slack channel / Confluence space. Tránh spam; dedup theo ngay+channel giúp tránh double-post.
- **Revoke khi xong:** nếu ngừng dùng, revoke token OpenRouter/Atlassian + regenerate Slack browser-token; `gws auth logout` / `gh auth logout` nếu cần.

## Unresolved (chờ my-dandori quyết)
1. my-dandori có schema/model riêng cho Jira/Sheet data không, hay tái dùng shape của mpm?
2. Có cần Slack channel/Confluence space RIÊNG cho dandori (tách khỏi mpm) để test không lẫn?
3. Google Sheet nào là nguồn data thật của dandori (tạo mới hay trỏ sheet có sẵn)?
