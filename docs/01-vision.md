# Dandori — Harness quản trị cho đội AI của bạn

> Tầm nhìn sản phẩm — bản đọc, không phải slide.
> Cho lãnh đạo kỹ thuật, quản lý sản phẩm, và bất kỳ ai nhìn hoá đơn AI cuối tháng mà không rõ tiền đi đâu. Không cần biết code vẫn đọc được.

---

## Một câu chưa ai trả lời được

Tập đoàn đặt cược lớn vào AI. Mục tiêu **x100** — kỳ vọng AI nhân năng suất cả công ty lên gấp trăm lần. Tiền đổ vào token, đội nào cũng được trang bị agent, code AI đẩy thẳng lên repo.

Rồi tới cuộc họp quý. Lãnh đạo hỏi một câu tưởng đơn giản:

> *AI thật sự đóng góp bao nhiêu vào năng suất chung?*

Im lặng. Đặt cược trăm lần mà không có lấy một thước đo.

Không phải vì AI kém — AI làm việc tốt. Không phải vì đội kém. Câu hỏi rơi vào khoảng không vì thứ để trả lời nó **chưa được xây**: một lớp quản trị bọc quanh agent. Ngành đang gọi lớp đó là **harness**, và 2026 là năm nó trở thành trọng tâm đầu tư kỹ thuật — sau *prompt engineering*, sau *context engineering*, giờ là *harness engineering*.

---

## Agent = Model + Harness

Cả ngành đang chốt lại quanh một công thức: **`Agent = Model + Harness`**. Model lo suy luận. Mọi thứ *không phải* model — context, tool, guardrail, sensor, hook, sandbox, orchestration — là **harness**, và chính harness quyết định agent có đáng tin trong production hay không.

Bằng chứng cứng: đội LangChain đưa coding agent của họ **từ hạng 30 lên hạng 5** trên benchmark ngành mà *không đổi model* — chỉ tối ưu harness. Chiều ngược lại cũng đúng: phần lớn sự cố AI trong doanh nghiệp không đến từ model yếu mà từ **lỗi harness** — context trôi, schema lệch, state phân rã. Reliability nằm ở harness, không nằm ở model.

Bạn đã chạm vào harness mỗi ngày mà không gọi tên: cái `CLAUDE.md` viết lúc 11h đêm, đoạn prompt review copy lần thứ N, script tự lint sau mỗi phiên, sandbox mà provider dựng sẵn. Vấn đề là chúng nằm rải rác trên laptop từng người — chưa thành một lớp.

### Có hai loại harness, và chúng khác nguồn gốc

Ngành phân biệt rõ hai nửa, vì nếu gộp chung sẽ xây nhầm chỗ:

- **Harness dựng sẵn (provider làm)** cho **độ tin cậy phổ quát** — tool loop, sandbox, permission cơ bản. Anthropic, OpenAI, Cursor đã đốt hàng chục triệu đô tối ưu. Bạn không cần xây lại.
- **Harness tuỳ biến (công ty làm)** cho **trách nhiệm tổ chức** — compliance, audit, chuẩn chất lượng, quy tắc domain, cách chia sẻ tri thức. **Không nhà cung cấp nào xây giúp** — họ không biết công ty bạn vận hành ra sao.

Cả hai đều cần, không cái nào thay cái nào. Nghịch lý là phần lớn công ty đang chi rất nhiều cho nửa đầu (token fee) trong khi nửa sau — cái quyết định chất lượng đầu ra và trả lời câu hỏi x100 — vẫn nằm trên laptop cá nhân. Quy mô nhỏ thì ổn. Quy mô lớn thì vỡ.

**Dandori là nửa sau đó** — harness tuỳ biến ở cấp tổ chức. Nó không viết code, không thay Claude Code hay Cursor. Nó bọc bên ngoài, biến một đám agent chạy lẻ thành **một đội ngũ được quản trị**.

---

## Harness đó gồm gì

Đồng thuận 2026 mô tả một harness production đủ chuẩn gồm năm tầng: *tool orchestration · verification loops · context & memory · guardrails · observability*. Ba tầng đầu là chỗ agent **chạy** — nửa dựng sẵn lo phần lớn. Hai tầng cuối — **guardrails và observability** — là chỗ **tổ chức chịu trách nhiệm**, và là chỗ hầu hết công ty đang trống.

