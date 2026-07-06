# Dandori là công cụ data-driven: từ dữ liệu thô đến quyết định

> Tài liệu cho lãnh đạo. Trả lời một câu hỏi: khi cả tổ chức vừa dùng AI vừa dùng người, làm sao biết chính xác đang thu được gì, và ra quyết định dựa trên số liệu thay vì cảm tính.
> Mọi con số và công thức trong tài liệu này đều là thứ Dandori đang chạy thật, không phải tính năng hứa hẹn.

---

## Vấn đề: bạn không thể quản thứ bạn không đo

Một tổ chức hiện đại có hai loại người làm việc: con người và agent AI. Con người để lại dấu vết ở Jira, ở GitHub, ở các cuộc họp. Agent để lại dấu vết ở đâu đó trong log, trong hoá đơn token cuối tháng, trong những dòng code được đẩy lên mà không ai chắc đã review kỹ.

Vấn đề là hai dòng dấu vết này nằm ở hai thế giới tách biệt, và không dòng nào tự gộp lại thành một bức tranh. Kết quả là những câu hỏi tưởng đơn giản của lãnh đạo lại không có câu trả lời bằng số:

- Tháng này chi bao nhiêu cho AI, và bao nhiêu trong số đó thực sự tạo ra giá trị?
- Đội nào, agent nào đang làm tốt, và dựa vào bằng chứng gì để nói vậy?
- So với con người, AI đang nhanh hơn hay chỉ đang tốn tiền hơn?

Dandori tồn tại để biến những câu hỏi đó từ "cảm giác" thành "con số có nguồn gốc". Nền của toàn bộ khả năng đó là **thu thập dữ liệu đa dạng, rồi hợp nhất về một cấu trúc chung**.

---

## Phần 1: Thu thập dữ liệu đa dạng, tự động, không làm phiền ai

Dandori thu thập dữ liệu từ ba luồng, và điểm mấu chốt là cả ba đều chảy về **cùng một kho có cấu trúc**.

### Luồng 1: Mọi lần AI chạy đều được ghi lại

Mỗi khi một agent AI chạy, một lớp mỏng ngồi phía sau tự ghi lại một bản ghi đầy đủ, mà người dùng không phải làm gì thêm. Mỗi bản ghi gắn với:

- **Ai chạy** (agent nào), **thuộc dự án nào**, **cho công việc nào** (task key).
- **Model nào**, **tốn bao nhiêu token** (chia rõ input, output, và cả token cache), và quy ra **chi phí thực tế bằng đô-la**.
- **Kết quả ra sao**: hoàn thành, thất bại, hay bị dừng giữa chừng.

Dandori bắt được cả những phiên chạy "lọt lưới" (agent chạy ngoài quy trình chuẩn) bằng cách quét lại lịch sử phiên làm việc. Nói cách khác, không có phiên AI nào là vô hình.

### Luồng 2: Công việc của con người, kéo về từ Jira và GitHub

Song song, Dandori kết nối trực tiếp với các hệ thống mà con người vẫn dùng hàng ngày:

- **Jira**: kéo về các đầu việc, trạng thái, người được giao.
- **GitHub**: kéo về các pull request, trạng thái review.

Đây là dấu vết công việc thật của con người, không phải ước lượng.

### Luồng 3: Ngữ cảnh và quyết định, không chỉ con số

Dandori không chỉ ghi "cái gì đã xảy ra" mà cả "vì sao". Với mỗi phiên, nó lưu lại ngữ cảnh đã được dùng và các điểm quyết định (chỗ nào bị chặn, chỗ nào cần người duyệt). Điều này về sau cho phép trả lời không chỉ "agent này tệ" mà "agent này tệ *vì sao*".

### Điểm mấu chốt: một cấu trúc chung cho cả người và AI

Đây là chỗ Dandori khác một dashboard thông thường. Việc do **con người** làm (từ Jira, GitHub) và việc do **agent** làm được đưa vào **cùng một bảng dữ liệu**, phân biệt nhau bằng đúng một dấu hiệu: đây là việc của người hay của máy.

Hệ quả trực tiếp: lần đầu tiên, con người và AI có thể được đặt cạnh nhau trên **cùng một thước đo**, thay vì so sánh táo với cam.

---

## Phần 2: Từ dữ liệu đó, tính toán và phân tích được gì

Khi dữ liệu đã hợp nhất, Dandori tính ra bốn nhóm kết quả mà lãnh đạo cần. Mỗi con số đều lần ngược về được đúng dữ liệu thô sinh ra nó.

### 2.1. Chi phí đi về đâu, và bao nhiêu là lãng phí

Vì mỗi lần chạy đều gắn với agent, dự án, và công việc, Dandori chia được hoá đơn AI theo **bất kỳ chiều nào**: theo dự án, theo agent, theo model. Câu hỏi "tháng này AI tốn bao nhiêu, chia theo dự án ra sao" có câu trả lời tức thì.

Nhưng biết tổng chi phí mới chỉ là kế toán. Giá trị thật nằm ở chỗ Dandori tách được **phần lãng phí** ra khỏi phần tạo giá trị:

- Phần chi cho các phiên **thất bại hoặc bị dừng** được tính là 100% lãng phí.
- Phần chi cho các phiên **bị đánh dấu có vấn đề** cũng vậy.
- Phần chi cho các phiên sạch được điều chỉnh theo **tỉ lệ code thực sự được giữ lại** (nếu code bị người sửa lại hết thì phần đó cũng là lãng phí).

Kết quả là một câu trả lời sắc như dao, thay cho một con số hoá đơn trần trụi:

