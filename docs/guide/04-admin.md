# Hướng dẫn cho Admin / Vận hành

> Bạn dựng và giữ Dandori chạy cho cả đội. Guide này về danh tính, phân quyền, guardrail, central-mode, và backup. Các thao tác ở đây quyết định an toàn của toàn hệ.

---

## Danh tính & phân quyền (v10)

### Tài khoản đăng nhập console

```bash
dandori operator add <username> --role admin|viewer   # tạo, nhập mật khẩu 2 lần
dandori operator list                                  # xem danh sách
dandori operator set-password <username>               # đổi mật khẩu (huỷ session cũ)
dandori operator disable <username>                    # off-board (huỷ session + token)
```

**Quy tắc trust-gate** (quan trọng, đây là thiết kế an toàn):

- **Chưa từng tạo tài khoản nào** + console nghe loopback → vào thẳng (local-trust, như bản đầu).
- **Đã có ≥1 tài khoản** → console **bắt buộc đăng nhập**; không session → redirect `/login`. Không bao giờ "không cookie ⇒ admin".
- **Nghe non-loopback (LAN) + chưa có tài khoản** → chỉ hiện trang bootstrap, **không cấp admin cho ai**.
- **Disable admin cuối cùng** → console **khoá** (không rơi về no-auth). Mở lại bằng `operator add`/`set-password` từ shell máy chủ.

**Hai vai:**

| Vai | Làm được | Không làm được |
|---|---|---|
| `viewer` | Xem mọi dashboard/report; đề cử (nominate) tri thức | Duyệt, kill, đổi budget, mandate, publish |
| `admin` | Mọi thứ trên + mọi hành động ghi | — |

Mọi hành động admin (duyệt, kill, mandate…) ghi **principal thật** vào audit hash-chain — sổ trả lời được *ai* làm gì, khi nào, vì sao.

### Token cho central-mode (nhiều máy)

Khi có máy khác gửi dữ liệu về (central-mode), mỗi máy cần token riêng:

```bash
dandori token create <username> --name <nhãn>    # in token MỘT LẦN — lưu ngay
dandori token list <username>                     # không in lại plaintext
dandori token revoke <token-id>                   # thu hồi (hiệu lực ngay)
```

Server derive principal **từ token** (không tin header client tự khai) — không mạo danh được. Disable operator → token của họ cũng chết ngay.

> Central-mode có nhưng còn giới hạn (single shared listener). Mặc định khuyến nghị **1 binary + 1 file SQLite** một máy.

---

## Guardrail: G1–G4

Guardrail engine kẹp vào từng tool-call (`dandori hook pre-tool`), thứ tự first-hit-wins: **kill → sandbox → block → budget → gate**.

| Loại | Kind (DB) | Hành vi | Mặc định gồm |
|---|---|---|---|
| **Block** (G1) | `block` | Deny thẳng | `rm -rf /~`, đọc/di chuyển `.env`, `DROP TABLE`/xoá migration, force-push |
| **Sandbox** (G2) | — (engine) | Write/Edit ngoài cwd của run → deny | (cho phép `.claude/projects/` — memory) |
| **Budget** (G3) | — (engine) | Vượt trần tháng → deny; cảnh báo 50/75/90% | trần từ `/budgets` hoặc config `global_monthly_usd` |
| **Gate** (G4) | `gate` | Tạo approval, chờ duyệt rồi cho qua/từ chối | `git push`, `gh pr merge`, `deploy`/`kubectl apply`/`terraform apply` |

### Quản rule

- **UI**: `/rules` — thêm/sửa/bật-tắt bằng form (không sửa YAML). `/rules/simulate` = "thử rule này trên 30 ngày run vừa rồi — chặn bao nhiêu lượt?" trước khi bật.
- **CLI**: `dandori rules` (xem) · `dandori rules simulate`.
- **Trực tiếp DB** (khi cần gấp): rule ở bảng `guardrail_rules` (cột `kind`/`pattern`/`enabled`/`scope_type`). Tắt một gate: `UPDATE guardrail_rules SET enabled=0 WHERE id=?`.

> **Gỡ gate deploy tạm thời** (ví dụ thật): rule gate `deploy|kubectl apply|terraform apply` khớp cả `wrangler pages deploy`. Tắt: set `enabled=0` cho đúng id đó. Guardrail **block** (nguy hiểm) nên giữ.

Điều chỉnh thời gian chờ duyệt: config `gate_wait_seconds`, `approval_ttl_minutes`.

---

## Audit — bằng chứng bất biến

```bash
dandori audit verify      # walk hash-chain, phát hiện can thiệp
dandori audit list        # liệt kê entry
```

`hash = sha256(prev || ts || actor || action || subject || detail)`. Một entry bị sửa → verify gãy đúng chỗ. Đây là câu trả lời khi compliance hỏi 6 tháng sau.

---

## Autonomy band & closed loop

```bash
dandori band <agent> supervised|gated|trusted
```

- `supervised` — mọi edit tool cần duyệt.
- `gated` — mặc định, rule gate áp dụng.
- `trusted` — bỏ gate thường, **giữ gate critical**.

Closed loop tự chạy: F → auto-supervised; D → đề xuất giáng (chờ duyệt). Cấu hình ngưỡng ở `/gate-thresholds`.

---

## Context Hub (chính sách tổ chức)

Context phân tầng Company → Team → Agent, tiêm vào mỗi session qua SessionStart. Sửa **Company layer** (áp cho MỌI agent) là mutation nhạy nhất — **approval-gated**, không ghi thẳng. Quản ở khu `📚 Context` trên console: editor + version + diff + rollback + "effective context preview" (xem đúng context agent thực sự thấy).

---

## Cấu hình & backup

- Config: file YAML (key như `listen`, `global_monthly_usd`, `learn_window_days`, `gate_wait_seconds`, `openrouter_model`…). Xem [integration-setup.md](../integration-setup.md).
- **Secret sống ở `.env` (gitignored) hoặc CLI keyring — không bao giờ commit.**
- Data: một file SQLite (`db_path`, mặc định `~/.dandori/dandori.db`). **Backup = copy file đó** (khi `serve` dừng, hoặc dùng `sqlite3 .backup`).
- Runtime data (`.data/`), `.env`, `.venv/` đã gitignore.

---

## An toàn phân phối tri thức (v13)

Khi bật kit/skill distribution, nhớ ranh giới cứng:

- **Không bao giờ distribute** `hooks/`, `settings.json`, `scripts/`, `output-styles/` — đó là dây điện guardrail (`internal/kitpolicy` chặn cả lúc nominate lẫn lúc pull).
- Kit/skill **pull-only** — Dandori không tự ghi lên máy engineer; họ chủ động pull, thấy diff, xác nhận.
- Mọi nội dung phân phối được **review nguyên văn + hash-pin against audit chain** trước khi tới máy ai.
