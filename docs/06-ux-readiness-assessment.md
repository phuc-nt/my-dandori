# Đánh giá độ sẵn sàng UX & vận hành

> Rà soát 3 tiêu chí trải nghiệm/vận hành đối chiếu code thật (2026-07-04).
> Bổ sung cho [05-product-gap-assessment.md](05-product-gap-assessment.md), vốn đánh giá gap so với vision. File này hỏi câu khác: **một tổ chức low-tech, với một CEO không đọc code, có dùng được sản phẩm này không**.
> Căn cứ bằng `file:line`, không phải cảm tính.

## Kết luận một câu

Ba tiêu chí đạt lệch nhau rõ: **UI tối thiểu thao tác đã làm rất tốt (đạt)**, **GOVERN có nền vững nhưng thiếu tầng executive để C-level dùng hàng ngày (đạt một phần, ~60%)**, và **cài đặt thì binary xuất sắc nhưng khoảng cách "tải xong → chạy thật" quá dốc cho người low-tech (đạt một nửa)**. Nút thắt chung của cả ba là cùng một thứ: thiếu một lớp onboarding/executive bằng ngôn ngữ người dùng, chứ không phải thiếu năng lực backend.

## Bảng điểm nhanh

| Tiêu chí | Điểm | Trạng thái |
|---|---|---|
| ① Dễ cài đặt & dùng với low-tech | C+ | Binary A, cấu hình D. Đạt một nửa |
| ② Tối thiểu thao tác UI, không cắt xén backend | A− | Đạt. 1-2 click cho tác vụ chính, backend không bị che |
| ③ GOVERN hữu dụng với C-level | B− (~60%) | Nền vững, thiếu tầng executive seamless |

---

## ① Dễ cài đặt và sử dụng với low-tech: C+ (đạt một nửa)

Chia làm hai nửa tách bạch: **cài binary thì low-tech thật**, nhưng **cấu hình để chạy thật thì đòi kỹ thuật**.

### Nửa dễ (binary + khởi động): làm tốt

- **Single binary pure-Go, no CGO, ~22MB.** Không cần Go toolchain lúc chạy, không Node/npm, không Docker, không asset pipeline. Migration + template + static đều `go:embed` vào binary ([internal/store/migrate.go:9](../internal/store/migrate.go)). Tải một file, chạy. Đây là mức low-tech lý tưởng.
- **Default an toàn, không cần config để khởi động.** `DryRun=true`, DB `~/.dandori/dandori.db` tự tạo, listen `127.0.0.1:4777` localhost-only, budget mặc định $50/tháng ([internal/config/config.go:89-111](../internal/config/config.go)). Chạy được ngay mà chưa cần khai báo gì.
- **DB tự dựng lần đầu.** Tự tạo thư mục, chạy 14 migration trong transaction, rollback nếu lỗi, tracked qua `PRAGMA user_version` ([internal/store/store.go:24-48](../internal/store/store.go)). Không có lệnh migrate thủ công.
- **Tích hợp thiếu thì suy giảm mềm, không crash.** Thiếu credential → trả HTTP 503 "atlassian credentials not configured" ([internal/web/handlers_writeactions.go:39-41](../internal/web/handlers_writeactions.go)), không sập server.

### Nửa khó (cấu hình + tích hợp): chặn người low-tech

- **Không có UI nhập credential.** Muốn nối Jira/Slack/GitHub/Google phải sửa `.env` hoặc set env var trước khi chạy. Không có form paste token, không có nút "Test Connection", không có credential vault trong console. Đây là gap lớn nhất của tiêu chí này.
- **Tích hợp đòi kỹ năng kỹ thuật thật:**
  - Slack: trích cookie `xoxc`/`xoxd` từ DevTools trình duyệt, giữ URL-encode. **Khó** với người không rành DevTools.
  - Google Workspace: `gws` CLI (tool không chính thức) + OAuth flow. **Khó**.
  - GitHub: `gh auth login` qua CLI. **Trung bình**.
  - Jira: lấy API token từ portal Atlassian, dán vào `.env`. **Trung bình**, có doc.
  - OpenRouter: một API key. **Dễ**.
