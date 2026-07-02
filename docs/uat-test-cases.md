# Kịch bản kiểm thử Dandori (UAT) — dành cho người không rành kỹ thuật

Tài liệu này hướng dẫn bạn **tự tay bấm thử** toàn bộ Dandori để xác nhận nó chạy đúng.
Không cần biết lập trình. Chỉ cần làm theo từng bước, rồi so với cột "Kết quả mong đợi".

## Chuẩn bị 1 lần

1. Mở **Terminal** (ứng dụng dòng lệnh trên máy).
2. Gõ lệnh sau rồi Enter (khởi động Dandori với dữ liệu thử riêng, an toàn):
   ```
   cd đường/dẫn/tới/my-dandori
   ./dandori serve --db .data/uat.db
   ```
3. Thấy dòng `dandori console → http://127.0.0.1:4777` là chạy được.
4. Mở trình duyệt (Chrome/Safari), vào địa chỉ: **http://127.0.0.1:4777**
5. Khi thử xong, quay lại Terminal bấm **Ctrl + C** để tắt.

> Mẹo: mọi thao tác đều KHÔNG gửi gì ra ngoài (Slack/Jira…) vì đang ở chế độ an toàn (`DRY_RUN`). Cứ bấm thoải mái.

---

## Phần A — Bảng điều hành (màn hình chính của CEO)

| # | Việc cần làm | Kết quả mong đợi |
|---|---|---|
| A1 | Vào http://127.0.0.1:4777 | Hiện trang **"Bảng điều hành"** bằng tiếng Việt, có dòng **"Giá trị AI mang lại"** |
| A2 | Nhìn khối "Các đội dự án" | Thấy các đội dạng thẻ. Đội tốt = **chấm xanh**, cần theo dõi = **vàng**, cần chú ý = **đỏ**, chưa có dữ liệu = **xám** |
| A3 | Nhìn khối "Việc cần bạn xử lý" | Nếu có đề xuất chờ duyệt sẽ hiện thẻ kèm nút **Duyệt** / **Bỏ qua**. Nếu không có, hiện "Không có việc nào cần bạn lúc này 🎉" |
| A4 | Bấm vào một thẻ đội | Chuyển sang trang chi tiết kỹ thuật của đội đó |
| A5 | Trên thanh trên cùng, bấm nút **"Kỹ thuật ⚙"** | Chuyển sang khu kỹ thuật (Standup/Org/Runs…) dành cho trưởng nhóm |
| A6 | Ở khu kỹ thuật bấm **"Điều hành ⛩"** | Quay lại Bảng điều hành. Lựa chọn được **ghi nhớ** khi tải lại trang |

---

## Phần B — Trợ lý điều hành (chatbot)

> Cần đã cấu hình `OPENROUTER_API_KEY` và `OPENROUTER_MODEL` trong file `.env`. Nếu chưa có, mục này sẽ báo "Trợ lý tạm không khả dụng" — đó là hành vi đúng, không phải lỗi.

| # | Việc cần làm | Kết quả mong đợi |
|---|---|---|
| B1 | Bấm **"Mở trợ lý"** (hoặc menu "Trợ lý") | Mở trang chat tiếng Việt, có ô nhập và vài câu hỏi gợi ý |
| B2 | Gõ: *"Chi phí AI tuần này là bao nhiêu?"* rồi bấm Gửi | Trợ lý trả lời bằng tiếng Việt kèm **con số thật** lấy từ hệ thống (không bịa) |
| B3 | Gõ: *"Team nào đang dùng agent hiệu quả nhất?"* | Trả lời so sánh các đội (nếu chưa khai báo đội, trợ lý nói cần tạo đội) |
| B4 | Gõ: *"Dừng agent X giúp tôi"* (X là tên bất kỳ đang chạy) | Trợ lý nói **đã tạo yêu cầu chờ duyệt** — và **KHÔNG có gì bị dừng ngay**. Đây là điểm an toàn quan trọng |
| B5 | Quay lại **Cần duyệt** | Thấy yêu cầu vừa tạo đang chờ. Bạn là người bấm duyệt cuối cùng, không phải chatbot |

---

## Phần C — Duyệt / Bỏ qua đề xuất

