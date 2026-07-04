# Outer Harness: lý thuyết tiền đề cho quản trị đội AI

> Một luận điểm khái niệm, độc lập với bất kỳ công cụ nào.
> Nó trả lời câu hỏi: khi một tổ chức giao việc thật cho nhiều AI agent, cần một lớp gì bọc quanh chúng để việc đó đáng tin?

---

## Vấn đề: năng lực đã có, quản trị thì chưa

Một tổ chức có thể trang bị AI cho mọi đội, đổ tiền vào token, và để agent đẩy kết quả thẳng vào quy trình thật. Đến lúc này, năng lực của model không còn là nút thắt nữa. Nút thắt đã chuyển sang một câu hỏi khác:

> *Cả tổ chức đang thu được gì từ AI, và có kiểm soát được nó không?*

Câu hỏi này thường rơi vào khoảng không. Không phải vì model kém, cũng không phải vì đội kém, mà vì thứ để trả lời nó thì **chưa được xây**: một lớp quản trị bọc quanh agent. Ngành đang gọi lớp đó là **harness**. Sau *prompt engineering*, rồi *context engineering*, trọng tâm kế tiếp chính là *harness engineering*.

Điểm khởi đầu của luận điểm này là một sự phân biệt trung thực: đo cho đúng quan trọng hơn là hứa cho lớn. Một lớp quản trị tốt không hứa một con số năng suất khổng lồ; thay vào đó, nó cho biết **con số thật là bao nhiêu**. Bởi vì biết một con số khiêm tốn mà kiểm soát được vẫn còn hơn là tin vào một con số lớn trong mù mờ.

---

## Agent = Model + Harness

Một agent vận hành được thì ghép từ hai phần. **Model** lo phần suy luận. Còn mọi thứ *không phải* model (context, guardrail, sensor, sandbox, orchestration) đều là **harness**. Và chính harness, chứ không phải riêng model, mới quyết định một agent có đáng tin trong môi trường thật hay không.

Có hai quan sát củng cố cho điều này:

- Với cùng một model, chất lượng của agent có thể dịch chuyển rất xa chỉ nhờ tối ưu harness. Năng lực suy luận thì không đổi, nhưng độ tin cậy thì có.
- Phần lớn sự cố của AI trong môi trường thật không đến từ một model yếu, mà đến từ **lỗi harness**: context trôi, schema lệch, trạng thái phân rã. Nói cách khác, reliability là một thuộc tính của harness.

Thực ra harness không phải là khái niệm mới với người làm việc cùng AI. Nó chính là cái tài liệu hướng dẫn viết vội lúc nửa đêm, là đoạn prompt kiểm tra được copy đi copy lại, hay cái script tự động chạy sau mỗi phiên. Vấn đề không phải là thiếu chúng; vấn đề là những mảnh đó đang nằm rải rác trên máy của từng người, và chưa hợp lại thành một lớp.

### Hai loại harness, khác nhau ở nguồn gốc

- **Harness dựng sẵn (nhà cung cấp làm):** gồm vòng lặp tool, sandbox, và permission cơ bản. Đây là phần mà các nhà cung cấp model đã đầu tư rất lớn, nên không tổ chức nào cần phải xây lại.
- **Harness tuỳ biến (tổ chức làm):** gồm compliance, audit, chuẩn chất lượng riêng, quy tắc theo domain, và cách chia sẻ tri thức nội bộ. Đây là phần mà **không nhà cung cấp nào xây giúp được**, đơn giản vì họ không biết một tổ chức cụ thể vận hành ra sao.

Ranh giới giữa hai loại này thì **không cố định**. Các nhà cung cấp vẫn liên tục nuốt dần phần tuỳ-biến vào phần dựng-sẵn. Nhưng có một phần họ **không bao giờ chuẩn hoá được**, bởi vì nó gắn cứng với từng tổ chức: đó là chuẩn chất lượng của riêng tổ chức đó, quy tắc tuân thủ của riêng ngành đó, và tri thức của riêng đội đó. Đó chính là phần tuỳ-biến bền vững, và cũng là chỗ mà lý thuyết này tập trung vào.

