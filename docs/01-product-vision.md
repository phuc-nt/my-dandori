# Dandori: Harness quản trị cho đội AI của bạn

> Tầm nhìn sản phẩm, bản đọc chứ không phải slide.
> Cho lãnh đạo nhìn hoá đơn AI cuối tháng mà không rõ tiền đi đâu. Không cần biết code vẫn đọc được.

---

## Một câu chưa ai trả lời được

Tập đoàn đặt cược lớn vào AI. Mục tiêu **x100**: kỳ vọng AI nhân năng suất cả công ty lên gấp trăm lần. Tiền đổ vào token, đội nào cũng được trang bị agent, code AI đẩy thẳng lên repo.

Rồi tới cuộc họp quý. Một câu hỏi tưởng đơn giản:

> *AI thật sự đóng góp bao nhiêu vào năng suất chung?*

Im lặng. Đặt cược trăm lần mà không có lấy một thước đo.

Không phải vì AI kém, AI làm tốt. Không phải vì đội kém. Câu hỏi rơi vào khoảng không vì thứ để trả lời nó **chưa được xây**: một lớp quản trị bọc quanh agent. Ngành đang gọi lớp đó là **harness**, và 2026 là năm nó thành trọng tâm đầu tư: sau *prompt engineering*, sau *context engineering*, giờ là *harness engineering*.

Nói thẳng: Dandori không hứa con số x100. Nó hứa thứ nền tảng hơn: **cho bạn biết con số thật là bao nhiêu**. Đo xong có thể là x100, có thể là x3. Biết được x3 mà kiểm soát được vẫn hơn tin x100 mà mù.

---

## Agent = Model + Harness

Cả ngành đang chốt quanh một công thức: **`Agent = Model + Harness`**. Model lo suy luận. Mọi thứ *không phải* model (context, guardrail, sensor, sandbox, orchestration) là **harness**, và chính harness quyết định agent có đáng tin trong production hay không.

Bằng chứng cứng: đội LangChain đưa coding agent của họ **từ hạng 30 lên hạng 5** trên benchmark ngành mà *không đổi model*, chỉ tối ưu harness. Chiều ngược cũng đúng: phần lớn sự cố AI doanh nghiệp không đến từ model yếu mà từ **lỗi harness** (context trôi, schema lệch, state phân rã). Reliability nằm ở harness.

Bạn đã chạm vào harness mỗi ngày mà không gọi tên: cái `CLAUDE.md` viết lúc 11h đêm, đoạn prompt review copy lần thứ N, script tự lint sau mỗi phiên. Vấn đề: chúng nằm rải rác trên laptop từng người, chưa thành một lớp.

### Có hai loại harness, và chúng khác nguồn gốc

- **Harness dựng sẵn (provider làm):** tool loop, sandbox, permission cơ bản. Anthropic, OpenAI, Cursor đã đốt hàng chục triệu đô. Bạn không cần xây lại.
- **Harness tuỳ biến (công ty làm):** compliance, audit, chuẩn chất lượng, quy tắc domain, cách chia sẻ tri thức. **Không nhà cung cấp nào xây giúp**, vì họ không biết công ty bạn vận hành ra sao.

Ranh giới này **sẽ dịch chuyển**: provider liên tục nuốt dần phần tuỳ-biến vào phần dựng-sẵn. Nhưng có một phần họ **không bao giờ chuẩn hoá được**, vì nó gắn cứng với từng tổ chức: *chuẩn chất lượng của riêng bạn, quy tắc tuân thủ của ngành bạn, tri thức của chính đội bạn*. Đó là phần tuỳ-biến bền vững, và là phần Dandori chọn.

**Dandori là nửa đó**: harness tuỳ biến ở cấp tổ chức. Nó không viết code, không thay Claude Code hay Cursor. Nó bọc bên ngoài, biến một đám agent chạy lẻ thành **một đội ngũ được quản trị**.

### Vì sao không tự dựng lấy?

