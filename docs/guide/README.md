# Hướng dẫn sử dụng Dandori (tiếng Việt)

> Dandori là outer harness quản trị đội AI — bọc ngoài Claude Code để **ghi lại** (CAPTURE), **gác realtime + audit** (GOVERN), và **chấm điểm A–F + ROI** (LEARN). Bộ guide này đưa bạn từ cài đặt đến vận hành theo vai.

## Đọc theo thứ tự

| # | Guide | Cho ai |
|---|---|---|
| 01 | [Bắt đầu](01-getting-started.md) | Người mới — build → serve → init → login → chạy thử |
| 02 | [Engineer](02-engineer.md) | Người viết code cùng AI hàng ngày |
| 03 | [Manager / PO](03-manager.md) | Quản đội — review, budget, mandate, leaderboard |
| 04 | [Admin / Vận hành](04-admin.md) | Operator/token, guardrail, RBAC, backup |
| 05 | [Luồng tri thức](05-knowledge-flow.md) | mining → draft → publish → pull skill/kit (v12–13) |
| 06 | [Tra cứu](06-reference.md) | Bảng CLI, trang console, guardrail, công thức grade/ROI |

## Đi thẳng theo việc

- **Lần đầu cài** → [01](01-getting-started.md)
- **Lệnh bị chặn `[dandori G...]`** → [02 §Khi lệnh bị chặn](02-engineer.md) hoặc [04 §Guardrail](04-admin.md)
- **Quên mật khẩu / khoá console** → [01 §Sự cố](01-getting-started.md), [04 §Danh tính](04-admin.md)
- **Gỡ/sửa một guardrail rule** → [04 §Guardrail](04-admin.md)
- **Lấy skill/kit về máy** → [02 §Lấy tri thức chung](02-engineer.md)
- **Đóng góp practice / import bài học** → [02](02-engineer.md), [05](05-knowledge-flow.md)
- **Duyệt tri thức, mandate xuống đội** → [03 §Áp tri thức](03-manager.md), [05](05-knowledge-flow.md)
- **Tra một lệnh / trang / công thức bất kỳ** → [06](06-reference.md)

## Tài liệu liên quan (không phải guide)

- [Tầm nhìn sản phẩm](../01-product-vision.md) · [Tính năng đầy đủ](../03-features.md)
- [Nối tích hợp (Jira/Slack/OpenRouter…)](../integration-setup.md)
- [Ghi chú hiện thực theo version](../04-implementation-notes.md) · [Journals](../journals/)

---

*Mọi lệnh và trang trong bộ guide lấy từ code thật. Nếu một lệnh không chạy như mô tả, chạy `dandori <lệnh> --help` để xem cờ hiện tại và báo lại.*
