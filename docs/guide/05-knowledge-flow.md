# Luồng tri thức: cá nhân → tổ chức → cá nhân

> Cụm tính năng mới nhất (v12–v13). Biến bài học rải rác của từng engineer thành tài sản chung của tổ chức, rồi phân phối lại về máy từng người — đo được từng bước. "Senior nghỉ, tri thức ở lại."

---

## Toàn cảnh vòng lặp

```
CAPTURE data ─▶ ① DETECT/MINE  ─▶ ② SOẠN  ─▶ ③ REVIEW  ─▶ ④ PUBLISH ─▶ ⑤ PHÂN PHỐI ─▶ ⑥ ĐO
               (tự / mining queue)  (tay / AI)   (admin web)   (gated)     (pull/suggest)  (adoption)
                                                                                              │
                                                              ⑦ tệ đi → đề cử retire ◀────────┘
```

Bốn **loại tri thức** (kind) chảy qua cùng một đường ống:

| Kind | Là gì | Phân phối bằng |
|---|---|---|
| **context** | đoạn CLAUDE.md/policy | tiêm vào session (SessionStart) |
| **rule** | guardrail rule | enforce server-side |
| **playbook** | mẫu prompt+model | card trên console |
| **skill** | file `.claude/skills/` | `dandori skill pull` |
| **kit** | trọn bộ `.claude/` chuẩn | `dandori kit pull` |

---

## ① Phát hiện tri thức đáng đúc

### Mining queue — "run đáng đọc" (v13)

**`/knowledge/mining`** liệt kê run có tín hiệu đáng học, tính bằng 4 truy vấn SQL (on-demand, không phải điểm số):

1. **Corrective steering rồi vẫn done** — người phải chỉnh nhiều mà cuối cùng xong.
2. **Fail → retry → success** — chuỗi retry kết thúc thành công.
3. **Guardrail block rồi vẫn done** — vượt chướng ngại.
4. **Cost outlier** (>3× median project) — kèm nhãn hai chiều: *đọc để biết đáng học (task khó) hay đáng tránh (loop lãng phí)*.

Xếp theo **số tín hiệu khác nhau** (không phải điểm gộp), không có cột người — **không phải leaderboard**. Dismiss một run = ẩn khỏi *đúng danh sách này thôi*, không giấu khỏi run detail/audit/governance.

### Detector tự động

`dandori knowledge detect` (hoặc chạy trong observer sweep): quét skill/tool/rule/context tương quan done-rate cao (Wilson CI hai vế phải tách, ≥10 mẫu/vế, segment theo project chống nhiễu task-mix) → **chỉ đề cử**, không bao giờ tự publish.

---

## ② Soạn practice

- **Tay**: mở run đáng đọc → viết practice markdown → đề cử.
- **AI-draft** (v13): nút "Soạn nháp (AI)" → OpenRouter đọc **bằng chứng đã che secret từ DB** (không đọc raw transcript) → soạn nháp → **người sửa + chịu trách nhiệm** → đề cử. Mang nhãn `origin: ai-draft` suốt vòng đời. Không auto-nominate. Nếu OpenRouter chết → form trống vẫn dùng được (viết tay).
- **Import**: `dandori knowledge import --memory|--journals` — nâng bài học đã ghi trong memory/journal thành unit.

Mọi nguồn gắn nhãn `origin`: `human` / `import-memory` / `import-journal` / `ai-draft` / `detector` — người review luôn biết tri thức từ đâu ra.

---

## ③ Review (admin, web-only)

**`/knowledge`** là thư viện + hàng đợi. Admin mở `/knowledge/unit/{id}` hoặc `/reviews`:

- Thấy **nguyên văn nội dung** (skill: full body; kit: manifest + từng file; context: diff) + **hash** đang duyệt.
- Với kit: duyệt **nguyên bộ** (atomic) — hash manifest pin đúng bytes bạn thấy.
- **Chỉ duyệt trên web** — tri thức bị loại khỏi Slack một-emoji (vì đó là nội dung sẽ chạy trên máy engineer, phải nhìn thấy bytes mới duyệt).

Nội dung **bất biến sau khi duyệt** — muốn sửa = tạo version mới, review lại.

---

## ④→⑤ Publish & phân phối

Duyệt xong (qua `RequestAction → applier → audit`):

- **context/rule** → publish lên layer, enforce sẵn.
- **skill/kit** → published, chờ engineer pull.

Engineer thấy:

- **Suggest** — card "agent bạn chưa dùng X, fleet dùng X done-rate +Δ (CI, n)" trên trang agent-detail.
- **Pull** — `dandori skill pull <tên>` / `dandori kit pull <tên>`: verify hash 3 lớp against audit → diff → xác nhận → ghi repo-local.

**Mandate** (manager áp đặt): admin bấm Mandate → context/rule enforce; skill = compliance notice (nhắc ai thiếu/lệch, không auto-push).

### An toàn phân phối — ranh giới cứng

- Kit **chỉ** mang file instruction (agents/rules/skills/commands `.md`). **Không bao giờ** `hooks/`, `settings.json`, `scripts/` — dây điện guardrail.
- Verify **against audit hash-chain** (nguồn độc lập) — DB-writer sửa cả body lẫn hash-row vẫn lệch audit → pull từ chối.
- Symlink-safe, ghi repo-local, engineer thấy diff trước khi ghi, git review = gate thứ hai.

---

## ⑥→⑦ Đo & rollback

Adoption đo **installed vs active**: skill "installed" khi pull, "active" khi thực sự có event dùng nó sau đó — click-adopt không farm được số. Before/after done-rate của người adopt, **kèm caveat**: *"quan sát — adopter thường adopt lúc đang bí, cải thiện có thể là hồi quy về trung bình, không phải nhân quả"*.

Đo ra tệ đi → hệ thống **đề cử retire** (không auto), người quyết. Retire một skill mandated: file đã pull trên máy engineer **giữ nguyên** (họ sở hữu máy), notice ngừng.

---

## Trạng thái thực tế (đọc trung thực)

Fleet hiện còn ít data (số Skill event nhỏ) → mining/detector ship như **hạ tầng + honest empty-state**, tự "sáng" khi run tích luỹ đủ ngưỡng. Context-ROI (đo giá trị một lần sửa CLAUDE.md) chỉ có số thật khi đội cấu hình Context Hub. Đây là thiết kế: **không bịa số khi chưa đủ mẫu**.