- **`/healthz` rỗng.** Chỉ trả `{"ok":true}` ([internal/web/server.go:83-86](../internal/web/server.go)), không validate config, không cảnh báo tích hợp nào chưa nối, không nhắc "chưa hook project nào".
- **Không có wizard onboarding.** `dandori init --project ... --agent ...` là bước CLI ([internal/cli/init_cmd.go:20-48](../internal/cli/init_cmd.go)), idempotent nhưng vẫn là dòng lệnh. Không có luồng hướng dẫn 3 bước trong UI.

### Chốt tiêu chí ①

Với **kỹ sư**: cài và cấu hình trơn tru. Với **CEO/PM low-tech**: tải và mở console được, nhưng **không tự nối tích hợp hay khởi tạo project mà không có người kỹ thuật kèm**. Một tính năng **UI quản lý credential (paste + test trong trình duyệt)** sẽ chuyển tiêu chí này từ C+ lên A−, vì nó xoá đúng khúc dốc duy nhất.

---

## ② Tối thiểu thao tác UI, không đánh đổi backend: A− (đạt)

Đây là tiêu chí đạt rõ nhất. UI cắt bước tốt mà backend không bị che.

### Số bước cho tác vụ chính: đã tối thiểu

- **Duyệt approval:** 1 click. Vào `/` (exec home) → nút "Duyệt" → HTMX POST, inbox tự refresh fragment, không reload ([internal/web/templates/pages/exec_home.html:69-74](../internal/web/templates/pages/exec_home.html), handler [handlers_exec.go:42-56](../internal/web/handlers_exec.go)).
- **Kill run:** 1 click trên `/runs/{id}`, status badge tự đổi, nút biến mất (`hx-swap="outerHTML"`) ([run_detail.html:138-141](../internal/web/templates/pages/run_detail.html)).
- **Override gate:** 1 form inline, bắt buộc `reason` (400 nếu rỗng), cập nhật fragment tại chỗ ([handlers_writeactions.go:124-144](../internal/web/handlers_writeactions.go)).
- **Xem grade/AI review:** async load (`hx-trigger="load"`), dashboard hiện ngay, review nạp ngầm không block.
- **Sửa context:** team/agent lưu thẳng 1 POST; company-layer 2 bước (request → duyệt) là **đúng chủ đích** vì đây là chính sách nhạy cảm, không phải bước thừa.

### Realtime tự động, không phải bấm tay

- Review queue tự poll mỗi 3s ([reviews.html:5](../internal/web/templates/pages/reviews.html)); log/status run mỗi 2s với **HTTP 286 stop** khi run kết thúc (không poll vô ích); budget mỗi 10s; wallboard TV mỗi 5s.
- **Approval không khoá vào UI:** duyệt qua Slack reaction ghi cùng audit trail, first-writer-wins ([handlers_exec.go:48](../internal/web/handlers_exec.go) + Slack poller). UI không phải điểm nghẽn phê duyệt.

### Backend không bị UI cắt xén

20 màn console phủ mọi tác vụ lặp lại. Chỉ ba thứ **cố ý** để CLI vì là one-time/nền:

- `dandori audit verify` (kiểm tra tính toàn vẹn hash-chain): chạy khi cần, không phải việc hàng ngày.
- Flywheel detect/promote/publish: vòng học chạy nền.
- Hook setup (`dandori init`, `hook wrap`): cấu hình một lần.

Guardrail/budget/audit/closed-loop đều chạy ở tầng hạ tầng; UI chỉ là chỗ cấu hình và quan sát điểm điều khiển, không phải chỗ "làm mỏng" backend.

### Điểm còn rườm (nhỏ, không phải lỗi)

- Dropdown Jira transition fetch mỗi lần load run detail ([handlers_writeactions.go:25-52](../internal/web/handlers_writeactions.go)), an toàn TOCTOU nhưng có thể cache ngắn.
- Diff lịch sử context phải click từng version thay vì tự hiện 2 bản mới nhất.
- Rule simulate phải bấm nút, chưa auto-sim khi gõ pattern.

### Chốt tiêu chí ②

