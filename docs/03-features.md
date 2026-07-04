# Dandori — Feature & Use-Case List

> Danh sách tính năng nên có, gom theo ba trụ **LEARN → GOVERN → CAPTURE**.
> Mỗi mục neo vào một **nhu cầu thật** (research 2026, xem cuối) và một **use-case** cụ thể. Gắn nhãn **[MVP]** / **[Sau]** để giữ YAGNI.
> Tham chiếu hai bản cũ: `dandori/` (full platform) và `dandori-cli/` (Go CLI).

---

## Nhu cầu thật — vì sao danh sách này tồn tại

Vài số liệu 2026 định hình toàn bộ scope dưới đây:

- **81%** lãnh đạo doanh nghiệp báo cáo *tăng* sự cố production do code AI sinh ra — dù tự chấm mình **83.6/100** về mức sẵn sàng. Khoảng cách giữa "tưởng kiểm soát được" và "thực tế" chính là chỗ Dandori lấp.
- **88%** pilot agent *không bao giờ lên được production* — thiếu isolation, governance, compliance là rào cản số một.
- Chi phí AI *dễ scale nhưng khó dự báo* — token đội lên khắp CI/CD, test, security scan; phần lớn tổ chức **chưa có cost attribution** trưởng thành.
- Đồng thuận ngành: control phải **runtime-enforced** (chặn *trong lúc* chạy), nằm **ngoài code của agent** để không bypass được — không phải finance dọn dẹp cuối tháng.

Ba trụ ánh xạ thẳng: **CAPTURE** = evidence/attribution, **GOVERN** = runtime control, **LEARN** = productivity measurement. Danh sách feature theo đúng thứ tự đó.

---

## Service liên kết — đội bạn đang dùng gì, Dandori chạm vào đâu

Đội dùng **Jira · Confluence · Slack · Google Workspace** (và push code lên **GitHub**). Dandori không thay cái nào — nó **đọc/ghi** để mỗi service thành một cánh tay của console. Ba nguyên tắc chạm:

- **Đọc → làm giàu CAPTURE:** Jira issue, Confluence doc, GitHub PR, Drive doc trở thành context + tín hiệu.
- **Ghi → đóng vòng GOVERN:** tạo ticket, đổi trạng thái, post report, comment PR, gửi alert — hành động từ Dandori.
- **Thông báo → nuôi thói quen:** Slack + Gmail đẩy đúng việc-cần-người tới đúng nơi người vốn đã ở.

| Service | Đọc | Ghi | Vai chính |
|---|---|---|---|
| **Jira** | issue, sprint, status | tạo ticket, transition, comment | task board — source of truth |
| **Confluence** | doc làm context/knowledge | post run/sprint report | knowledge store |
| **Slack** | — | alert/digest + **duyệt interactive** (approve/reject trong Slack → audit) | mặt tiền thông báo & hành động |
| **GWS** | Drive doc → context | **Sheets** export cost/ROI · **Gmail** digest/escalation · **Calendar** lịch review | nơi PO/finance vốn đã sống |
| **GitHub** | PR, review, revert, merge | comment/approve PR | code + tín hiệu AI-CFR *(coi như đương nhiên)* |

> Slack + Sheets + Gmail là **kênh cho low-tech**: duyệt trong Slack, đọc cost trong Sheets, nhận digest qua mail — không ai phải học một web app mới. Đây là phần nối tiếp trực tiếp luận điểm audience-fit của cả dự án.

---

## Trụ ① CAPTURE — thu mọi thứ thành dữ liệu

*Nền của hai trụ kia. Không có data thật thì LEARN/GOVERN chỉ là dashboard rỗng.*