Câu hỏi đúng của người mua. Một script lint rời, một prompt review rời thì ai cũng viết được. Thứ **không** tự dựng được không phải từng mảnh, mà là **sự nối giữa chúng thành một vòng khép**: một guardrail chặn được ghi lại → tín hiệu đó chảy vào điểm số agent → điểm thấp tự mở review → quyết định vào sổ audit không sửa được. Từng mảnh thì tầm thường; *cái vòng* mới là thứ tổ chức không dựng nổi bằng script rời trên laptop. Đó là lý do Dandori là một lớp, không phải một bộ tiện ích.

---

## Harness đó gồm gì

Đồng thuận 2026 mô tả harness production đủ chuẩn gồm năm tầng: *tool orchestration · verification loops · context & memory · guardrails · observability*. Ba tầng đầu là chỗ agent **chạy**, nửa dựng sẵn lo phần lớn. Hai tầng cuối, **guardrails và observability**, là chỗ **tổ chức chịu trách nhiệm**, và là chỗ hầu hết công ty đang trống.

Dandori dựng đúng phần trống đó, gói thành **một vòng đời**: chính cái vòng tổ chức 100 năm nay vẫn dùng để quản con người: *nhìn kết quả để đánh giá → quyết định & hành động → ghi lại mọi việc → lặp lại, tốt hơn*. Ba trụ:

```
   ③ LEARN          ──▶   ② GOVERN        ──▶   ① CAPTURE
   biến run thành          phân bổ quyền          trí nhớ có cấu
   tri thức hành động       tin cậy               trúc của tổ chức
        ▲                                              │
        └──────── tri thức quay lại, vòng sau tốt hơn ─┘
```

Một điểm xuyên suốt: *ba trụ không phải ba tính năng rời*. Mỗi trụ vừa là **nguồn** vừa là **bể chứa** của hai trụ kia; dữ liệu chảy hai chiều qua cả vòng. Đó là điều khiến nó là hệ thống, không phải ba dashboard.

---

## Trụ ③ · LEARN: biến mỗi run thành tri thức hành động được

Tuyển một engineer, vài tháng sau có **performance review**: giỏi lên không, giao việc khó được chưa, chỗ nào cần kèm. Agent thì không, chỉ có chat log và cảm giác lờ mờ. Đây là khoảng trống lãnh đạo thấy trước nhất.

LEARN cho mỗi agent một **bản đánh giá khách quan**: grade A–F, vài câu nhận xét tiếng người, xu hướng, xếp hạng so với cả fleet. Bốn chỉ số, đúng bốn câu tổ chức nào cũng hỏi về một nhân sự:

| Chỉ số | Hỏi điều gì |
|---|---|
| **Acceptance** | code nó viết có được giữ, hay người sửa lại hết? |
| **Success** | task cuối cùng có xong không? (lấy từ Jira, không bịa) |
| **Autonomy** | tự chạy được, hay phải người cứu giữa chừng? |
| **Reliability** | ổn định, hay lỗi tool / lặp vô hạn? |

Rồi tới câu x100. Biết "tốn bao nhiêu token" mới là kế toán. **ROI** ghép *chi phí × kết quả*, trừ code bị vứt, task fail, retry loop, để tách phần lãi khỏi phần đổ sông:

> *"Agent này tốn $8,200, nhưng **38%, hơn $3,000, là lãng phí** vì code bị vứt, task fail, retry. Đó mới là ROI."*

Và vì câu hỏi không dừng ở một agent (*"team A hay team B dùng AI tốt hơn?"*), LEARN đặt **mọi agent và mọi người lên cùng một bảng**. Bằng chứng thay cho tranh cãi.

**Hai cơ chế khiến những con số đó đáng tin**, chỗ LEARN khác một dashboard:

- **Provenance, không con số nào không giải thích được.** Mỗi số lần ngược về đúng dữ liệu thô sinh ra nó; cùng đầu vào luôn cho cùng kết quả, không ngưỡng ẩn. Đây là điều kiện để lãnh đạo *dám* quyết dựa trên nó.
- **Calibration, so với chính tổ chức bạn.** *"Vì sao 80 là tốt mà không phải 75?"* Ngưỡng gõ tay = cảm tính khoác lớp số. LEARN chuẩn theo phân vị trên chính fleet của bạn: *"agent này ở **phân vị 85**, hơn 85% lượt làm trong tổ chức, kể cả của người."* Fleet đổi, ngưỡng tự dịch.

**Nhưng đo lường mới là bước một.** Giá trị thật nằm ở ba bước xa hơn, và đây là chỗ LEARN mở rộng khỏi "cái thước đo":

- **Từ chấm điểm sang chẩn đoán.** Grade D chưa đủ; cần *"vì sao D và làm gì để lên C"*: thiếu context, prompt mơ hồ ở loại task nào, hay tool nào hay hỏng. Từ **bảng điểm** sang **bản kê đơn**.
- **Từ hồi cố sang tiên lượng.** Không chỉ *"agent này đã làm ra sao"* mà *"task loại này giao agent nào thì xác suất thành công cao nhất"*. Đo lường quay về **dẫn quyết định trước khi giao việc**.
- **Từ đánh giá agent sang tổ chức tự học.** Leaderboard trả lời *"đội nào dùng AI tốt và vì sao"*, rồi *lan cách làm đó ra*. Pattern hay, prompt tốt, context đúng được đóng gói thành tri thức tái dùng. Senior nghỉ, tri thức ở lại.

Đó là chỗ vòng đời khép: cái học được quay về làm tốt hơn vòng sau, cho cả agent lẫn người.

---

## Trụ ② · GOVERN: phân bổ quyền tin cậy, không chỉ chặn cái sai

LEARN cho biết agent tốt hay tệ. Rồi sao? Đây là chỗ Dandori tách khỏi mọi công cụ *chỉ quan sát*. Một dashboard dừng ở "thấy agent tệ", rồi con người tự đọc, tự quyết, tự hành động, ngoài hệ thống, không dấu vết. GOVERN đóng vòng đó.

**Mặt phòng thủ, Guardrail: chặn *trước khi* hại.** Quy tắc vàng: *prompt là gợi ý (agent có thể vi phạm); guardrail là contract (không bypass được)*. Nên guardrail không nằm trong system prompt; nó nằm ở **tầng hạ tầng**, kẹp đúng vào từng hành động:

- Chặn lệnh nguy hiểm: `rm -rf`, đụng `.env`/secret production, xoá nhầm migration.
- Giới hạn phạm vi: agent chỉ động vào thư mục được cấp.
- **Trần ngân sách:** vượt hạn mức → dừng, không đợi cuối tháng mới giật mình.
- Cổng phê duyệt: hành động rủi ro cao (deploy prod, breaking DB change) → dừng, đòi người duyệt *ngay tại thời điểm đó*.

**Mặt kiến tạo, không chỉ *cấm* mà *dẫn*.** Guardrail tốt không chỉ nói "không được"; nó gợi đường đúng: *"lệnh này ngoài phạm vi, dùng thư mục Y thay vì root."* Từ cấm sang huấn luyện tại chỗ. Và mỗi lần "dẫn" lại là một tín hiệu về chỗ agent hay lạc, chảy thẳng về LEARN.

**Chỗ GOVERN thật sự khác biệt:** nó không phải bức tường, nó là **cơ chế phân bổ và thu hồi quyền tin cậy**, giống thăng/giáng chức con người. Câu hỏi thật của lãnh đạo không phải *"chặn cái sai thế nào"* mà *"giao việc khó cho agent nào thì yên tâm"*. GOVERN trả lời bằng cách để **điểm số ở LEARN có hệ quả thật**: agent grade A được nới quyền tự chủ; agent grade D bị siết, chặn khỏi task khó tới khi người duyệt. Chính sách không cố định; nó *phản ứng theo* thành tích. Đây là chiều LEARN chảy ngược vào GOVERN, khép cái vòng lại chặt.

