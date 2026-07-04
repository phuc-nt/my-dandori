# Dandori — Tech Stack

> Đề xuất tech stack cho bản Dandori mới. Ba ưu tiên xếp theo thứ tự: **nhanh · nhẹ · UI dễ dùng với người ít rành kỹ thuật (low-tech)**.
> Tham chiếu hai bản cũ: `dandori/` (Node/Express full platform) và `dandori-cli/` (Go CLI). Kết luận: đi theo xương Go của CLI, nâng cấp đúng phần UI.

---

## TL;DR

| Tầng | Chọn | Vì sao |
|---|---|---|
| **Ngôn ngữ / runtime** | **Go 1.26+** | Single binary, zero-dep, khởi động tức thì, RAM thấp — "nhanh & nhẹ" là mặc định, không phải tối ưu về sau |
| **HTTP** | **chi v5** | Router chuẩn, mỏng, stdlib-compatible; đã dùng ở dandori-cli |
| **DB (local)** | **SQLite** (`modernc.org/sqlite`, pure Go) | Không CGO, cross-compile dễ, file đơn — hợp offline-first |
| **DB (server, tuỳ chọn)** | **PostgreSQL** (`pgx`) | Chỉ bật khi cần tổng hợp nhiều máy; mặc định không cần |
| **UI** | **Go html/template + HTMX + Tailwind (CDN) + Chart.js** | Server-render, low-tech mở là chạy; không build step, không SPA |
| **CLI** | **Cobra** | Đã có ở dandori-cli, chuẩn ngành Go |
| **Config** | **YAML + env override** (`gopkg.in/yaml.v3`) | Không hardcode; secret sống ở `~/.dandori/config.yaml` |
| **Test** | **`go test` table-driven** | Nhanh, không cần runner ngoài |

Một câu: **Go single-binary + HTMX server-render**. Đây là stack cho tool nội bộ ưu tiên tốc độ và độ đơn giản, không phải cho một SaaS đại chúng.

---

## Vì sao Go, không phải Node

Bản `dandori/` (full platform) chạy Node/Express/TS — rất mạnh về tính năng (MCP SDK, Google Drive OAuth, 903 test) nhưng **nặng runtime**: cần `node_modules`, cần Node cài sẵn, RAM cao hơn, khởi động chậm hơn. Đó là stack đúng cho một platform đầy đủ, **sai** cho ba ưu tiên lần này.

Go cho ta đúng ba thứ đang cần, miễn phí ngay từ đầu:

- **Nhanh** — biên dịch sẵn, không JIT warm-up; p99 request thấp và ổn định.
- **Nhẹ** — **một file binary**, không runtime đi kèm, không `node_modules`. Copy là chạy.
- **Dễ phát hành cho low-tech** — người dùng không cài Node/Python/Docker. `brew install` hoặc tải một file. Đây là điều kiện *ngầm* của "UI dễ dùng": nếu cài đặt đã khó thì UI đẹp mấy cũng vô nghĩa.

dandori-cli đã chứng minh xương này chạy được (Go 1.26, chi, modernc SQLite, pgx, Cobra — pure Go, no CGO). Bản mới **kế thừa nguyên xương đó**, không phát minh lại.

---

## Vì sao HTMX cho UI (đây là điểm khác dandori-cli)

Đây là chỗ bản mới **nâng cấp** so với cả hai bản cũ. dandori-cli hiện nhúng HTML tĩnh + Chart.js; `dandori/` render phía server kiểu truyền thống. Cả hai đều ổn nhưng chưa tối ưu cho tiêu chí "low-tech dễ dùng + tương tác mượt mà không nặng".

**HTMX** là điểm ngọt cho đúng bài toán này — một dashboard nội bộ, tương tác vừa phải, người dùng không rành kỹ thuật:

- **Nhẹ khủng khiếp** — thư viện ~14KB, nạp một dòng `<script>`. So với một SPA React điển hình ~800KB bundle. Trên mạng yếu, chênh lệch là *mở được ngay* vs *chờ trắng màn hình*.
- **Không build step** — không webpack, không npm, không transpile. Server trả về HTML, HTMX swap từng mảnh. Sửa UI = sửa template, F5. Đúng tinh thần Go single-binary: `go:embed` nhét cả UI vào chính binary.
- **Nhanh có số đo** — benchmark 2026: dashboard HTMX tải ~412ms vs ~2,847ms bản React tương đương; Lighthouse trên 3G: 94 vs 34.
- **Ít code** — các app CRUD-nặng (mà dashboard quản trị chính là CRUD-nặng) giảm 40–60% code frontend khi bỏ SPA.

Cụ thể ghép:

- **`html/template` + `go:embed`** — template server-render, nhúng thẳng vào binary. Zero external file.
- **HTMX** — tương tác động (lọc, phân trang, cập nhật ô, poll live agent grid) bằng attribute HTML, server trả HTML fragment. Không cần viết JS.
- **Tailwind qua CDN (hoặc CSS thuần)** — style nhanh, không cần bước build CSS. Nếu sau này cần tối ưu, đổi sang Tailwind CLI standalone binary (vẫn no-Node).
- **Chart.js qua CDN** — vẽ leaderboard, cost trend, grade distribution. Đủ đẹp, đủ nhẹ, không cần build.
- **Alpine.js (chỉ khi cần)** — cho tương tác client thuần (toggle, dropdown) không muốn round-trip. YAGNI: chỉ thêm khi HTMX không đủ.

> **Nguyên tắc UI:** low-tech mở trình duyệt là dùng được ngay, không tab load lâu, không lỗi JS, mọi thao tác chính làm được bằng chuột. Server-render + HTMX phục vụ đúng điều đó; SPA thì thừa cho ~chục người dùng nội bộ ("một khoản thuế tổ chức").

---

## Khi nào KHÔNG chọn stack này

Trung thực về giới hạn, để quyết định có cơ sở:

- **Cần realtime phức tạp** (nhiều luồng SSE/WebSocket lồng nhau, collaborative editing) → HTMX polling + SSE vẫn được, nhưng nếu tương tác client dày như một IDE thì cân nhắc thêm SPA cho *đúng khu vực đó*.
- **UI cực kỳ động, nhiều state phía client** → chỗ đó (và chỉ chỗ đó) có thể island bằng một framework nhẹ (Preact/Svelte). Không vì thế mà SPA-hoá toàn bộ.
- **Team chỉ biết JS/TS, không ai viết Go** → chi phí học Go là thật; khi đó cân nhắc giữ Node như `dandori/`. Nhưng đánh đổi lại là mất "nhẹ".

Với Dandori nội bộ, ba trường hợp trên đều chưa chạm tới → **Go + HTMX là lựa chọn đúng.**

---

## Ánh xạ stack vào ba trụ vision

Để thấy stack phục vụ đúng LEARN → GOVERN → CAPTURE, không phải chọn công nghệ cho vui:

| Trụ | Cần gì từ stack | Đáp bằng |
|---|---|---|
| **① CAPTURE** | ghi mọi run có cấu trúc, offline được | SQLite pure-Go, schema chung cho human + agent |
| **② GOVERN** | chặn tại tool-call, audit không sửa được | Go middleware (chi) làm guardrail; hash-chain trong SQLite |
| **③ LEARN** | grade, leaderboard, ROI, trend — hiện cho lãnh đạo xem | HTMX dashboard + Chart.js, provenance query thẳng từ raw data |

Server tổng hợp nhiều máy (PostgreSQL/pgx) là **tuỳ chọn** — mặc định một binary + một file SQLite chạy trọn vòng. Đúng tinh thần "nhẹ".

---

## Chốt

**Go 1.26 single-binary · chi · modernc SQLite (Postgres tuỳ chọn) · html/template + go:embed + HTMX + Tailwind CDN + Chart.js · Cobra · YAML config.**

Kế thừa xương đã chạy của dandori-cli, chỉ nâng đúng phần UI bằng HTMX để đạt tiêu chí "dễ dùng với low-tech" mà vẫn giữ "nhanh & nhẹ".

---

## Đọc tiếp

- **Tầm nhìn** — ba trụ LEARN → GOVERN → CAPTURE: [01-product-vision.md](01-product-vision.md)
