# Dandori data-driven: đo cái gì, tính thế nào, và vì sao đáng tin

> Tài liệu cho lãnh đạo. Ba câu hỏi được trả lời theo thứ tự: Dandori **thu** dữ liệu gì · **tính** ra con số gì (công thức cụ thể, không hộp đen) · và **vì sao các công thức đó thực sự đo được** agent và team, không phải số ngẫu nhiên.
> Mọi công thức dưới đây trích từ code đang chạy thật, không phải tính năng hứa hẹn.

---

## Vấn đề: bạn không thể quản thứ bạn không đo

Tổ chức hiện đại có hai loại người làm việc: con người và agent AI. Con người để lại dấu vết ở Jira, GitHub. Agent để lại dấu vết trong log rải rác và hoá đơn token cuối tháng. Hai dòng dấu vết này không tự gộp thành một bức tranh, nên những câu hỏi tưởng đơn giản lại không có câu trả lời bằng số:

- Tháng này chi bao nhiêu cho AI, bao nhiêu trong đó **thực sự tạo giá trị**?
- Agent nào giỏi, agent nào đang đốt tiền — **bằng chứng đâu**?
- Giao việc khó cho agent nào thì **yên tâm**?

Dandori trả lời bằng một chuỗi ba bước: **CAPTURE** (thu mọi thứ thành dữ liệu có cấu trúc) → **LEARN** (tính điểm, ROI, xếp hạng) → **GOVERN** (điểm số có hệ quả thật: nới hoặc siết quyền). Tài liệu này đi qua đúng chuỗi đó.

---

## Phần 1 — Thu gì: dữ liệu thô làm nền cho mọi con số

Mỗi lần agent chạy, một lớp mỏng (hook) đứng **ngoài agent** tự ghi lại — người dùng không làm gì thêm, agent không can thiệp được vào bản ghi:

| Nhóm | Ghi những gì | Dùng để tính |
|---|---|---|
| **Định danh & chi phí** | agent · project · task Jira · model · token (input/output/**cache**) · $ · trạng thái (done/failed/killed) · thời lượng | cost attribution, ROI, hiệu suất model |
| **Hành vi công cụ** | từng tool call pass/fail · lệnh bị guardrail chặn · điểm phải xin phép người | Acceptance, Reliability, Autonomy |
| **Can thiệp của người** | tin nhắn người gửi **giữa chừng** run (đã redact secret) · lần duyệt/từ chối | Autonomy, audit |
| **Số phận code** | dòng thêm/xoá thật (đo từ git) · commit bị **revert** về sau | Acceptance |
| **Ngữ cảnh** | phiên bản context nào được tiêm vào run | chẩn đoán "vì sao tệ" |
| **Việc của con người** | Jira issue, GitHub PR — kéo về **cùng một bảng** với agent run | so người vs AI cùng thước đo |

Ba điểm nền tảng:

1. **Không phiên nào vô hình** — ngoài hook còn một watcher quét lại lịch sử, bắt cả phiên chạy ngoài quy trình.
2. **Người và AI chung một cấu trúc** — điều kiện để mọi so sánh về sau là táo-với-táo.
3. **Từ v10, mọi bản ghi có danh tính thật** — ai chạy, ai duyệt, ai dừng: từng hành động quản trị gắn với một con người cụ thể đã đăng nhập, ghi vào sổ audit không sửa được.

---

## Phần 2 — Tính gì: các công thức, trình bày không giấu

### 2.1. Bốn chỉ số của một agent (mỗi chỉ số 0–100)

Đúng bốn câu tổ chức nào cũng hỏi về một nhân sự:

| Chỉ số | Câu hỏi | Công thức (đúng như code chạy) |
|---|---|---|
| **Acceptance** | Code viết ra có được giữ lại không? | `100 × (1 − số_edit_bị_loại / tổng_edit)` — edit bị loại = bị người/guardrail từ chối **∪** nằm trong run có commit bị git revert |
| **Success** | Việc cuối cùng có xong không? | `100 × run_done / run_đã_kết_thúc` — run gắn task Jira chỉ tính done khi **Jira báo done**; không gắn thì phải kết thúc sạch, không mang cờ lỗi mở |
| **Autonomy** | Tự chạy được, hay phải người cứu? | `100 × (1 − run_bị_can_thiệp / tổng_run)` — can thiệp = xin phép giữa chừng **hoặc** người phải nhắn tin giữa run (prompt mở đầu không tính) |
| **Reliability** | Ổn định hay hay hỏng? | `100 × (1 − trung_bình(tỉ_lệ_tool_lỗi, tỉ_lệ_bị_guardrail_chặn, tỉ_lệ_bị_kill))` |

**Composite** = trung bình cộng **bốn chỉ số, trọng số bằng nhau** — cố ý không có núm tinh chỉnh ẩn nào để "nắn" điểm.

### 2.2. Grade A–F: calibrate theo chính đội của bạn, không ngưỡng gõ tay

Composite không quy ra hạng bằng ngưỡng cứng kiểu "trên 80 là A" — vì 80 là con số cảm tính. Thay vào đó:

> Xếp composite của agent vào **phân phối của cả fleet** (mọi agent đang hoạt động, cộng cả baseline con người ẩn danh nếu bật):
> **A ≥ phân vị 80 · B ≥ 60 · C ≥ 40 · D ≥ 20 · F dưới đó.**

"Agent hạng A" nghĩa là: *tốt hơn 80% lượt làm việc trong chính tổ chức bạn*. Đội mạnh lên thì thước tự nâng theo. Hai van an toàn: fleet dưới 5 thực thể → dùng thang tĩnh và **dán nhãn "chưa calibrate"**; agent dưới 5 run trong kỳ → gắn cờ **"độ tin cậy thấp"** thay vì giả vờ chắc chắn.

### 2.3. ROI: tách phần lãng phí khỏi hoá đơn

Ba xô **loại trừ lẫn nhau** — mỗi đô-la chỉ bị đếm một lần:

```
Lãng phí = $run_failed/killed  (mất trắng)
         + $run_done_nhưng_còn_cờ_lỗi_mở  (chưa dùng được)
         + $run_sạch × (1 − Acceptance%)  (phần code bị vứt)
```

> *"Agent này tốn $8,200 — 38% ($3,100) là lãng phí: $1,200 run fail, $700 run còn lỗi treo, $1,200 là phần code bị sửa lại. Giá trị thật: $5,100."*

Mỗi xô kèm danh sách run cụ thể — click vào đô-la nào cũng ra được run sinh ra nó.

### 2.4. Trust: điểm số có hệ quả, không phải dashboard trưng bày

"Trust" trong Dandori không phải một con số trừu tượng — nó là **quyền tự chủ** được cấp/thu theo thành tích, như thăng giáng chức:

- Mỗi agent thuộc một **band**: `supervised` (mọi edit cần duyệt) · `gated` (mặc định, rule gate áp dụng) · `trusted` (bỏ gate thường, giữ gate critical).
- **Closed loop tự động**: grade rơi xuống **F** → tự giáng về supervised + mở review; xuống **D** → đề xuất giáng chờ người duyệt + mở ticket Jira; phục hồi → tự gỡ cờ. Mỗi bước vào audit.

Đây là chỗ LEARN chảy ngược vào GOVERN: điểm thấp không chỉ "được nhìn thấy" — nó **siết quyền ngay**, và điểm lên thì nới lại.

**Cùng logic đó áp cho tri thức, không chỉ agent:** khi dữ liệu cho thấy một cách làm/skill thực sự cải thiện kết quả (chênh lệch present-vs-absent đủ mẫu, khoảng tin cậy tách biệt rõ), Dandori đưa nó qua đúng một vòng — người duyệt xem toàn bộ nội dung trước khi công bố, rồi đo tiếp before/after ở từng người áp dụng. Đo được là *giữ*; đo ra tệ hơn là *đề xuất rút lại*, không bao giờ tự động — người luôn quyết câu cuối, số liệu chỉ đưa bằng chứng lên bàn.

### 2.5. Hiệu suất chi phí (insights): model nào đáng tiền

Tầng phân tích mô tả thuần (cố ý **không** trộn vào điểm agent):

- **Cost-per-done theo model**: `$tổng / số run done` — model nào rẻ trên mỗi kết quả thật.
- **Cache-hit %**: bao nhiêu phần token được tái dùng — đòn bẩy giảm chi phí lớn nhất trong thực tế.
- **Cost-per-outcome theo project/agent**: `$X / N việc xong` — hiện rõ tử số mẫu số, run đang chạy loại khỏi mẫu.
- Nhóm dưới **3 mẫu** bị dán nhãn **"chưa đủ mẫu"** ngay trên bảng — không bao giờ để một con số mỏng đứng cạnh con số dày như thể ngang hàng.

Cùng họ: **ước lượng tác động trước khi duyệt** ("hành động tương tự trước đây trung bình tốn $X, đụng Y file" — chỉ hiện khi ≥3 mẫu) và **gợi ý giao việc** theo lịch sử done-rate.

---

## Phần 3 — Vì sao các công thức này đo được thật, không phải số ngẫu nhiên

Đây là phần quan trọng nhất với người ra quyết định: một bộ chỉ số chỉ đáng dùng khi nó chịu được năm câu chất vấn sau.

### 1. Nó đo *kết quả*, không đo *hoạt động*

Các thước đo năng suất thất bại kinh điển (số dòng code, số giờ, số commit) đều đo hoạt động — thứ dễ bơm phồng. Bốn chỉ số của Dandori đều neo vào một **sự kiện có hậu quả thật**: code *sống sót qua review và không bị revert* (Acceptance), task *được Jira xác nhận xong* (Success), *con người có phải bỏ việc riêng ra cứu không* (Autonomy), *công cụ có hỏng thật không* (Reliability). Không cái nào tăng được bằng cách "làm nhiều hơn" — chỉ tăng được bằng cách "làm đúng hơn".

### 2. Tín hiệu nằm ngoài tầm tay kẻ bị đo

Nguyên tắc thiết kế cứng: **bên bị chấm điểm không được tự phát tín hiệu chấm điểm**. Revert đọc từ git (hành động của người khác, sau khi run kết thúc). Trạng thái done đọc từ Jira (do PO chuyển, không phải agent tự khai). Guardrail block và permission ask được ghi bởi tầng hạ tầng đứng *trước* agent, agent không chặn được việc ghi. Một agent muốn điểm đẹp chỉ có một đường: thật sự làm việc tốt hơn.

### 3. Không có núm chỉnh ẩn — và ngưỡng do chính dữ liệu của bạn đặt

Composite là trung bình cộng bốn chỉ số trọng số bằng nhau: không ai "ưu tiên" được chỉ số nào trong bóng tối. Hạng A–F đặt theo **phân vị trên chính fleet của bạn**, không phải một hằng số ai đó gõ vào config. Khi có người hỏi *"vì sao nó hạng B?"*, câu trả lời không phải "vì quy định thế" mà là *"vì composite của nó đứng ở phân vị 63 trong 12 thực thể của tổ chức, đây là bốn chỉ số thành phần và danh sách run tạo ra chúng"* — mỗi con số trên UI mang theo đúng công thức và ID dữ liệu nguồn của nó (provenance). Cùng đầu vào luôn cho cùng đầu ra.

### 4. Thiếu dữ liệu được nói thẳng, không được nguỵ trang thành điểm

Đây là chỗ phần lớn dashboard gian dối một cách vô tình. Dandori có ba van chống điều đó, đã được kiểm nghiệm thật trong quá trình phát triển:

- Không có dữ liệu → chỉ số trả **trung tính kèm dòng chữ nói rõ** ("no edit tool calls in window"), không phải một con số 0 trông như "tệ" hay 100 trông như "giỏi".
- Mẫu mỏng → dán nhãn ("chưa đủ mẫu", "độ tin cậy thấp", "chưa calibrate") ngay cạnh con số.
- **Từ chối tính khi nền chưa đủ**: cuối v9, một bản nâng cấp công thức đã bị chính quy trình red-team của dự án **hoãn lại** sau khi đối chiếu từng cột dữ liệu thật — vì tinh chỉnh trọng số trên các cột còn trống là "rửa" dữ-liệu-thiếu thành điểm số. Capture được vá trước, công thức chờ dữ liệu dày mới đụng. Một hệ thống dám *không* ra số khi chưa đủ căn cứ là hệ thống bạn có thể tin lúc nó ra số.

### 5. Con số dẫn đến hành động được ghi sổ — nên nó buộc phải đúng

Ở dashboard trưng bày, số sai không ai chịu trách nhiệm. Ở Dandori, grade D/F **tự kích hoạt** giáng quyền và mở review; mọi hành động đó (và người duyệt nó) vào **audit hash-chain không sửa được**. Khi con số có hệ quả pháp lý-vận hành và có chữ ký, sai số không thể sống lâu — mọi mắt xích đều truy ngược được về run gốc để kiểm chứng.

### Giới hạn — nói trước, không để bạn tự phát hiện

Trung thực về thước đo cũng là một phần của độ tin: (a) agent chỉ-đọc (không edit code) hiện trung tính ở Acceptance — cần đọc kèm cờ "ít tín hiệu"; (b) Acceptance dựa vào git, repo không dùng git thì mù tín hiệu revert; (c) ROI chưa tính chi phí *thời gian người* (agent 2h vs người 10h) — nằm trong lộ trình khi có baseline đáng tin; (d) đo lường quá tay sinh Goodhart, nên Dandori chấm *pattern và kết quả*, calibrate theo fleet, và **không công khai xếp hạng con người** (baseline người là ẩn danh, tắt được).

---

## Bằng chứng: chạy thật, không phải demo

Toàn bộ pipeline trên đã được kiểm chứng end-to-end trên dữ liệu thật của chính máy phát triển: **37 phiên AI thật** từ ~20 dự án (tổng chi phí thật ~$6,400, 7 model khác nhau, cache-hit trải từ 0% đến 100%), hợp nhất cùng **24 đầu việc Jira + 2 PR GitHub**; bảng xếp hạng A–F, ROI tách lãng phí theo từng project, và trang insights đã được đối chiếu **từng ô số trên trình duyệt với truy vấn SQL gốc — khớp chính xác 100%**. Guardrail chặn thật (đã kiểm chứng sống bằng lệnh nguy hiểm), audit chuỗi hash xác minh được bằng một lệnh.

**Phân tích nâng cao (insights) — cùng nguyên tắc, không phải ngoại lệ:** bản mở rộng gần nhất thêm các lát cắt sâu hơn (chi phí theo phiên bản context, "việc còn bị sửa sau khi agent báo xong", tỉ lệ chặn theo từng rule, kinh tế học của việc người phải chỉnh lái agent giữa chừng) — nhưng khi một lát cắt chưa có đủ dữ liệu thật để tin (ví dụ: đội chưa cấu hình Context Hub), trang **hiện thẳng "chưa có dữ liệu"** thay vì vẽ biểu đồ trống trông như số 0 có ý nghĩa. Đây chính là van #4 ở Phần 3 đang hoạt động, không phải lời hứa suông.

---

## Tri thức tuyển-lọc (v13) — cùng kỷ luật honest-data

Bản mới nhất thêm một nhánh khác hẳn: biến run tốt thành tri thức tái dùng, phân phối được — nhưng **không có con số mới nào bị bịa ra để làm việc đó**.

- **Mining là "run đáng đọc", không phải bảng xếp hạng.** Bốn tín hiệu (steering lặp lại, fail-rồi-thành-công, bị chặn-rồi-vẫn-xong, chi phí bất thường) chỉ gom **run**, không có cột operator, không xếp hạng ai. Dismiss một run khỏi tab này không xoá dấu vết nó ở bất cứ đâu khác — audit, run-detail vẫn thấy đủ.
- **AI-draft chỉ sinh văn xuôi, không bao giờ sinh số.** Bản nháp tri thức do LLM viết từ bằng chứng DB (đã redact, chưa từng đọc transcript gốc) là **mô tả**, không phải một chỉ số mới cộng vào composite/grade — người vẫn phải đọc và sửa trước khi đưa vào review.
- **Cost-outlier là nhãn hai chiều, không phải "xấu".** Một run tốn gấp 3× median project có thể là dấu hiệu lãng phí — hoặc dấu hiệu một việc khó hơn bình thường đang được làm đúng. Nhãn chỉ ra "đáng nhìn kỹ hơn", không tự kết luận tốt/xấu thay người.

---

## Tóm lại một trang

1. **Thu**: mọi phiên AI (ai · task · $ · token/cache · tool pass/fail · can thiệp của người · số phận code qua git) + việc của con người từ Jira/GitHub — về một cấu trúc chung, ghi bởi tầng đứng ngoài agent, gắn danh tính thật.
2. **Tính**: 4 chỉ số kết-quả (Acceptance · Success · Autonomy · Reliability) → composite trọng-số-bằng-nhau → hạng A–F **calibrate theo phân vị fleet** → ROI tách lãng phí ba xô → trust band có hệ quả thật → insights chi phí theo model/project.
3. **Đáng tin vì**: đo kết quả chứ không đo hoạt động · tín hiệu ngoài tầm tay kẻ bị đo · không ngưỡng ẩn, mọi số truy được về run gốc · thiếu dữ liệu nói thẳng thay vì nguỵ trang · và con số kéo theo hành động được ghi sổ audit — nên nó buộc phải chịu được chất vấn.

Khoảng cách giữa tổ chức *dùng* AI hiệu quả và tổ chức chỉ *có* AI nằm ở chỗ này: biến được dấu vết rải rác thành con số dám đặt lên bàn họp quý.