| # | Feature | Use-case | Nhu cầu thật | Ưu tiên |
|---|---|---|---|---|
| C1 | **Auto-capture mỗi run** — layer mỏng ghi ai · task · token/cost · code đổi · tool pass/fail · context dùng | "Không ai phải nhập tay; mở Claude Code chạy là có bản ghi" | mọi phân tích cần data không-ma-sát | **[MVP]** |
| C2 | **Multi-runtime adapter** — Claude Code trước, rồi Cursor/Codex/Copilot | "Đội dùng 3 tool khác nhau, vẫn về một schema" | tránh khoá cứng một vendor | **v2 ✅** Claude+Codex · **[Sau]** Cursor/Copilot |
| C3 | **Cost attribution** — gắn metadata agent · project · team · task vào từng run | "Hoá đơn tháng này chia theo project ra sao — đội nào đốt?" | chi phí AI khó dự báo, chưa ai chia được | **[MVP]** |
| C4 | **Unified schema human + agent** — kéo Jira/GitHub (việc người) và agent runs vào *cùng bảng* | "So người vs agent trên cùng thước đo" | điều kiện để LEARN so sánh công bằng | **[MVP]** |
| C5 | **Multi-layer Context Hub** — Company → Project → Team → Agent → Task, có chủ sở hữu | "CLAUDE.md của senior thành policy tổ chức, ở lại khi họ nghỉ" | tri thức bay mất khi người đi | **v5 ✅ (Company→Team→Agent)** |
| C6 | **Context version control** — diff + rollback context theo thời gian | "Đổi context xong agent tệ đi — revert về bản trước" | context drift là lỗi harness #1 | **v5 ✅** |
| C7 | **Background watcher** — bắt cả run khi wrapper bị bỏ qua | "Không lọt run nào, kể cả chạy tay" | audit phải đủ, không thủng | **[MVP]** |

---

## Trụ ② GOVERN — chặn cái sai, quyết & ghi lại

*Hai nhịp: guardrail (realtime, phòng ngừa) + closed loop (hậu kiểm). Đây là chỗ Dandori tách khỏi công cụ chỉ-quan-sát.*

### Nhịp 1 — Guardrail realtime (chặn tại tool-call, ngoài code agent)

| # | Feature | Use-case | Nhu cầu thật | Ưu tiên |
|---|---|---|---|---|
| G1 | **Pre-action block** — chặn lệnh nguy hiểm *trước khi* chạy: `rm -rf`, đụng `.env`/secret prod, xoá migration | "Agent định xoá thư mục ngoài scope — chặn ngay, ghi lại" | 81% tăng sự cố prod do code AI | **[MVP]** |
| G2 | **Scope / sandbox** — agent chỉ động vào working dir được cấp | "Không với tới root OS hay repo khác" | isolation là rào #1 để lên prod | **[MVP]** |
| G3 | **Budget circuit-breaker** — trần token/đô theo agent · project; cảnh báo 50/75/90% → hard stop | "Vượt hạn mức → dừng, không đợi cuối tháng giật mình" | spend dễ scale, khó forecast | **[MVP]** |
| G4 | **Permission gate realtime** — hành động rủi ro cao (deploy prod, breaking DB) → dừng, đòi human duyệt *ngay lúc đó* | "Không duyệt sau — chặn đúng thời điểm" | interruption point là control lõi | **[MVP]** |
| G5 | **Kill switch** — global hard-stop thu hồi mọi tool-permission; soft pause theo session | "Agent kẹt loop / chạy dại → cắt ngay" | kill switch = primitive bắt buộc | **MVP ✅** |
| G6 | **Post-action check** — sau mỗi action: lint/typecheck/schema-validate, trả feedback để agent tự sửa | "Sai kiểu là biết ngay, không đợi review" | bắt lỗi sớm trong execution | **v7 ✅** |

### Nhịp 2 — Closed loop & gate (hậu kiểm theo chu kỳ)

