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

## Guardrail: G1–G5

Guardrail engine kẹp vào từng tool-call (`dandori hook pre-tool`), thứ tự first-hit-wins: **kill → sandbox → block → budget/secret → gate → risk**.

| Loại | Kind (DB) | Hành vi | Mặc định gồm |
|---|---|---|---|
| **Block** (G1) | `block` | Deny thẳng | `rm -rf /~`, đọc/di chuyển `.env`, `DROP TABLE`/xoá migration, force-push |
| **Secret/PII** (G1.5, v14) | engine | Khóa API/PEM/Bearer → DENY (giấu giá trị); ≥5 email hay Luhn-valid card → GATE cần duyệt | `secrets_guard_enabled: true` (mặc định), độc lập không phụ thuộc G1/G2 |
| **Sandbox** (G2) | — (engine) | Write/Edit ngoài cwd của run → deny | (cho phép `.claude/projects/` — memory) |
| **Budget** (G3, v14 thay đổi) | — (engine) | Vượt trần tháng → tùy chế độ; cảnh báo 50/75/90% | `budget.mode: downgrade` (mặc định): chỉ từ chối model đắt; `hard`: từ chối mọi model |
| **Gate** (G4) | `gate` | Tạo approval, chờ duyệt rồi cho qua/từ chối | `git push`, `gh pr merge`, `deploy`/`kubectl apply`/`terraform apply` |
| **Risk-score** (G5, v14) | engine | Tool quá tần suất / deny quá dày trong cửa sổ trượt → escalate cần duyệt (gate) hay ghi log | `risk_score.mode: log` (mặc định, không khóa); `gate` (escalate duyệt) sau khi calibrate ngưỡng |

### Quản rule

- **UI**: `/rules` — thêm/sửa/bật-tắt bằng form (không sửa YAML). `/rules/simulate` = "thử rule này trên 30 ngày run vừa rồi — chặn bao nhiêu lượt?" trước khi bật.
- **CLI**: `dandori rules` (xem) · `dandori rules simulate`.
- **Trực tiếp DB** (khi cần gấp): rule ở bảng `guardrail_rules` (cột `kind`/`pattern`/`enabled`/`scope_type`). Tắt một gate: `UPDATE guardrail_rules SET enabled=0 WHERE id=?`.

> **Gỡ gate deploy tạm thời** (ví dụ thật): rule gate `deploy|kubectl apply|terraform apply` khớp cả `wrangler pages deploy`. Tắt: set `enabled=0` cho đúng id đó. Guardrail **block** (nguy hiểm) nên giữ.

Điều chỉnh thời gian chờ duyệt: config `gate_wait_seconds`, `approval_ttl_minutes`.

---

## Audit — bằng chứng bất biến (v15: co-signing)

```bash
dandori audit verify      # walk hash-chain, phát hiện can thiệp + lý do (chain|signature|truncated|checkpoint)
dandori audit list        # liệt kê 50 entry gần nhất
```

`hash = sha256(prev || ts || actor || action || subject || detail)`. Một entry bị sửa → verify gãy đúng chỗ. Đây là câu trả lời khi compliance hỏi 6 tháng sau.

### Bật audit co-signing (v15 — khóa mới)

Audit co-signing (Ed25519) dùng để bảo vệ chuỗi khỏi MITM / bên thứ ba không có khoá bí mật. **Cảnh báo:** trên một máy duy nhất nơi khoá sống cạnh DB, insider có cả hai cái vẫn có thể sửa — co-signing bảo vệ chống lại bên ngoài không sở hữu khoá, không phải insider độc quyền.

**Bước 1: Tạo cặp khoá**

```bash
dandori audit keygen
```

Output:
```
Ed25519 keypair generated. Set the private key as an env var to enable audit signing:

  export DANDORI_AUDIT_SIGNING_KEY=<base64-private-key>

SAVE THIS NOW — it will not be shown again. Store it in .env (gitignored) or a secret manager, never in config.yaml.

Public key (share out-of-band for verification, e.g. pin it in an auditor's runbook):
  <base64-public-key>
Public key fingerprint (sha256, hex): <fingerprint>
```

