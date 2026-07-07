# Hướng dẫn cho Engineer

> Bạn viết code cùng AI hàng ngày. Dandori chạy ngầm — không đổi cách bạn dùng Claude Code. Guide này cho bạn biết những gì đang được ghi, cách xem điểm của mình, và cách lấy/đóng góp tri thức chung.

---

## Dandori thấy gì về công việc của bạn

Sau khi dự án đã `dandori init`, mỗi phiên Claude Code tự sinh một bản ghi. Bạn **không phải làm gì thêm**. Mỗi run ghi: agent nào · task (Jira key nếu có) · model · token (input/output/cache) · chi phí $ · trạng thái (done/failed/killed) · dòng code thêm/xoá · tool nào pass/fail · tin nhắn bạn gửi giữa chừng (đã che secret) · guardrail có chặn gì không.

Xem run của chính mình: **`/runs`** → click một run để thấy timeline tool-call, chỗ nào fail, context nào được tiêm vào.

## Grade của bạn nghĩa là gì

Vào **`/dash/agent/{tên-agent}`**. Mỗi agent được chấm 4 chỉ số (0–100), rồi quy ra hạng **A–F**:

| Chỉ số | Trả lời | Tăng bằng cách |
|---|---|---|
| **Acceptance** | Code viết ra có được giữ không? | Đừng để code bị revert / bị người sửa lại hết |
| **Success** | Task cuối cùng có xong không? | Đóng task trên Jira thật (không tự khai) |
| **Autonomy** | Tự chạy được, hay phải người cứu? | Ít cần can thiệp giữa chừng |
| **Reliability** | Ổn định, hay hay hỏng? | Ít tool lỗi / bị guardrail chặn / bị kill |

**Hạng calibrate theo cả đội**, không phải ngưỡng cứng: A = phân vị ≥80 (tốt hơn 80% lượt làm trong tổ chức, kể cả của người). Đội mạnh lên, thước tự nâng. Đội dưới 5 thực thể hoặc agent dưới 5 run → dán nhãn "chưa đủ mẫu / độ tin cậy thấp" thay vì giả vờ chắc chắn.

> Mỗi con số **truy ngược được về run gốc** — click vào là ra dữ liệu thô. Không có ngưỡng ẩn.

## Khi lệnh bị chặn

Nếu Claude Code báo bị deny với `[dandori G...]`, đó là guardrail gác **trước khi** lệnh chạy:

- **G1 block** — lệnh nguy hiểm (`rm -rf /`, đụng `.env`, force-push). Deny thẳng, không qua được.
- **G2 sandbox** — Write/Edit ra ngoài thư mục dự án. Deny.
- **G3 budget** — vượt trần chi phí tháng. Deny.
- **G4 gate** — `git push` / merge PR / deploy. Tạo **yêu cầu duyệt**, chờ người admin duyệt (web `/reviews` hoặc Slack ✅), rồi cho qua hoặc từ chối.

Nếu bị gate chặn: nhờ admin/manager duyệt ở `/reviews`, hoặc chờ reaction Slack. Guardrail nằm **ngoài code của agent** nên không lách được bằng prompt — đó là chủ đích.

---

## Lấy tri thức chung về máy (v13)

Đội đã tổng hợp practice/skill/kit tốt. Bạn kéo về repo của mình:

### Pull một skill lẻ

```bash
dandori skill list                    # xem skill đã publish + trạng thái cài local
dandori skill pull <tên>              # kéo vào .claude/skills/<tên>/SKILL.md
```

`pull` verify hash 3 lớp (chống giả mạo), hiện diff trước khi ghi, hỏi xác nhận. File vào **repo-local `.claude/skills/`** — nằm trong `git status` để bạn review như code thường.

### Pull một agent-kit trọn bộ

```bash
dandori kit list                      # kit đã publish + trạng thái cài
dandori kit pull <tên>                # kéo cả bộ agents/rules/skills/commands (.md)
```

Kit là **bộ file `.claude/` chuẩn** đóng gói lại — cùng cơ chế an toàn: hash-pin từng file, symlink-safe, một lần xác nhận cho cả bộ. **Không bao giờ** đụng `hooks/`, `settings.json`, `scripts/` (đó là dây điện guardrail).

> File đã có trên máy mà kit thiếu → giữ nguyên, không xoá. Bạn sở hữu máy mình.

---

## Đóng góp practice bạn học được

Khi bạn phát hiện một cách làm hay trong lúc code, đưa nó lên cho cả đội:

### Cách 1 — Đề cử tay

Trên **`/knowledge`** bấm nút đề cử, hoặc từ trang run detail. Bạn viết practice (markdown), nó vào hàng đợi review. Viewer cũng đề cử được — admin duyệt.

### Cách 2 — Để AI soạn nháp (v13)

Trên **`/knowledge/mining`** (hàng đợi "run đáng đọc") hoặc trang run detail, bấm **"Soạn nháp (AI)"** (chỉ hiện khi đã nối OpenRouter). AI đọc bằng chứng từ run (đã che secret, không đọc raw transcript) và soạn một nháp practice. **Bạn sửa và chịu trách nhiệm** trước khi đề cử — nháp mang nhãn `origin: ai-draft` suốt vòng đời để người review biết.

### Import bài học đã viết

Nếu bạn đã ghi bài học vào memory/journal:

```bash
dandori knowledge import --memory --project my-app    # lấy từ ~/.claude/projects/.../memory/*.md
dandori knowledge import --journals                   # lấy từ docs/journals/*.md
```

Từng file được xem trước, quét secret, khử trùng lặp rồi đưa vào hàng đợi review.

Chi tiết cả vòng: [05-knowledge-flow.md](05-knowledge-flow.md).

---

## Câu hỏi nhanh

- **Điểm tôi thấp vì đọc/test nhiều, không sửa code?** — Acceptance để trung tính khi không có edit; đọc kèm cờ "ít tín hiệu". Không bị phạt oan.
- **Tôi làm trên `main`, không có Jira key** — Success dựa vào "kết thúc sạch không cờ lỗi" khi không link được Jira. Coverage task_key thấp là honest, không phải lỗi.
- **AI-draft có làm loãng chất lượng không?** — Nháp phải qua người sửa + review gate mới publish; nhãn `ai-draft` minh bạch. Không auto-publish.