| # | Feature | Use-case | Nhu cầu thật | Ưu tiên |
|---|---|---|---|---|
| G7 | **Quality gate độc lập** — pipeline ngoài agent chấm & chặn run dưới ngưỡng (coverage, lint, security scan); separation of duties | "Người viết code không phải người duy nhất chấm code đó" | governance là bottleneck mới | **[MVP]** |
| G8 | **Closed loop tự động** — grade thấp → flag → mở review task (Jira) → gán reviewer → ghi quyết định | "Thấy agent tệ rồi *tự* làm gì đó, không thủ công" | control phải khép vòng, không dừng ở 'thấy' | **v3 ✅** |
| G9 | **Approval workflow** — task có lifecycle (TODO → IN REVIEW → DONE), mỗi lần duyệt ghi ai·khi nào·vì sao | "Mọi quyết định thành bản ghi bất biến" | auditor đòi bằng chứng control chạy đều | **MVP ✅** |

### Use-case xuyên suốt GOVERN

> **PO/PDM + QA vận hành, agent làm dev, không dev người.** PO viết requirement (Confluence), tạo PBI (Jira), review output; QA viết test case, theo dõi quality trên dashboard; agent nhận task → chạy → báo cáo lại. Dandori là cầu nối bọc ngoài. *(kế thừa concept dandori-cli)*

---

## Trụ ③ LEARN — đánh giá & rút tri thức

*Thứ lãnh đạo nhìn thấy trước nhất. Trả lời trực tiếp câu hỏi x100.*

| # | Feature | Use-case | Nhu cầu thật | Ưu tiên |
|---|---|---|---|---|
| L1 | **Grade A–F** trên 4 chỉ số: Acceptance · Success · Autonomy · Reliability | "Agent này giỏi không, đang lên không — như performance review" | không đo được productivity AI | **[MVP]** |
| L2 | **ROI / cost-effectiveness** — ghép chi phí × kết quả, tách phần lãi khỏi phần phí (code vứt, task fail, retry) | "$8,200 tốn, 38% là lãng phí — phần x100 nào đang lãi?" | biết token ≠ biết ROI | **[MVP]** |
| L3 | **Provenance** — mỗi số lần ngược về raw data; cùng input → cùng output, không ngưỡng ẩn | "Số này ở đâu ra? — dám quyết dựa trên nó" | evidence phải decision-grade | **[MVP]** |
| L4 | **Calibration theo fleet** — chuẩn theo phân vị trên chính tổ chức bạn (gồm việc người) | "Phân vị 85 — hơn 85% lượt làm, kể cả của người. Không magic number" | ngưỡng gõ tay = cảm tính | **[MVP]** |
| L5 | **Cross-fleet leaderboard** — xếp hạng, phân bố grade, đội lên/xuống | "Team A hay team B dùng AI tốt hơn — bằng chứng, không cảm giác" | so sánh cần cùng một bảng | **[MVP]** |
| L6 | **Trend / trust index** — xu hướng theo tuần, điểm tổng hợp 0–100 + autonomy band | "Agent đang lên hay tụt — có nên giao task khó hơn?" | cần tín hiệu để phân việc | **v3 ✅** |
| L7 | **Agent assignment** — gợi ý agent cho task theo capability/history/load, PO confirm | "Task này giao agent nào hợp nhất?" | phân việc còn thủ công | **v7 ✅** |
| L8 | **Knowledge capture** — đóng gói pattern/prompt/context tốt thành tri thức tái dùng | "Senior nghỉ, tri thức ở lại" | tri thức đi theo người nghỉ | **v3 ✅** |

---

## Tầng giao diện — không phải dashboard, là **operations console**

> Đây là chỗ đổi tư duy: UI **không phải một bản báo cáo để xem** — nó là **bàn điều khiển** nơi con người *lái* cả đội agent. Nguyên tắc: **mỗi con số phải có một động từ đứng cạnh.** Thấy agent grade D không dừng ở "thấy" — ngay đó pause / hạ band / mở review.
>
> Với data đã CAPTURE + bốn service liên kết (Jira · Confluence · GitHub · Drive) + agent runtime, UI làm được nhiều hơn hẳn việc nhìn. HTMX hợp đúng mấy dạng này: live-queue poll, inline-edit, wizard form, drill-down panel — round-trip server rẻ, không cần SPA.