Dandori dựng đúng phần trống đó, gói thành **một vòng đời** — chính cái vòng tổ chức 100 năm nay vẫn dùng để quản con người: *nhìn kết quả để đánh giá → quyết định & hành động → ghi lại mọi việc → lặp lại, tốt hơn*. Ba trụ:

```
   ③ LEARN          ──▶   ② GOVERN        ──▶   ① CAPTURE
   đánh giá agent          chặn cái sai,          mọi run đẻ ra
   như đánh giá người      quyết & ghi lại        dữ liệu có cấu trúc
        ▲                                              │
        └──────── tri thức quay lại, vòng sau tốt hơn ─┘
```

Đọc theo thứ tự lãnh đạo chạm tới: **LEARN** (thứ nhìn thấy trước — agent giỏi hay tệ), **GOVERN** (làm gì với điều đó), **CAPTURE** (cái nền dữ liệu đỡ cả hai). Một điểm xuyên suốt: *đo lường và audit không phải một trụ riêng* — CAPTURE ghi, GOVERN audit, LEARN phân tích. Dữ liệu là mạch máu chảy qua cả vòng.

---

## Trụ ③ — LEARN: đánh giá agent như đánh giá một con người

Tuyển một engineer, vài tháng sau có **performance review**: giỏi lên không, giao việc khó được chưa, chỗ nào cần kèm. Agent thì không — chỉ có chat log và cảm giác lờ mờ. Đây là khoảng trống đầu tiên và cũng là thứ lãnh đạo thấy trước nhất.

LEARN cho mỗi agent một **bản đánh giá khách quan**: grade A–F, vài câu nhận xét tiếng người, xu hướng lên hay xuống, xếp hạng so với cả fleet. Bốn chỉ số — đúng bốn câu tổ chức nào cũng hỏi về một nhân sự, không tự chế:

| Chỉ số | Hỏi điều gì |
|---|---|
| **Acceptance** | code nó viết có được giữ, hay người sửa lại hết? |
| **Success** | task cuối cùng có xong không? (lấy từ Jira, không bịa) |
| **Autonomy** | tự chạy được, hay phải người cứu giữa chừng? |
| **Reliability** | ổn định, hay lỗi tool / lặp vô hạn? |

Rồi tới câu x100 ở đầu bài. Biết "tốn bao nhiêu token" mới là kế toán. **ROI** là ghép *chi phí × kết quả* — trừ đi code bị vứt, task fail, retry loop — để tách phần có lãi khỏi phần đổ sông:

> *"Agent này tốn $8,200, nhưng **38% — hơn $3,000 — là lãng phí** vì code bị vứt, task fail, retry. Đó mới là ROI: phần x100 nào đang lãi, phần nào đang đốt tiền."*

Và vì câu hỏi lãnh đạo không dừng ở một agent — *"team A hay team B dùng AI tốt hơn?"* — LEARN đặt **mọi agent và mọi người lên cùng một bảng**: leaderboard toàn fleet, phân bố grade, đội lên đội xuống. Bằng chứng thay cho tranh cãi.

**Hai cơ chế khiến những con số đó đáng tin** — đây mới là chỗ LEARN khác một dashboard thường:

- **Provenance — không con số nào không giải thích được.** Mỗi con số lần ngược về đúng dữ liệu thô sinh ra nó; cùng đầu vào luôn cho cùng kết quả, không ngưỡng ẩn. Đây là điều kiện để lãnh đạo *dám* quyết dựa trên nó.
- **Calibration — so với chính tổ chức bạn.** *"Vì sao 80 là tốt mà không phải 75?"* Ngưỡng gõ tay = cảm tính khoác lớp số. LEARN chuẩn theo phân vị trên chính fleet của bạn: *"agent này ở **phân vị 85** — hơn 85% lượt làm trong tổ chức, kể cả của người."* Fleet đổi, ngưỡng tự dịch. Không magic number.

Cuối cùng, cái LEARN rút ra được **đóng gói lại** — pattern tốt, prompt hay, context đúng thành tri thức tái dùng. Senior nghỉ, tri thức ở lại. Đó là chỗ vòng đời khép: cái học được quay về làm tốt hơn vòng sau.