| # | Việc cần làm | Kết quả mong đợi |
|---|---|---|
| C1 | Ở Bảng điều hành, tại một thẻ trong "Việc cần bạn", bấm **Duyệt** | Thẻ biến mất, số đếm giảm, thao tác được lưu lại (có dấu vết kiểm toán) |
| C2 | Bấm **Bỏ qua** ở một thẻ khác | Thẻ biến mất, đề xuất chuyển sang trạng thái "đã bỏ qua" |
| C3 | Sau khi duyệt, để ý dòng chữ | Ghi **"Đã duyệt (bảng điều khiển)"** — không ghi tên cá nhân (đúng thiết kế) |

---

## Phần D — Các đội (Teams)

Thực hiện trong Terminal (mở cửa sổ Terminal thứ hai, giữ nguyên cửa sổ đang chạy server):

| # | Gõ lệnh | Kết quả mong đợi |
|---|---|---|
| D1 | `./dandori team add "Đội Thanh Toán" --db .data/uat.db` | In ra `team #… "Đội Thanh Toán"` |
| D2 | `./dandori team list --db .data/uat.db` | Liệt kê đội vừa tạo |
| D3 | `./dandori team assign "Đội Thanh Toán" --agent <mã-agent> --db .data/uat.db` | Gán agent vào đội (mã agent xem ở trang Runs). Báo thành công |
| D4 | Tải lại Bảng điều hành trên trình duyệt | Đội mới xuất hiện thành một thẻ |

---

## Phần E — "Người quan sát" tự động (Master Observer)

| # | Gõ lệnh | Kết quả mong đợi |
|---|---|---|
| E1 | `./dandori observe run --db .data/uat.db` | In ra `observer: surfaced=… proposed=… applied=… deduped=…` không báo lỗi |
| E2 | Chạy lại lệnh trên lần nữa | Lần hai `deduped` tăng, `surfaced/proposed` không sinh trùng (chống spam) |
| E3 | Nếu có `proposed`, mở **Cần duyệt** trên web | Thấy đề xuất do hệ thống tự sinh, chờ bạn duyệt |

---

## Phần F — Chia sẻ cách làm hay (Flywheel / Playbook)

| # | Gõ lệnh | Kết quả mong đợi |
|---|---|---|
| F1 | `./dandori flywheel detect --db .data/uat.db` | Liệt kê các run "sạch, prompt rõ" đáng lưu thành mẫu (hoặc báo "no candidates" nếu chưa có) |
| F2 | `./dandori flywheel promote <mã-run> --db .data/uat.db` | Tạo một playbook (mẫu) từ run đó |
| F3 | Mở menu **Playbooks** trên web | Thấy playbook vừa tạo |
| F4 | `./dandori flywheel publish <id-playbook> --db .data/uat.db` | Báo `slack=dry-run confluence=dry-run` (an toàn — không gửi thật vì đang DRY_RUN) |

---

## Phần G — An toàn & không thể lách (kiểm tra quan trọng)

| # | Việc cần làm | Kết quả mong đợi |
|---|---|---|
| G1 | Trong khu **Kỹ thuật**, bấm nút đỏ **"Kill switch"** rồi xác nhận | Nút chuyển sang "KILL SWITCH ON". Mọi lệnh của agent sẽ bị chặn |
| G2 | Bấm lần nữa để tắt kill switch | Trở lại bình thường |
| G3 | Dù dùng chatbot yêu cầu "duyệt hết" / "dừng hết" | Hệ thống **không bao giờ** tự duyệt hay tự dừng — luôn chỉ tạo yêu cầu chờ người bấm |

---

## Nếu có gì sai

- Ghi lại: bạn bấm gì, màn hình hiện gì, khác "Kết quả mong đợi" ra sao.
- Chụp màn hình nếu được.
- Gửi cho nhóm kỹ thuật kèm số thứ tự (ví dụ "A3 sai").

## Dọn dẹp sau khi thử

- Quay lại Terminal đang chạy server, bấm **Ctrl + C**.
- Dữ liệu thử nằm trong `.data/uat.db` — xoá đi nếu muốn làm lại từ đầu:
  ```
  rm .data/uat.db .data/uat.db-shm .data/uat.db-wal
  ```