### U-A · Điều khiển đội agent (fleet control)

| # | Feature | User *làm gì* | Trụ | Service | Ưu tiên |
|---|---|---|---|---|---|
| UA1 | **Launch run từ browser** | chọn agent + task Jira + context layer → chạy | GOVERN·CAPTURE | Jira, runtime | **v6 ✅ (claude)** |
| UA2 | **Kill switch theo dòng** | nút đỏ trên mọi run đang chạy → dừng + ghi lý do | GOVERN | runtime | **[MVP]** |
| UA3 | **Set budget inline** | sửa trần $/token của run/agent/project ngay tại ô | GOVERN | runtime | **[MVP]** |
| UA4 | **Promote/demote autonomy band** | dropdown chuyển agent giữa band (supervised → auto-merge) | LEARN·GOVERN | runtime, GitHub | **v3 ✅** |
| UA5 | **Retry / retry-with-fixes** | chạy lại với cùng context, hoặc sửa prompt trước khi chạy | CAPTURE | runtime | **v6 ✅** |
| UA6 | **Bulk action** | chọn N run → pause/kill/set-budget cùng lúc | GOVERN | runtime | **v6 ✅ (kill/budget)** |

### U-B · Đóng vòng governance (review queue) — *nhóm giá trị nhất*

| # | Feature | User *làm gì* | Trụ | Service | Ưu tiên |
|---|---|---|---|---|---|
| UB1 | **Review queue** | inbox live-poll các run bị flag đang chờ người quyết | GOVERN | runtime, GitHub | **[MVP]** |
| UB2 | **Approve/Reject + lý do** | one-click duyệt/từ chối; free-text "vì sao" rơi thẳng vào audit bất biến | GOVERN | runtime | **[MVP]** |
| UB3 | **Permission-gate modal** | agent chạm lệnh gated → modal hỏi người: allow once / allow always / deny | GOVERN | runtime | **[MVP]** |
| UB4 | **Quality-gate override** | gate fail → người override kèm justification (ghi lại) | GOVERN | GitHub | **v7 ✅** |
| UB5 | **Escalation routing** | flag chưa xử lý quá SLA → tự đẩy lên eng lead | GOVERN | — | **v3 ✅** |

### U-C · Hành động xuyên service (Dandori là hub, không phải silo)

| # | Feature | User *làm gì* | Trụ | Service | Ưu tiên |
|---|---|---|---|---|---|
| UC1 | **Flag → Jira ticket** | biến run lỗi/flag thành bug Jira, pre-fill link run + log | GOVERN·CAPTURE | Jira | **[MVP]** |
| UC2 | **Transition Jira inline** | đổi trạng thái ticket (In Progress → Done) ngay từ dòng run | CAPTURE | Jira | **v7 ✅** |
| UC3 | **Post Confluence report** | một click đăng summary run/sprint lên Confluence | LEARN | Confluence | **v2 ✅** |
| UC4 | **PR comment/approve** | comment hoặc approve PR agent mở, ngay trong Dandori | GOVERN | GitHub | **v7 ✅** |
| UC5 | **Revert-detector → action** | phát hiện code agent bị revert → gợi ý "mở incident / demote agent" | LEARN·GOVERN | GitHub | **v2 ✅** |
| UC6 | **Kéo Drive doc vào context** | search Drive, gắn doc làm context layer một click | CAPTURE | Drive | **v7 ✅** |
| UC7 | **Duyệt trong Slack** | review queue đẩy vào Slack; nút Approve/Reject interactive ngay trong tin nhắn → ghi lý do vào audit bất biến | GOVERN | Slack | **[MVP]** |
| UC8 | **Sheets export cost/ROI** | đẩy bảng cost·leaderboard·ROI ra Google Sheets cho PO/finance đọc nơi họ vốn ở | LEARN·CAPTURE | GWS Sheets | **v7 ✅** |
| UC9 | **Calendar review/sprint** | tạo lịch review, gắn deadline duyệt vào Google Calendar | GOVERN | GWS Calendar | **v7 ✅** |