---

## Trụ ② — GOVERN: đánh giá rồi *làm gì* với nó

LEARN cho biết agent tốt hay tệ. Rồi sao? Đây là chỗ Dandori tách khỏi mọi công cụ *chỉ quan sát*. Một dashboard dừng ở "thấy agent tệ" — rồi con người phải tự đọc, tự quyết, tự hành động, ngoài hệ thống, không dấu vết. GOVERN đóng vòng đó theo **hai nhịp thời gian**:

```
   TRƯỚC / TRONG khi agent chạy          SAU đó — theo chu kỳ
   ─────────────────────────────         ──────────────────────────
   GUARDRAIL (realtime, phòng ngừa)      CLOSED LOOP (hậu kiểm)
   chặn ngay tại từng tool-call          flag → review → gate → audit
```

**Nhịp 1 — Guardrail: chặn *trước khi* hại.** Quy tắc vàng: *prompt là gợi ý (agent có thể vi phạm); guardrail là contract (không bypass được)*. Nên guardrail không nằm trong system prompt — nó nằm ở **tầng hạ tầng**, kẹp đúng vào tool-call:

- Chặn lệnh nguy hiểm trước khi chạy — `rm -rf`, đụng `.env`/secret production, xoá nhầm migration.
- Giới hạn scope / sandbox — agent chỉ động vào thư mục được cấp, không với tới root hay repo khác.
- **Trần ngân sách** — vượt hạn mức token/đô → dừng, không đợi cuối tháng mới giật mình.
- Permission gate — hành động rủi ro cao (deploy prod, breaking DB change) → dừng, đòi human duyệt *ngay tại thời điểm đó*.

Mỗi lần guardrail kích hoạt đều **đẻ ra một sự kiện** CAPTURE ghi lại; agent hay bị chặn → tín hiệu reliability thấp, chảy thẳng về grade ở LEARN. Vòng tự khép.

**Nhịp 2 — Closed loop & gate: hành động *sau khi* đánh giá.**

- **Quality gate** — pipeline **độc lập với agent** chấm điểm và chặn run dưới ngưỡng: coverage, lint, security scan. *Separation of duties*: người viết code không thể là người duy nhất chấm code đó. "Chuẩn chất lượng của công ty" trở thành một cổng máy chặn được, không phải một dòng dặn trong prompt.
- **Closed loop** — grade thấp → tự flag → mở review task (gắn Jira) → gán reviewer → ghi quyết định vào audit.
- **Audit trail** — append-only, hash-chain để không sửa được. Sự cố xảy ra → truy ngược cả chuỗi trong vài giây, không đào Slack.

> *"Một agent định chạy lệnh xoá thư mục ngoài scope — guardrail chặn ngay, ghi lại. Cùng agent đó tuần này tụt grade D. Dandori tự mở review task, gán senior, chặn nó khỏi task khó tới khi human duyệt. Senior xem provenance, quyết 'oversight 2 tuần', ghi lý do. Sáu tháng sau compliance hỏi — câu trả lời nằm sẵn trong audit."*

Đây là lý do Dandori là *một lớp riêng*, không phải một dashboard: phòng ngừa chặn từ đầu, hậu kiểm bắt cái lọt qua — và cả hai đều lưu bằng chứng.

---

## Trụ ① — CAPTURE: cái nền đỡ tất cả

LEARN và GOVERN chỉ chạy được nếu có dữ liệu thật. CAPTURE là cái nền đó — đặt cuối vì nó vô hình với lãnh đạo, nhưng thiếu nó thì hai trụ trên chỉ là dashboard rỗng.

Mỗi lần ai chạy AI (Claude Code, Cursor, Codex…), một layer mỏng ngồi sau **tự ghi một bản ghi có cấu trúc** — ai chạy, task gì, token/cost, code đổi ra sao, tool pass/fail, context nào được dùng. Người dùng không phải làm gì thêm. CAPTURE làm ba việc:

- **Cost attribution — biết tiền đi đâu.** Mỗi run gắn metadata: agent nào · project nào · task nào · bao nhiêu token/đô. Đây là điều kiện để trả lời *"hoá đơn tháng này chia theo project ra sao"* và để trần ngân sách ở GOVERN hoạt động realtime.
- **Multi-layer context — tri thức chảy đúng chiều.** Context phân tầng có chủ rõ ràng (Company → Project → Team → Agent → Task): policy chảy *từ trên xuống*, skill hay chảy *từ dưới lên*. Cái `CLAUDE.md` của một senior không còn nằm trên laptop — nó thành một tầng context có chủ, tái dùng được, *ở lại* khi người đi.
- **Structured data — nền của mọi phân tích.** CAPTURE kéo cả **việc do người làm** (Jira, GitHub) lẫn **agent runs** vào *cùng một schema*. Đây là điều kiện để LEARN so người vs agent trên cùng thước đo — và vì mọi nhận định lần ngược được tới đúng dòng dữ liệu thô ở đây, cả vòng đều có provenance.

---

## Giá trị thật của Dandori

Dandori **không đổi cách engineer dùng AI**. Nó đổi cách **lãnh đạo nhìn AI** — từ cảm tính sang dữ liệu. Đối chiếu lại theo ba trụ:

| Trụ | Câu hỏi lãnh đạo | Trước Dandori | Với Dandori |
|---|---|---|---|
| **③ LEARN** | "Agent giỏi không, đang lên không?" | cảm tính | Grade A–F calibrate theo fleet, review tiếng người, trend |
| **③ LEARN** | "x100 có thật? Bao nhiêu là phí?" | tranh cãi | So người vs agent cùng thước đo; ROI tách phần lãi khỏi phần đổ sông |
| **② GOVERN** | "Sao agent **không** làm việc nguy hiểm?" | cầu may / dặn trong prompt | Guardrail tầng hạ tầng, chặn trước, không bypass được |
| **② GOVERN** | "Thấy agent tệ rồi **làm gì**?" | thủ công, ngoài hệ thống | Closed loop: tự flag → route → quyết → audit |
| **① CAPTURE** | "Tiền AI đi đâu? Liệt kê AI change Q3?" | khó trả lời | Cost attribution theo project; provenance lần về raw; audit không sửa được |

Ba trụ không phải ba tính năng rời — chúng là **một vòng kín**: CAPTURE thu tín hiệu thật, LEARN đọc ra ý nghĩa (kể cả ROI của x100), GOVERN phản xạ (chặn / duyệt / flag), rồi tri thức quay lại làm vòng sau tốt hơn.

> **Một câu:** outer harness không phải cái kính lúp soi agent — nó là **hệ thần kinh** của đội AI. Đó là thứ biến AI trong tổ chức từ *một đám agent chạy lẻ, không ai đo được* thành *một đội ngũ được quản trị, x100 đo được và kiểm soát được*.

---

## Câu hỏi cũ, câu hỏi mới

Câu hỏi cũ, ai cũng hỏi:

> *"AI có thay được developer không?"*

Câu này hỏi nhầm chỗ. Khi tập đoàn đã đặt cược x100, thứ cần hỏi không còn là "AI có giỏi không" mà là **tổ chức có quản trị được nó không** — gói trong đúng ba vế:

> *Công ty có dám **đánh giá** agent như đánh giá người (**LEARN**), có dám **kiểm soát** nó như kiểm soát một quy trình (**GOVERN**), và có **truy được** mọi việc nó đã làm không (**CAPTURE**)?*

Trả lời được ba vế đó thì cái x100 mới **đo được, kiểm soát được, truy được**. Mỗi ngày không có lớp này, tổ chức lại tích thêm một khoản **nợ vô hình**: chi phí không ai chia được, quyết định không ai truy được, tri thức lặng lẽ đi theo người nghỉ. Khoảng cách giữa công ty *dùng* AI hiệu quả và công ty chỉ *có* AI nằm đúng ở đó — biến thói quen cá nhân thành hạ tầng dùng chung.

Đó là harness Dandori sinh ra để dựng.

---

## Đọc tiếp

- **Kịch bản pitch** — tầm nhìn ba trụ này rút gọn thành bản nói trên sân khấu: [04-pitch.md](../../dandori-docs/docs/04-pitch.md)