Đạt. UI đơn giản mà không đơn giản hoá backend: operator vẫn thấy trọn timeline sự kiện, guardrail block, audit, nested approval, grade, cost. Ba điểm rườm là tinh chỉnh tương lai, không ảnh hưởng hiệu quả.

---

## ③ GOVERN hữu dụng với CEO / level C: B− (~60%, đạt một phần)

Nền tảng đúng và sâu, nhưng thiếu một lớp "executive seamless" để một người không-kỹ-thuật dùng nó làm công cụ quản trị rủi ro hàng ngày.

### Đã hữu dụng cho C-level

- **Standup + Reviews = "hôm nay cần bạn làm gì".** Một trang gom pending approval, open flag, budget health; nút Approve/Reject gọn ([standup.html:14-22](../internal/web/templates/pages/standup.html), [reviews.html](../internal/web/templates/pages/reviews.html)).
- **Wallboard TV mode:** 3 chỉ số tổng (đang chạy, chi tiêu vs ngân sách có màu đỏ/vàng, hàng chờ), ngôn ngữ nghiệp vụ, không jargon ([wallboard_fragment.html](../internal/web/templates/partials/wallboard_fragment.html)).
- **Org dashboard:** leaderboard theo agent (grade, cost, useful %, trend Δ7d), cost trend 14 ngày, change-failure rate (DORA, C-level quen) ([handlers_dash.go:27-51](../internal/web/handlers_dash.go)).
- **Slack approval bridge:** CEO duyệt ngay trong Slack bằng reaction, có whitelist approver (separation of duties ở tầng hạ tầng) ([internal/integrations/slack/approvals.go:12-76](../internal/integrations/slack/approvals.go)).
- **Ngôn ngữ nghiệp vụ ở nhiều chỗ đúng chuẩn.** Closed-loop reason ghi rõ công thức thay vì điểm trần: `"low grade D: composite 45.2 over 120 runs (acceptance 82.0 · success 76.5 · ...)"` ([closed_loop.go:76-79](../internal/govern/closed_loop.go)). Budget alert: `"budget global at 95% ($4750/$5000)"` thay vì `budget_warn_fired` ([budget.go:79-82](../internal/govern/budget.go)).
- **Closed-loop tự xử, người chỉ duyệt mấu chốt:** F→auto-demote (an toàn bất đối xứng), D→đề xuất demote chờ duyệt, A/B/C→tự resolve flag ([closed_loop.go:11-61](../internal/govern/closed_loop.go)). Đúng cảm giác "hệ thống tự chạy, tôi chỉ vào ở điểm quan trọng".
- **Compliance export** JSON/CSV sẵn cho auditor.

### Thiếu tầng executive (chỗ chưa tới)

- **Không có risk heat-map tổng.** Muốn thấy toàn cảnh "agent nào nguy hiểm, approval nào chờ lâu, flag nào open" phải đi qua nhiều trang rời. Thiếu một trang tổng rủi ro duy nhất.
- **Closed-loop demote im lặng.** F-grade auto-demote xảy ra trong worker nền, **không báo CEO** ("vừa hạ agent X từ trusted xuống supervised vì grade F"). Đây là đúng chỗ cần một email/Slack alert.
- **Approval thiếu ước lượng tác động.** Card chỉ hiện action + reason (vd "Edit /src/main.py · supervised band"), chưa có "sửa tương tự lần trước tốn $X, đụng Y file". CEO thiếu dữ kiện để duyệt tự tin ([handlers_reviews.go](../internal/web/handlers_reviews.go)).
- **Autonomy band chỉ "hard switch".** Đổi band là một dropdown áp dụng ngay ([dash_agent.html:4-12](../internal/web/templates/pages/dash_agent.html)), chưa có staged rollout ("đổi ở run test kế tiếp") hay trang lịch sử điều chỉnh band riêng. CEO ngại demote tức thì.
- **Audit trail chưa có view tổng cho C-level.** Có log và export, nhưng không có trang web lọc/tóm tắt ("tất cả thay đổi band 7 ngày qua"), phải export CSV rồi tự đọc ([compliance_export.go:95-102](../internal/govern/compliance_export.go)).
- **Vài chỗ còn jargon.** Mô tả rule "block denies outright · gate pauses · critical gates even trusted agents" ([rules.html:3](../internal/web/templates/pages/rules.html)) quá kỹ thuật; TTL approval (gate hết hạn 10 phút, band-proposal không hết hạn, [gate.go:107-126](../internal/govern/gate.go)) đúng logic nhưng UI không hiện "còn 9m 34s".