> UC7 nối thẳng UB1/UB2: review queue *có mặt cả trên web lẫn trong Slack*. Với low-tech, duyệt trong Slack tự nhiên hơn mở web — mà bằng chứng "ai·khi nào·vì sao" vẫn rơi vào cùng một audit trail.

### U-D · Context Hub như một sản phẩm (cái *moat*)

| # | Feature | User *làm gì* | Trụ | Service | Ưu tiên |
|---|---|---|---|---|---|
| UD1 | **Context editor + version** | sửa mọi layer (Company→Task), tự version, ghi ai/khi nào | CAPTURE | — | **v5 ✅** |
| UD2 | **Context diff + rollback** | so hai version một CLAUDE.md/policy, revert một click | CAPTURE·GOVERN | — | **v5 ✅** |
| UD3 | **Promote to org policy** | "thăng" context team/agent tốt lên tầng Company | CAPTURE·LEARN | — | **v5 ✅** |
| UD4 | **"Effective context" preview** | xem đúng context đã merge mà agent *thực sự* thấy cho một task | CAPTURE | — | **v5 ✅** |

> UD4 bị đánh giá thấp một cách oan: câu hỏi debug số một luôn là *"agent thật ra biết những gì?"* — preview này trả lời thẳng.

### U-E · Cấu hình & policy bằng form (không sửa YAML — đúng low-tech)

| # | Feature | User *làm gì* | Trụ | Service | Ưu tiên |
|---|---|---|---|---|---|
| UE1 | **Budget policy form** | đặt budget + ngưỡng circuit-breaker theo agent/project/team bằng form | GOVERN | — | **[MVP]** |
| UE2 | **Guardrail rule builder** | form "chặn lệnh khớp X" / "đòi duyệt nếu Y" — không YAML | GOVERN | runtime | **v3 ✅** |
| UE3 | **Quality-gate threshold UI** | slider min-grade, %test-pass, coverage trước khi auto-merge | GOVERN·LEARN | GitHub | **v7 ✅** |
| UE4 | **Policy simulator** | "thử rule này trên 30 ngày run vừa rồi — bao nhiêu lượt bị chặn?" | GOVERN | — | **v3 ✅** |

> UE4 là **feature xây niềm tin**: không ai dám bật một guardrail mà họ không preview được hậu quả.

### U-F · Điều tra & giải thích (analyst tools)

| # | Feature | User *làm gì* | Trụ | Service | Ưu tiên |
|---|---|---|---|---|---|
| UF1 | **Drilldown** | click số tổng → chi tiết → raw run | CAPTURE | — | **[MVP]** |
| UF2 | **Provenance explorer** | đi ngược mọi con số (grade/$/ROI) về đúng raw event sinh ra nó | LEARN·CAPTURE | all | **[MVP]** |
| UF3 | **Run comparison** | chọn 2+ run → so cost/token/tool/grade cạnh nhau | LEARN·CAPTURE | — | **v2 ✅** |
| UF4 | **Explain cost spike** | click điểm bất thường trên chart cost → breakdown cái gì đẩy lên | LEARN·CAPTURE | runtime | **v2 ✅** |
| UF5 | **"Why did this fail?" trace** | timeline tool-call, tô đậm bước fail + context tại thời điểm đó | CAPTURE·GOVERN | runtime | **v3 ✅** |

> UF2 là **feature độ tin cậy**: biến "tin cái dashboard" thành "kiểm chứng được cái dashboard".

### U-G · Chủ động & thói quen (để người *sống* ở đây, không chỉ ghé)