### Vì sao không thể tự dựng bằng các mảnh rời

Một script kiểm tra rời, hay một prompt review rời thì ai cũng viết được. Thứ **không** thể tự dựng được không phải là từng mảnh, mà là **sự nối kết giữa chúng thành một vòng khép kín**: một lần chặn được ghi lại → rồi tín hiệu đó chảy vào cách đánh giá agent → một đánh giá thấp tự kích hoạt rà soát → và quyết định cuối cùng đi vào một sổ ghi không sửa được. Từng mảnh thì tầm thường; nhưng *cái vòng* mới là thứ không thể dựng nổi bằng các script rời rạc trên từng máy. Chính vì vậy, lớp quản trị phải là một **lớp** thực sự, chứ không phải chỉ là một bộ tiện ích.

---

## Lớp quản trị gồm gì

Một harness đủ chuẩn cho môi trường thật có thể được mô tả bằng năm tầng: *tool orchestration · verification loops · context & memory · guardrails · observability*. Ba tầng đầu là chỗ agent **chạy**, và ở đây phần dựng sẵn đã lo phần lớn. Nhưng hai tầng cuối, tức là **guardrails và observability**, mới là chỗ **tổ chức phải chịu trách nhiệm**, và đó cũng là chỗ mà hầu hết tổ chức đang bỏ trống.

Chính phần trống đó được gói lại thành **một vòng đời**, và đó cũng là cái vòng mà tổ chức đã dùng hàng trăm năm nay để quản con người: *nhìn kết quả để đánh giá → quyết định và hành động → ghi lại mọi việc → rồi lặp lại, tốt hơn*. Ba module của nó là:

```
   ③ LEARN           ──▶   ② GOVERN          ──▶   ① CAPTURE
   biến mỗi run           phân bổ quyền           trí nhớ có cấu
   thành tri thức          tin cậy                trúc của tổ chức
        ▲                                              │
        └──────── tri thức quay lại, vòng sau tốt hơn ─┘
```

Có một nguyên lý xuyên suốt ở đây: *ba module này không phải là ba tính năng rời rạc*. Mỗi module vừa là **nguồn** lại vừa là **bể chứa** của hai module kia, nên dữ liệu chảy hai chiều qua cả vòng. Chính điều đó khiến nó trở thành một hệ thống, chứ không phải chỉ là ba màn hình số liệu tách biệt.

---

## Module ③ · LEARN: biến mỗi run thành tri thức hành động được

Với một con người, sau vài tháng làm việc thì luôn có một bản đánh giá: đã giỏi lên chưa, đã giao được việc khó chưa, và chỗ nào còn cần kèm. Nhưng với agent thì thường không có gì ngoài log và một cảm giác lờ mờ. Đây là khoảng trống dễ thấy nhất.

Nguyên lý của module này là: mỗi agent đều xứng đáng có một **bản đánh giá khách quan**, được đo bằng chính những câu hỏi mà tổ chức nào cũng hỏi về một nhân sự:

- Kết quả nó tạo ra có được giữ lại, hay bị người khác làm lại từ đầu?
- Công việc cuối cùng có hoàn thành không?
- Nó tự chạy được, hay phải người cứu giữa chừng?
- Nó ổn định, hay hay hỏng và lặp vô ích?

Nhưng đo chi phí mới chỉ là kế toán. Câu hỏi thật sự phải là **hiệu quả trên chi phí**: tức là ghép *chi phí × kết quả*, rồi trừ đi phần bị vứt, phần thất bại, và phần lặp lại vô ích, để tách được phần giá trị thật khỏi phần đổ sông. Và vì câu hỏi không dừng lại ở một agent, nên mọi agent (kể cả con người) đều cần được đặt lên **cùng một thước đo**, để việc so sánh dựa trên bằng chứng thay vì tranh cãi.

**Có hai cơ chế khiến những con số đó trở nên đáng tin**, và đây cũng là chỗ đánh giá khác với một bảng số liệu thuần tuý:

- **Provenance: không con số nào là không giải thích được.** Mỗi con số đều lần ngược về được đúng dữ liệu thô đã sinh ra nó; cùng một đầu vào thì luôn cho cùng một kết quả, và không có ngưỡng ẩn nào. Đây chính là điều kiện để người ra quyết định *dám* dựa vào nó.
- **Calibration: so với chính tổ chức đó.** Một ngưỡng gõ tay thì chỉ là cảm tính khoác lên lớp số. Ngưỡng đúng phải được chuẩn theo phân vị trên chính tập agent của tổ chức: *"agent này đang ở phân vị cao hơn phần lớn công việc trong tổ chức, kể cả của người."* Khi tập thay đổi thì ngưỡng cũng tự dịch theo.

**Nhưng đo lường mới chỉ là bước một.** Giá trị thật sự nằm ở ba bước xa hơn, và đây là chỗ module LEARN mở rộng ra khỏi vai trò của một "cái thước đo":

- **Từ chấm điểm sang chẩn đoán.** Biết một agent yếu thôi là chưa đủ; ta còn cần biết *vì sao nó yếu và làm gì để nó tốt hơn*: là do thiếu context, do hướng dẫn mơ hồ ở một loại việc nào đó, hay do một công cụ nào đó hay hỏng. Đây là bước đi từ một bảng điểm sang một bản kê đơn.
- **Từ hồi cố sang tiên lượng.** Không chỉ trả lời *"agent này đã làm ra sao"*, mà còn trả lời *"loại việc này thì giao cho ai sẽ có xác suất thành công cao nhất"*. Đo lường lúc này quay về dẫn dắt quyết định *ngay trước khi* giao việc.
- **Từ đánh giá cá thể sang tổ chức tự học.** Một khi đã biết đội nào dùng AI hiệu quả và vì sao, thì cách làm tốt đó có thể được đóng gói thành tri thức tái dùng và lan ra rộng hơn. Người giỏi có rời đi, nhưng cách làm thì ở lại.

Và đó chính là chỗ vòng đời khép lại: cái học được sẽ quay về làm tốt hơn cho vòng sau, cho cả agent lẫn con người.

---

## Module ② · GOVERN: phân bổ quyền tin cậy, không chỉ chặn cái sai

LEARN cho ta biết một agent tốt hay tệ. Rồi sao nữa? Đây chính là chỗ mà một lớp *quản trị* tách hẳn khỏi một lớp *chỉ quan sát*. Một công cụ quan sát sẽ dừng lại ở chỗ "thấy agent tệ", rồi con người phải tự đọc, tự quyết, và tự hành động, hoàn toàn ngoài hệ thống và không để lại dấu vết. Còn GOVERN thì đóng cái vòng đó lại.

**Mặt phòng thủ của nó là guardrail: chặn *trước khi* gây hại.** Quy tắc nền tảng ở đây là: *một chỉ dẫn chỉ là gợi ý (nên có thể bị vi phạm); còn một guardrail là ràng buộc (nên không bypass được)*. Vì thế guardrail không thể sống trong lời nhắc của agent; nó phải nằm ở **tầng hạ tầng**, kẹp đúng vào từng hành động ngay trước khi hành động đó xảy ra:

- Chặn các thao tác nguy hiểm: như xoá trên diện rộng, đụng vào bí mật của môi trường thật, hay phá cấu trúc dữ liệu.
- Giới hạn phạm vi: agent chỉ được động vào đúng vùng được cấp.
- Trần ngân sách: vượt hạn mức thì dừng lại, chứ không đợi đến cuối kỳ mới giật mình.
- Cổng phê duyệt: với hành động rủi ro cao thì dừng lại và đòi người duyệt *ngay tại thời điểm đó*.