Và mọi nhịp đều để lại bằng chứng:

- **Closed loop:** grade thấp → tự flag → mở review task (gắn Jira) → gán reviewer → ghi quyết định vào audit.
- **Audit trail:** append-only, không sửa được. Sự cố xảy ra → truy ngược cả chuỗi trong vài giây, không đào Slack.

> *"Một agent định xoá thư mục ngoài phạm vi, guardrail chặn ngay, ghi lại. Cùng agent đó tuần này tụt grade D. Dandori tự mở review, gán senior, chặn nó khỏi task khó tới khi người duyệt. Senior xem provenance, quyết 'giám sát 2 tuần', ghi lý do. Sáu tháng sau compliance hỏi, câu trả lời nằm sẵn trong audit."*

**Một nguyên lý ít ai nói tới:** harness không chỉ quản agent. Người duyệt gate, người override, người promote tri thức, họ cũng là actor có thể sai hoặc lạm quyền. Nên GOVERN phải **đối xứng**: mọi thao tác quản trị của con người cũng vào audit, cũng đo được. *Ai canh người canh?* Chính hệ thống, bằng cùng cơ chế nó dùng để canh agent.

---

## Trụ ① · CAPTURE: trí nhớ có cấu trúc của cả tổ chức

LEARN và GOVERN chỉ chạy được nếu có dữ liệu thật. CAPTURE là cái nền đó, đặt cuối vì nó vô hình với lãnh đạo, nhưng thiếu nó thì hai trụ trên chỉ là dashboard rỗng.

Mỗi lần ai chạy AI (Claude Code, Cursor, Codex…), một layer mỏng ngồi sau **tự ghi một bản ghi có cấu trúc**: ai chạy, task gì, token/cost, code đổi ra sao, tool pass/fail, context nào được dùng. Người dùng không phải làm gì thêm.

Nhưng CAPTURE không phải máy đếm tiền. Bản chất sâu hơn: nó **ghi lại *ý định và ngữ cảnh*, không chỉ *con số***.

- **Cost attribution, biết tiền đi đâu.** Mỗi run gắn: agent nào · project nào · task nào · bao nhiêu. Đây là điều kiện trả lời *"hoá đơn tháng này chia theo project ra sao"* và để trần ngân sách ở GOVERN chạy realtime.
- **Ghi "vì sao", không chỉ "cái gì".** Không chỉ *"agent sửa file X tốn $2"* mà cả *quyết định*: prompt nào, context nào, đã bị chặn ở đâu. Nhờ đó LEARN chẩn đoán được *tại sao* agent tệ (thiếu context hay prompt mơ hồ) chứ không chỉ chấm điểm; và audit ở GOVERN có cả ý định, không chỉ hành vi.
- **Multi-layer context, tri thức chảy đúng chiều.** Context phân tầng có chủ rõ ràng (Company → Project → Team → Agent → Task): policy chảy *từ trên xuống*, skill hay chảy *từ dưới lên*. Cái `CLAUDE.md` của một senior không còn nằm trên laptop; nó thành một tầng context có chủ, *ở lại* khi người đi.
- **Structured data, nền của mọi phân tích.** CAPTURE kéo cả **việc do người làm** (Jira, GitHub) lẫn **agent runs** vào *cùng một schema*. Đây là điều kiện để LEARN so người vs agent trên cùng thước đo; và vì mọi nhận định lần ngược được tới đúng dòng dữ liệu thô ở đây, cả vòng đều có provenance.

Gộp lại, CAPTURE là **trí nhớ liên-phiên của tổ chức**: mỗi run là một hạt tri thức *"task loại này, agent này, context này → kết quả này"*. Đó là chỗ tri thức thôi nằm trên laptop cá nhân và trở thành tài sản chung.

---

## Giá trị thật của Dandori

Dandori **không đổi cách engineer dùng AI**. Nó đổi cách **lãnh đạo nhìn AI**: từ cảm tính sang dữ liệu.