> *"Agent này tốn $8,200, nhưng 38% (hơn $3,000) là lãng phí vì code bị vứt, task fail, và retry. Phần tạo giá trị thật chỉ là $5,000."*

### 2.2. Chấm điểm mỗi agent như chấm một nhân sự

Dandori chấm mỗi agent bằng bốn chỉ số, đúng bốn câu mà tổ chức nào cũng hỏi về một nhân viên:

| Chỉ số | Trả lời câu hỏi |
|---|---|
| **Acceptance** | Code nó viết có được giữ lại, hay bị người sửa lại hết? |
| **Success** | Công việc cuối cùng có hoàn thành không? (lấy từ Jira thật, không bịa) |
| **Autonomy** | Nó tự chạy được, hay phải người cứu giữa chừng? |
| **Reliability** | Nó ổn định, hay hay hỏng và lặp vô ích? |

Bốn chỉ số này gộp thành một điểm tổng, rồi quy ra hạng **A đến F**, kèm một bản nhận xét bằng tiếng người do AI viết ra. Điều quan trọng là hạng không dựa vào một ngưỡng gõ tay cảm tính, mà **calibrate theo chính đội của bạn**: một agent hạng A nghĩa là nó tốt hơn phần lớn công việc đang diễn ra trong tổ chức, kể cả công việc của con người. Đội thay đổi thì thước đo tự dịch theo.

### 2.3. Đặt người và AI lên cùng một bảng xếp hạng

Vì việc của người và việc của agent nằm chung một cấu trúc, Dandori dựng được một bảng xếp hạng đặt **mọi agent và mọi người lên cùng một thước đo**. Câu hỏi "AI đang nhanh hơn người hay chỉ tốn tiền hơn", "đội A hay đội B dùng AI hiệu quả hơn" được trả lời bằng bằng chứng, thay cho tranh cãi. Việc so sánh con người là ẩn danh và có thể tắt, để công cụ đo lường không tự biến thành công cụ gây áp lực.

### 2.4. Phát hiện bất thường và dẫn quyết định

Trên nền dữ liệu đó, Dandori còn:

- **Phát hiện chi phí tăng đột biến** ở cấp từng phiên chạy.
- **Ước lượng tác động trước khi duyệt**: khi một hành động cần người phê duyệt, Dandori hiện luôn "hành động tương tự trước đây trung bình tốn bao nhiêu, đụng bao nhiêu file", để người duyệt quyết định có cơ sở.
- **Gợi ý giao việc**: dựa trên lịch sử, đề xuất giao loại việc này cho agent nào thì xác suất thành công cao nhất.

---

## Phần 3: Vì sao những con số này đáng để lãnh đạo tin

Một dashboard đẹp mà số liệu không đáng tin thì còn nguy hiểm hơn không có gì. Dandori được xây quanh hai nguyên tắc để mọi con số chịu được chất vấn.

- **Truy nguồn được (provenance).** Mỗi con số lần ngược về được đúng dữ liệu thô đã sinh ra nó. Cùng một đầu vào luôn cho cùng một kết quả, không có ngưỡng ẩn. Khi ai đó hỏi "con số này ở đâu ra", luôn có câu trả lời cụ thể.
- **Ghi chép không sửa được (audit).** Mọi quyết định quản trị được ghi vào một sổ chỉ-ghi-thêm, chống can thiệp. Sáu tháng sau khi có sự cố hay khi bộ phận tuân thủ hỏi tới, câu trả lời nằm sẵn ở đó, truy được trong vài giây thay vì đào lại lịch sử trò chuyện.

---

## Bằng chứng: những con số này là thật

Toàn bộ khả năng trên đã được kiểm chứng end-to-end trên dữ liệu thật, không phải môi trường trình diễn. Trong một lần chạy thử trên chính máy phát triển:

- Dandori thu về **36 phiên làm việc AI thật** từ **21 dự án** khác nhau, với tổng chi phí thật khoảng **$6,200**.
- Đồng thời kéo về **24 đầu việc từ Jira** và **2 pull request từ GitHub**, hợp nhất vào cùng cấu trúc với các phiên AI.
- Từ đó tự động dựng bảng xếp hạng có hạng A đến F cho toàn bộ đội, sinh nhận xét bằng tiếng người cho agent tốn kém nhất, và tách được phần chi phí tạo giá trị khỏi phần lãng phí, cho từng dự án.

Nói cách khác, mọi thứ trong tài liệu này không phải là điều Dandori *sẽ* làm được, mà là điều nó *đã* làm.

---

## Tóm lại một trang

Dandori biến việc quản trị đội ngũ vừa-người-vừa-AI từ cảm tính thành data-driven, qua ba bước:

1. **Thu thập đa dạng và tự động**: mọi phiên AI (chi phí, kết quả, ngữ cảnh) cộng với công việc thật của con người từ Jira và GitHub, tất cả về một cấu trúc chung.
2. **Tính toán ra thứ lãnh đạo cần**: chi phí chia theo mọi chiều và tách rõ lãng phí; điểm số A đến F cho từng agent; một bảng xếp hạng chung cho cả người và AI; cảnh báo bất thường và gợi ý giao việc.
3. **Đủ đáng tin để dám quyết**: mọi con số truy được về nguồn, mọi quyết định vào sổ không sửa được.

Khoảng cách giữa một tổ chức *dùng* AI hiệu quả và một tổ chức chỉ *có* AI nằm đúng ở chỗ này: có biến được dữ liệu rải rác thành một bức tranh chung để ra quyết định hay không.