| # | Feature | User *làm gì* | Trụ | Service | Ưu tiên |
|---|---|---|---|---|---|
| UG1 | **Morning standup view** | landing page theo vai: "đội bạn đêm qua làm gì + cái gì *cần bạn* hôm nay" | LEARN·GOVERN | Jira, GitHub | **[MVP]** |
| UG2 | **Alert subscription (đa kênh)** | đăng ký điều kiện (budget >80%, grade tụt, revert) → đẩy ra **Slack** (channel/DM) hoặc **Gmail** | GOVERN·LEARN | Slack, GWS Gmail | **[MVP]** |
| UG2b | **Digest theo vai** | daily/weekly fleet-health digest gửi Slack (đội) + Gmail (người không ở Slack) | LEARN | Slack, GWS Gmail | **v7 ✅** |
| UG3 | **Saved views** | lưu bảng đã lọc (vd "run fail của team tôi") thành tab đặt tên | CAPTURE | — | **v7 ✅** |
| UG4 | **Run → playbook** | biến run tốt thành template tái dùng (task+context+guardrail) | LEARN | runtime | **v3 ✅** |
| UG5 | **Live fleet wallboard** | chế độ TV: run đang chạy, gauge spend, độ sâu queue | CAPTURE·GOVERN | runtime | **v7 ✅** |
| UG6 | **Compliance export** | bundle JSON/CSV + audit trail, xuất sang SIEM | GOVERN·CAPTURE | — | **v2 ✅** |

### U-H · Nền tảng UI

| # | Feature | User *làm gì* | Ưu tiên |
|---|---|---|---|
| UH1 | **Dashboard 3 tầng** — engineer · project · org, CWD-aware landing | mỗi vai mở ra thấy đúng thứ cần | **[MVP]** |
| UH2 | **CLI song song web** — mọi analytics chính có bản terminal | không mở browser vẫn xem được | **[MVP]** |

### 8 lựa chọn đòn bẩy cao nhất

Nếu phải xếp thứ tự làm, đây là tám cái đáng nhất — hầu hết *tái dùng data đã có*, effort thấp, đổi hẳn cảm giác sản phẩm:

1. **UB1+UB2 Review queue + Approve/Reject có lý do** — động từ giá trị nhất; đóng vòng GOVERN, nuôi audit trail. Flow HTMX live-poll hoàn hảo. *Effort thấp, tác động lớn nhất.*
2. **UG1 Morning standup view** — trả lời "cái gì *cần tôi* hôm nay"; biến tool ghé-hàng-tuần thành thói quen hàng ngày. Chỉ ghép data sẵn có.
3. **UC1 Flag → Jira ticket** — chứng minh Dandori là hub không phải silo; tiết kiệm thời gian lớn; dễ làm.
4. **UE4 Policy simulator** — "thử guardrail trên 30 ngày qua" trước khi bật. Mở khoá việc dám dùng các feature GOVERN.
5. **UF2 Provenance explorer** — đi ngược mọi số về raw event. Feature độ tin cậy làm mọi metric khác đáng tin.
6. **UD1+UD2+UD4 Context editor + version + effective-preview** — biến Context Hub thành sản phẩm thật, không phải một folder.
7. **UE1+UE2 Rule builder bằng form** — đưa GOVERN vào tầm tay PO/QA low-tech. Đúng luận điểm audience-fit của cả dự án.
8. **UG4 Run → playbook** — biến thắng lợi lẻ thành tài sản tổ chức tái dùng; giá trị cộng dồn; flywheel của LEARN.

### HTMX hợp / không hợp

- **Hợp mạnh:** review queue & wallboard (`hx-trigger="every 3s"` poll) · inline-edit budget/band/Jira-status/context (`hx-patch` swap một dòng) · wizard form (guardrail builder, policy simulator, launch-run, flag→ticket) · drill-down panel (provenance, explain-spike, why-failed — click swap partial) · modal approve/reject.
- **Không nên ép qua HTMX:** command palette (Cmd-K) và drag-reassign cần JS client thật; context diff nên dùng lib diff riêng.