**Còn mặt kiến tạo của nó là: không chỉ *cấm* mà còn *dẫn*.** Một guardrail tốt không chỉ nói "không được"; nó còn gợi ra đường đúng: *"thao tác này ngoài phạm vi, hãy dùng vùng Y thay vì gốc."* Đây là bước đi từ cấm đoán sang huấn luyện ngay tại chỗ. Và mỗi lần "dẫn" như vậy lại là một tín hiệu cho biết chỗ agent hay lạc, tín hiệu đó chảy thẳng về LEARN.

**Nhưng chỗ GOVERN thật sự khác biệt là:** nó không phải một bức tường, mà là một **cơ chế phân bổ và thu hồi quyền tin cậy**, giống hệt việc thăng và giáng chức một con người. Câu hỏi thật của người lãnh đạo không phải là *"chặn cái sai thế nào"*, mà là *"giao việc khó cho ai thì mới yên tâm"*. GOVERN trả lời câu đó bằng cách để **điểm đánh giá của LEARN có hệ quả thật**: một agent được đánh giá cao thì được nới quyền tự chủ; còn một agent bị đánh giá thấp thì bị siết lại, chặn khỏi việc khó cho tới khi có người duyệt. Chính sách vì thế không hề cố định; nó *phản ứng theo* thành tích. Đây chính là chiều mà LEARN chảy ngược vào GOVERN, khép cái vòng lại cho chặt.

Và mọi nhịp trong đó đều để lại bằng chứng:

- **Closed loop:** một đánh giá thấp → tự đánh dấu → mở việc rà soát → gán người duyệt → rồi ghi quyết định vào sổ audit.
- **Audit trail:** chỉ ghi thêm, không sửa được. Khi có sự cố, ta truy ngược cả chuỗi trong vài giây, chứ không phải đi đào lại các cuộc trò chuyện rải rác.

**Có một nguyên lý ít được nói tới:** một harness không chỉ để quản agent. Người duyệt cổng, người ghi đè quyết định, người phê chuẩn tri thức, tất cả họ cũng là những agent có thể sai hoặc lạm quyền. Vì thế quản trị buộc phải **đối xứng**: mọi thao tác quản trị của con người cũng phải vào audit và cũng phải đo được. Vậy *ai canh người canh?* Chính là hệ thống, bằng cùng một cơ chế mà nó dùng để canh agent.

---

## Module ① · CAPTURE: trí nhớ có cấu trúc của cả tổ chức

LEARN và GOVERN chỉ có thể chạy được nếu có dữ liệu thật. Và CAPTURE chính là cái nền đó. Nó được đặt ở cuối trong phần trình bày vì nó gần như vô hình với người lãnh đạo, nhưng nếu thiếu nó thì hai module ở trên cũng chỉ là những màn hình rỗng.

Nguyên lý của nó là: mỗi lần một agent chạy, sẽ có một lớp mỏng ngồi phía sau để **tự ghi lại một bản ghi có cấu trúc**: gồm ai chạy, việc gì, chi phí bao nhiêu, kết quả đổi ra sao, công cụ nào thành hay bại, và context nào đã được dùng. Người dùng không phải làm gì thêm.

Nhưng CAPTURE không phải là một cái máy đếm tiền. Bản chất của nó sâu hơn thế: nó **ghi lại cả *ý định và ngữ cảnh*, chứ không chỉ *con số***.