| Trụ | Câu hỏi lãnh đạo | Trước Dandori | Với Dandori |
|---|---|---|---|
| **③ LEARN** | "Agent giỏi không, vì sao tệ, sắp tới giao ai?" | cảm tính | Grade calibrate theo fleet · chẩn đoán vì sao · tiên lượng giao việc |
| **③ LEARN** | "x100 có thật? Bao nhiêu là phí?" | tranh cãi | So người vs agent cùng thước đo; ROI tách lãi khỏi phần đổ sông |
| **② GOVERN** | "Giao việc khó cho agent nào thì yên tâm?" | cầu may | Phân bổ quyền tin cậy theo grade; chặn cái sai là mặt phòng thủ |
| **② GOVERN** | "Thấy agent tệ rồi **làm gì**?" | thủ công, ngoài hệ thống | Closed loop: tự flag → route → quyết → audit |
| **① CAPTURE** | "Tiền AI đi đâu? Vì sao agent quyết vậy?" | khó trả lời | Cost theo project; ghi cả ý định; provenance lần về raw |

Ba trụ là **một vòng kín hai chiều**: CAPTURE thu tín hiệu thật (cả ý định), LEARN đọc ra ý nghĩa và tiên lượng, GOVERN phân bổ tin cậy và phản xạ, rồi tri thức quay lại nâng cả agent lẫn con người ở vòng sau.

> **Một câu:** outer harness không phải cái kính lúp soi agent; nó là **hệ thần kinh** của đội AI: cảm nhận (CAPTURE), phán đoán (LEARN), phản xạ (GOVERN), và *nhớ* để lần sau tốt hơn.

---

## Ranh giới: trung thực về giới hạn

Một lớp quản trị đáng tin phải biết chỗ nó *không* làm được:

- **Harness không cứu được model tệ.** Nó tối ưu độ tin cậy, không thay năng lực suy luận.
- **Harness không thay phán đoán con người** ở quyết định mơ hồ; nó đưa bằng chứng để người quyết tốt hơn, không quyết thay.
- **Đo quá tay sinh Goodhart.** Khi một chỉ số thành mục tiêu, agent (và người) sẽ tối ưu chỉ số thay vì kết quả thật. Vì thế LEARN chấm *pattern và kết quả*, calibrate theo fleet, và không bao giờ công khai xếp hạng con người, để cái thước không tự phá chính nó.

Nêu ranh giới không làm luận điểm yếu đi. Nó làm luận điểm *đáng tin*, vì thứ hứa hẹn toàn phần là thứ không ai dám giao quyền cho.

---

## Câu hỏi cũ, câu hỏi mới

Câu hỏi cũ, ai cũng hỏi:

> *"AI có thay được developer không?"*

Hỏi nhầm chỗ. Khi tập đoàn đã đặt cược x100, thứ cần hỏi không còn là "AI có giỏi không" mà là **tổ chức có quản trị được nó không**, gói trong đúng ba vế:

> *Công ty có dám **đánh giá** agent như đánh giá người (**LEARN**), có dám **kiểm soát** nó như kiểm soát một quy trình (**GOVERN**), và có **truy được** mọi việc nó đã làm không (**CAPTURE**)?*

Trả lời được ba vế đó thì cái x100 mới **đo được, kiểm soát được, truy được**. Mỗi ngày không có lớp này, tổ chức tích thêm một khoản **nợ vô hình**: chi phí không ai chia được, quyết định không ai truy được, tri thức lặng lẽ đi theo người nghỉ. Khoảng cách giữa công ty *dùng* AI hiệu quả và công ty chỉ *có* AI nằm đúng ở đó: biến thói quen cá nhân thành hạ tầng dùng chung.

Đó là harness Dandori sinh ra để dựng.

---

## Đọc tiếp

- **Kịch bản pitch:** tầm nhìn ba trụ rút gọn thành bản nói: [04-pitch.md](../../dandori-docs/docs/04-pitch.md)