*Toàn bộ theo stack đã chốt (Go + HTMX server-render): low-tech mở là dùng, không build step. Xem [02-tech-stack.md](02-tech-stack.md).*

---

## Ranh giới scope — YAGNI

Trung thực về cái **không** làm (ít nhất ở bản đầu), để khỏi phình:

- **Không thay Jira/GitHub** — đó *là* task board & source of truth; Dandori đọc/ghi, không dựng lại.
- **Không thay CI/CD** — quality gate *gọi* pipeline có sẵn, không viết lại runner.
- **Không tự viết code** — không thay Claude Code/Cursor; Dandori bọc ngoài.
- **Chưa làm** multi-user SSO/RBAC đầy đủ, knowledge marketplace, multi-runtime ngoài Claude Code → để **[Sau]** khi MVP chứng minh giá trị.

---

## Lát cắt MVP (nếu chỉ được làm một vòng đầu)

Chọn đúng những mục **[MVP]** khép được vòng nhỏ nhất mà vẫn *demo được cả ba trụ*:

**CAPTURE** C1 auto-capture · C3 cost attribution · C4 unified schema · C7 watcher
**GOVERN** G1 pre-action block · G2 sandbox · G3 budget breaker · G4 permission gate · G7 quality gate
**LEARN** L1 grade · L2 ROI · L3 provenance · L4 calibration · L5 leaderboard
**UI (act, không chỉ view)** UH1 dashboard 3 tầng · UH2 CLI · UF1 drilldown · UF2 provenance explorer · **UB1+UB2 review queue + approve/reject** · UB3 permission-gate modal · UA2 kill switch · UA3 set-budget inline · UE1 budget form · UC1 flag→Jira · UG1 morning standup
**Service (đội đang dùng)** Jira (UC1) · **Slack: UC7 duyệt interactive + UG2 alert** · GWS/GitHub đọc-context · *(Confluence/Sheets/Calendar/PR-write để [Sau])*

→ Đủ kể trọn câu chuyện x100, và UI đã là **console hành động**: *thu data thật → chặn cái sai realtime + người duyệt ngay trên UI → đánh giá agent như đánh giá người, có ROI, truy ngược được từng số*.

---

## Câu hỏi chưa chốt

Ba câu về feature:
- Multi-runtime: làm Cursor/Codex ngay MVP hay đợi? (ảnh hưởng độ phức tạp adapter C2)
- Quality gate G7 tự chạy scan hay chỉ *đọc* kết quả CI có sẵn? (build vs integrate)
- Kill switch — MVP hay [Sau]? (nhu cầu thật mạnh, nhưng cần cơ chế thu hồi permission chắc)

Ba câu về UI hành động (mới, quyết định phần lớn scope Console ở trên):
- **Độ sâu điều khiển runtime** — agent runtime có *thật sự nhận* tín hiệu pause/kill/budget giữa chừng không, hay Dandori chỉ observe-only? Đây là câu **quan trọng nhất**: nó quyết định nhóm U-A là điều khiển thật hay chỉ UI làm màu.
- **Quyền ghi lên service liên kết** — có (và có muốn) creds *ghi* Jira/Confluence/GitHub không, hay chỉ đọc? Chặn toàn bộ nhóm U-C.
- **RBAC trước khi ship write-action** — ai được kill run của ai, promote context của ai? UI governance cần một câu chuyện phân quyền trước khi mở các nút ghi. *(Gợi ý: **Google SSO + Directory** của GWS là mảnh trả lời sẵn — login bằng Google, lấy user/team/org từ Directory. Để **[Sau]** cùng RBAC, nhưng đây là đường đi rõ ràng nhất.)*

---

## Đọc tiếp

- **Tầm nhìn** — ba trụ: [01-vision.md](01-vision.md)
- **Tech stack** — Go + HTMX: [02-tech-stack.md](02-tech-stack.md)