### Chốt tiêu chí ③

Đạt phần lõi: một C-level đã có standup để duyệt, wallboard để nhìn, Slack để phê nhanh, leaderboard để so, closed-loop để hệ thống tự xử. Chưa đạt phần "seamless": thiếu bản đồ rủi ro tổng, thiếu thông báo chủ động khi hệ thống tự hành động, thiếu ngữ cảnh tác động lúc duyệt. Đây là khoảng cách giữa "một dashboard đủ dùng" và "một công cụ điều hành mà CEO mở mỗi sáng".

---

## Nút thắt chung của cả ba tiêu chí

Cả ba đều dừng ở cùng một biên: **backend đã đủ mạnh, cái thiếu là lớp giao tiếp với người dùng cuối bằng ngôn ngữ của họ.**

- Tiêu chí ① thiếu **UI credential + wizard** (người low-tech không tự cấu hình được).
- Tiêu chí ③ thiếu **tầng executive + thông báo chủ động** (CEO không tự nắm toàn cảnh rủi ro).
- Tiêu chí ② đạt chính vì nó **là** lớp giao tiếp đó, làm tốt.

Nói cách khác: sản phẩm đã chứng minh backend làm được việc khó (guardrail thật, closed-loop thật, audit thật). Việc còn lại nhẹ hơn về kỹ thuật nhưng nặng về sản phẩm: **gói năng lực đó vào những màn hình mà một người không-kỹ-thuật mở lên là hiểu và hành động được ngay.**

## Đề xuất ưu tiên

Xếp theo tác động trên cả ba tiêu chí, không theo độ khó:

1. **UI quản lý credential (paste + test connection).** Gỡ khúc dốc lớn nhất của tiêu chí ①. Tác động cao nhất trên một tính năng.
2. **Thông báo chủ động closed-loop** (email/Slack khi auto-demote, khi budget vượt, khi flag mở lâu). Biến GOVERN từ "phải vào xem" thành "hệ thống báo khi cần". Tác động lên tiêu chí ③.
3. **Trang tổng rủi ro (risk overview)** gom agent yếu + approval chờ + flag open + budget nóng vào một chỗ. Tác động lên tiêu chí ③.
4. **Wizard onboarding + `/healthz` có nội dung** (báo tích hợp nào chưa nối, project nào chưa hook). Tác động lên tiêu chí ①.
5. **Ước lượng tác động trên approval card** (cost/file/rủi ro của hành động tương tự trước đó). Tác động lên tiêu chí ③.

Bốn trong năm đề xuất là **UI/product**, không phải năng lực lõi. Điều này khớp kết luận của [05](05-product-gap-assessment.md): cái thiếu để adopt không phải feature backend, mà là lớp làm cho tổ chức thật (nhiều người, không kỹ thuật) dùng được. Ở [05] lớp đó là **danh tính & phân quyền**; ở đây là **onboarding & executive UX**. Hai mảnh này cùng thuộc một milestone "product-ready cho tổ chức".

## Câu hỏi chưa giải quyết

- Credential UI lưu token vào đâu cho an toàn: vẫn `.env` (ghi qua luồng có kiểm soát), hay một secret store riêng? Liên quan gap "data-at-rest chưa mã hoá" ở [05].
- Thông báo chủ động: gửi qua kênh nào mặc định (Slack digest có sẵn vs email GWS)? Ngưỡng nào đáng báo để không gây nhiễu?
- Risk overview và tầng executive UX nên gộp vào milestone v8 (Identity & RBAC) hay tách riêng, vì cả hai đều nhắm "tổ chức thật"?
- Ước lượng tác động cần dữ liệu lịch sử "hành động tương tự", lấy từ đâu và tính lúc nào (khi tạo approval hay lazy khi render)?

---

## Re-score sau v8 (2026-07-06)