**Bước 2: Lưu private key vào env**

```bash
# .env (gitignored)
DANDORI_AUDIT_SIGNING_KEY=<base64-private-key>
```

hoặc secret manager của tổ chức.

**Bước 3: PIN fingerprint out-of-band**

Chia sẻ public key + fingerprint (sha256 hex) cho auditor **ngoài kênh** — Git, Slack, email riêng, runbook có chữ ký — không qua bundle export (bundle có thể giả mạo). Auditor PIN fingerprint này độc lập:

```bash
dandori audit pubkey
```

Output:
```
public key: <base64>
fingerprint (sha256, hex): <fingerprint>
```

So sánh fingerprint **by hand** — kênh riêng biệt khỏi hệ thống. Đây là trust anchor.

**Bước 4: Verify on export**

```bash
dandori export compliance --out bundle.json
```

Bundle hiện chứa:
- `signed_count` / `unsigned_count` — bao nhiêu entry được ký
- Signature + key_id cho từng entry
- Phát hiện được: edited rows, full rebuilds, tail-truncation

Auditor **cross-check** fingerprint bundle report với PIN của họ. Signature mismatch → someone edited.

**v15 central mode:** server tạo co-signed audit rows cho những quyết định guardrail từ dev machine (central client);`dandori export compliance` gồm central runs + chữ ký server.

### Break-glass: tắm nhanh guardrail (sự cố)

Nếu DB bị khoá / guardrail bị break, dùng env **lâu đài**:

```bash
DANDORI_GOVERN_FAIL_OPEN=1 dandori serve
```

Guardrail vẫn chạy nhưng mọi lỗi DB → Allow (fail-open) thay vì Deny (fail-closed). Không lâu dài — tìm cách sửa DB. Seed block rule bảo vệ: agent không thể tự bỏ `~/.dandori/` để tắt Dandori.

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

## Central-mode — nhận dữ liệu từ nhiều máy (v15 parity)

Trong central-mode (một server, nhiều dev machine), v15 đạt đủ tính năng như local-mode:

### Đăng ký máy client

Từ dev machine:

```bash
dandori connect <server-url>
```

Server tạo token duy nhất (via `dandori token create`). Máy này gửi dữ liệu về server qua HTTPS.

### Audit trung ương

Trước đây: central runs **không** tạo audit row. v15: server tạo co-signed audit row cho mỗi quyết định guardrail của central client (G3 budget, G5 risk, etc.). `dandori export compliance` gồm central audit + signature.

### Guardrail G3/G5 ở trung ương

- **G3 Budget**: central-mode → Ask (vì server không biết model của run). Local-mode vẫn downgrade-hint.
- **G5 Risk-score**: dùng policy snapshot (operator cấu hình per-operator), server evaluate và escalate duyệt.

### Skill/kit pull qua mạng (v15 — bảo vệ RCE)

```bash
dandori skill pull <name>    # v15: fetch từ server, verify hash + signature per-unit
dandori kit pull <name>      # toàn bộ kit từ server
```

**Bảo vệ:**
- Byte-hash + signature mỗi file
- Deny-list (hooks/scripts/settings không phân phối)
- Symlink-safety preserved

---

## An toàn phân phối tri thức (v13–v15)

Khi bật kit/skill distribution, nhớ ranh giới cứng:

- **Không bao giờ distribute** `hooks/`, `settings.json`, `scripts/`, `output-styles/` — đó là dây điện guardrail (`internal/kitpolicy` chặn cả lúc nominate lẫn lúc pull).
- Kit/skill **pull-only** — Dandori không tự ghi lên máy engineer; họ chủ động pull, thấy diff, xác nhận.
- Mọi nội dung phân phối được **review nguyên văn + hash-pin against audit chain** trước khi tới máy ai.
- v15 central pull: verify byte-hash + per-unit signature từ server.