- **Quy chi phí về đúng nguồn.** Mỗi run đều gắn với: agent nào, dự án nào, việc nào, và tốn bao nhiêu. Đây là điều kiện để chia được chi phí theo dự án, và để cho trần ngân sách ở GOVERN chạy được theo thời gian thực.
- **Ghi cả "vì sao", chứ không chỉ "cái gì".** Không chỉ là *"agent sửa thứ X tốn Y"*, mà còn là cả *quyết định đằng sau*: đã dùng chỉ dẫn nào, context nào, và bị chặn ở đâu. Nhờ đó mà LEARN mới *chẩn đoán* được vì sao một agent yếu, chứ không chỉ chấm điểm; và audit ở GOVERN mới có được cả ý định, chứ không chỉ hành vi.
- **Context phân tầng, để tri thức chảy đúng chiều.** Context được phân theo tầng và có chủ rõ ràng (từ tổ chức xuống dự án, đội, agent, rồi việc): quy tắc thì chảy *từ trên xuống*, còn kỹ năng hay thì chảy *từ dưới lên*. Nhờ vậy, tri thức của một người giỏi không còn nằm trên máy cá nhân nữa; nó trở thành một tầng context có chủ, và *ở lại* ngay cả khi người đó đi.
- **Dữ liệu có cấu trúc, làm nền cho mọi phân tích.** Ghi nhận kéo cả **việc do người làm** lẫn **việc do agent làm** vào *cùng một schema*. Đây là điều kiện để so sánh người và agent trên cùng một thước đo; và vì mọi nhận định đều lần ngược về được đúng dòng dữ liệu thô ở đây, nên cả vòng đều có provenance.

Gộp lại, ghi nhận chính là **trí nhớ liên-phiên của tổ chức**: mỗi run là một hạt tri thức *"loại việc này, agent này, context này → thì cho ra kết quả này"*. Đó là chỗ mà tri thức thôi nằm trên máy cá nhân và bắt đầu trở thành tài sản chung.

---

## Ranh giới: trung thực về giới hạn

Một lớp quản trị đáng tin thì phải biết rõ chỗ nào nó *không* làm được:

- **Harness không cứu được một model tệ.** Nó chỉ tối ưu độ tin cậy, chứ không thay được năng lực suy luận.
- **Harness không thay được phán đoán của con người** ở những quyết định mơ hồ; nó chỉ đưa ra bằng chứng để con người quyết tốt hơn, chứ không quyết thay.
- **Đo quá tay thì sinh ra Goodhart.** Một khi một chỉ số trở thành mục tiêu, thì cả agent lẫn người sẽ tối ưu chính chỉ số đó thay vì kết quả thật. Vì thế đánh giá phải chấm trên *pattern và kết quả*, phải calibrate theo chính tập của tổ chức, và tuyệt đối không biến việc xếp hạng con người thành một bảng công khai, để cho cái thước không tự phá chính nó.

Việc nêu ra ranh giới không làm cho luận điểm yếu đi. Ngược lại, nó làm luận điểm *đáng tin* hơn, bởi vì một thứ hứa hẹn được toàn phần lại chính là thứ không ai dám giao quyền cho.

---

## Câu hỏi cũ, câu hỏi mới

Câu hỏi cũ, ai cũng hỏi:

> *"AI có thay được con người không?"*

Đó là hỏi nhầm chỗ. Khi một tổ chức đã đặt cược lớn vào AI, thì thứ cần hỏi không còn là "AI có giỏi không" nữa, mà là **tổ chức có quản trị được nó không**, và câu này gói gọn trong đúng ba vế:

> *Tổ chức có dám **đánh giá** một agent như đánh giá một con người, có dám **kiểm soát** nó như kiểm soát một quy trình, và có **truy được** mọi việc nó đã làm hay không?*

Chỉ khi trả lời được cả ba vế đó thì mọi con số về năng suất mới thực sự **đo được, kiểm soát được, và truy được**. Còn mỗi ngày trôi qua mà không có lớp này, tổ chức lại tích thêm một khoản **nợ vô hình**: đó là chi phí không ai chia được, quyết định không ai truy được, và tri thức thì lặng lẽ đi theo người nghỉ. Khoảng cách giữa một tổ chức *dùng* AI hiệu quả và một tổ chức chỉ *có* AI nằm đúng ngay ở đó: ở việc biến được thói quen cá nhân thành hạ tầng dùng chung.

Tóm lại trong một câu: outer harness không phải là cái kính lúp soi agent; nó là **hệ thần kinh** của cả một đội AI, với đủ cảm nhận (CAPTURE), phán đoán (LEARN), phản xạ (GOVERN), và cả khả năng *nhớ* để lần sau làm tốt hơn. Và đó chính là lớp lý thuyết mà một sản phẩm quản trị đội AI cần hiện thực hoá.