Milestone **v8 Onboarding & Executive UX** đã làm cả 5 đề xuất trên. Câu hỏi chưa giải quyết được chốt trong quá trình plan + red-team (xem [plan v8](../plans/260705-1512-dandori-v8-onboarding-executive-ux/plan.md)).

| Tiêu chí | Trước | Sau v8 | Bằng chứng |
|---|---|---|---|
| ① Cài đặt low-tech | C+ | **A−** | Credential UI qua trình duyệt ([handlers_settings.go](../internal/web/handlers_settings.go)) + Test connection ([probe.go](../internal/integrations/probe/probe.go)); wizard 3 bước ([handlers_welcome.go](../internal/web/handlers_welcome.go)); `/healthz` có nội dung thật ([health.go](../internal/web/health.go)); `dandori init` ghi `hooked:*` để bước 1 ✓ ngay. Nối 1 integration hoàn toàn qua UI, không sửa file tay. Còn lại: token vẫn cần lấy từ nguồn ngoài (Slack DevTools), giới hạn cố hữu không xoá được. |
| ② UI tối thiểu bước | A− | **A−** (giữ) | Không đổi; các trang mới theo cùng pattern HTMX fragment. |
| ③ GOVERN cho C-level | B− (~60%) | **B+/A−** | Thông báo chủ động qua Slack Alerter cho demote/budget/flag-stale, message tiếng Việt + link ([alerts.go](../internal/integrations/slack/alerts.go), [closed_loop.go](../internal/govern/closed_loop.go)); trang tổng rủi ro `/risk` ([handlers_risk.go](../internal/web/handlers_risk.go)); ước lượng tác động trên approval card ([impact_estimate.go](../internal/learn/impact_estimate.go)). Còn lại: healthz `age_days` cảnh báo test cũ nhưng không tự re-probe token chết. |

**Quyết định đã chốt** (khác bản đề xuất, do red-team): (a) Credential ghi `.env` write-through nhưng **yêu cầu restart** để worker nhận (workers bind config lúc boot, không hot-swap được an toàn); (b) thông báo dùng `slack.Alerter` **sẵn có** + events, KHÔNG xây đường notify mới (tránh double-message + latency trong hot-path guardrail); (c) impact estimate advisory-minimal, exclude synthetic action, cache qua settings.

**Nút thắt còn lại cho "tổ chức thật":** đúng như [05], đó là **danh tính & phân quyền** — đã **SHIP ở v10 (260707)**: login local + principal thật vào audit + per-operator token + 2-role gate 29 route ghi. (v9 thực tế rẽ sang capture-gap + insights vì red-team phát hiện data nền rỗng; xem [04](04-implementation-notes.md) mục v9.) v8 đóng lớp onboarding/executive UX; v10 đóng lớp auth/RBAC tối thiểu. Hai mảnh cùng thuộc mục tiêu "product-ready cho tổ chức" — còn lại data-at-rest encryption + full granular RBAC/SSO là [Sau] (xem [05](05-product-gap-assessment.md)).

### Ghi chú H3 — identity-fork trong analytics (v10)

v10 tách "operator" thành hai loại row cùng bảng `operators`: (a) **console login account** (`username`/`password_hash` do `operator add` tạo, canonical id = username), và (b) **machine-principal** tự động (`alice@dev-laptop` kiểu, `ResolveOperator` tạo khi capture thấy máy mới, không có password). Trước v10, mọi phân tích theo "operator" (leaderboard người, `TeamCompare`) chỉ thấy loại (b) vì chưa có (a). Sau v10, hai loại tồn tại song song trong cùng cột `operator_id` trên `runs`/`api_tokens` — một người dùng console qua login (`alice`) và cùng người đó capture qua hook trên máy riêng (`alice@dev-laptop`) là **hai id khác nhau về mặt dữ liệu**, dù cùng một con người. Hệ quả: analytics per-operator (behavior, steering, TeamCompare) trước/sau cutover per-operator-token có thể split thành 2 dòng cho cùng một người, thay vì gộp. Reconcile (map machine-principal → console account cùng người) **chưa làm — [Sau]**, cần khi pilot thật cho thấy split này gây nhiễu số liệu; hiện tại chấp nhận vì central-mode mới bắt đầu chuyển sang per-operator token, mẫu còn nhỏ.
