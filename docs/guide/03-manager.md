# Hướng dẫn cho Manager / PO

> Bạn quản đội — con người và agent. Guide này cho bạn biết cách nhìn sức khoẻ đội, duyệt các việc cần người quyết, kiểm soát chi phí, và áp tri thức/chính sách xuống toàn đội. Mọi hành động của bạn vào **sổ audit không sửa được**.

---

## Buổi sáng: nhìn gì trước

| Trang | Cho biết |
|---|---|
| **`/exec`** hoặc trang chủ | "Đêm qua đội làm gì + cái gì *cần bạn* hôm nay" (theo vai) |
| **`/risk`** | Agent hạng D/F + tuổi cờ + xu hướng tụt |
| **`/reviews`** | Hàng đợi mọi việc đang chờ người duyệt |
| **`/dash/org`** | Leaderboard toàn fleet, phân bố grade, đội lên/xuống |
| **`/insights`** | Model nào đáng tiền, project nào tốn/kết quả |

## Đọc leaderboard & grade

`/dash/org` đặt **mọi agent và mọi người lên cùng một thước** (việc người kéo từ Jira/GitHub cùng schema). Grade A–F calibrate theo phân vị của chính đội bạn — A là phân vị ≥80. So sánh con người là **ẩn danh và tắt được** (`calibrate_with_humans`), để thước không thành công cụ gây áp lực.

Click bất kỳ số nào → drill về run gốc. Đây là điều kiện để bạn *dám* quyết dựa trên nó.

## ROI: bao nhiêu là lãng phí

`/dash/agent/{agent}` hiện ROI tách 3 xô loại trừ nhau:

```
Lãng phí = $run failed/killed + $run còn cờ lỗi mở + $run sạch × (1 − Acceptance%)
```

Ví dụ: *"Agent tốn $8,200 — 38% ($3,100) là lãng phí."* Mỗi xô kèm danh sách run cụ thể.

---

## Review queue: đóng vòng governance

**`/reviews`** là inbox live các việc chờ người quyết. Mỗi việc:

- **Approve / Reject + lý do** — one-click, lý do free-text rơi thẳng vào audit bất biến.
- **Ước lượng tác động** (trên card) — "hành động tương tự trước đây trung bình tốn $X, đụng Y file" (≥3 mẫu).
- Với **tri thức** (skill/kit/context — v12/13): card hiện **nguyên văn nội dung + hash** để bạn thấy đúng cái sẽ publish trước khi duyệt. Loại này **chỉ duyệt trên web** (không qua Slack một-emoji — cố ý, vì đó là nội dung sẽ chạy trên máy engineer).

Duyệt cũng làm được **trong Slack**: reaction ✅/❌ trên tin cảnh báo → ghi lý do vào cùng audit (trừ tri thức, phải web).

## Điều khiển đội agent

| Việc | Ở đâu |
|---|---|
| **Kill run đang chạy** | nút đỏ trên mọi run · `/runs` bulk-kill nhiều run |
| **Đổi budget** | `/budgets` — trần $/token theo global/agent/project; cảnh báo 50/75/90% |
| **Nâng/hạ autonomy band** | dropdown trên agent: `supervised` (mọi edit cần duyệt) → `gated` (mặc định) → `trusted` (bỏ gate thường, giữ critical) |
| **Flag → Jira ticket** | biến run lỗi thành bug Jira, pre-fill link + log |
| **Kill switch toàn cục** | `/api/kill` — dừng mọi tool-permission khi có sự cố |

**Closed loop tự động**: agent tụt hạng **F** → tự giáng supervised + mở review; **D** → đề xuất giáng chờ bạn duyệt + mở ticket Jira; phục hồi → tự gỡ cờ. Bạn không phải canh tay.

---

## Áp tri thức & chính sách xuống đội (v12/13)

Khi một practice/skill được chứng minh giá trị, bạn có hai mức:

- **Suggest** — practice published hiện thành gợi ý trên trang agent-detail ("agent chưa dùng X, fleet dùng X done-rate +Δ"). Engineer tự pull.
- **Mandate** — bạn (admin) bấm **Mandate** trên `/knowledge/unit/{id}`. Với context/rule: enforce qua rail sẵn (tiêm vào session / guardrail server-side). Với skill: **compliance visibility** — SessionStart nhắc agent nào thiếu/lệch + danh sách trên console. Không auto-push file, không refuse-to-run (engineer sở hữu máy).

Đo hiệu quả: adoption before/after mỗi practice (kèm caveat "quan sát, có thể là hồi quy về trung bình — không phải nhân quả"). Đo ra tệ → hệ thống tự đề cử retire, bạn quyết.

Chi tiết cả vòng: [05-knowledge-flow.md](05-knowledge-flow.md).

---

## Xuất báo cáo cho cấp trên

- **`/export/compliance`** — bundle JSON/CSV + audit trail (cho SIEM/kiểm toán).
- **`/dash/export-sheets`** — đẩy cost/leaderboard/ROI ra Google Sheets (cho finance đọc nơi họ vốn ở).
- **`/dash/send-digest`** — digest sức khoẻ fleet gửi Slack + Gmail.
- **`/reports/confluence`** — đăng summary run/sprint lên Confluence.

---

## Nguyên tắc nên nhớ

- **Mọi số truy ngược được** — nếu ai đó chất vấn, click về raw event.
- **Audit đối xứng** — chính hành động duyệt/override/mandate của *bạn* cũng vào audit. Ai canh người canh: cùng hệ thống.
- **Không công khai xếp hạng con người** — chống Goodhart. Baseline người ẩn danh.
