# Đánh giá khoảng trống sản phẩm — Dandori vs Vision

> Rà soát từ vision gốc ([01-vision.md](01-vision.md)) đối chiếu với code thật (2026-07-04).
> Mục đích: trả lời thẳng "sản phẩm hiện tại thiếu gì để một tổ chức dám adopt".
> Căn cứ bằng `file:line`, không phải cảm tính.

## Kết luận một câu

Dandori **thực thi đúng và sâu cả 3 trụ cho kịch bản 1 đội / 1 máy** — không phải dashboard làm màu — nhưng **chưa lên production cho tổ chức nhiều người vì thiếu hẳn tầng danh tính & phân quyền (auth/RBAC)**. Vision viết cho "tổ chức", code hiện chạy cho "một người".

## Điểm quyết định đã verify: điều khiển runtime là THẬT

Câu hỏi vision đánh dấu "quan trọng nhất" ([03-features.md:255](03-features.md)): agent runtime có *thật sự nhận* tín hiệu chặn giữa chừng, hay chỉ observe-only?

**Trả lời: THẬT ở tầng tool-call.** Hook pre-tool ([internal/cli/pre_tool.go:41-55](../internal/cli/pre_tool.go)) phát ra `PermissionDecision: deny/ask` JSON mà Claude Code tuân theo — chặn tool-call thật, kể cả agent chạy ngoài (hook-captured), không chỉ agent launch từ console. Guardrail nằm ngoài agent code (trong hook, chạy trước tool) nên **không bypass được bằng prompt**. Đây là ranh giới sống-còn giữa "lớp quản trị thật" và "UI làm màu" — Dandori ở đúng phía thật.

## 3 trụ — cái gì vững, cái gì nông

### ① CAPTURE (~85%, vững)

**Thật:**
- Auto-capture qua hook, fail-open ([internal/capture/ingest.go:24-44](../internal/capture/ingest.go)) — lỗi capture ghi stderr + exit 0, không bao giờ chặn phiên user.
- Cost dedup theo assistant message-ID ([internal/capture/transcript.go:74](../internal/capture/transcript.go)) → cấu trúc không thể double-count.
- Context phân tầng Company→Team→Agent chạy thật ([internal/contexthub/merge.go:46-96](../internal/contexthub/merge.go)), version-immutable, có provenance (`context_injected` ghi rõ version mỗi tầng được merge).
- Human work + agent run **cùng một schema** `work_items` ([internal/learn/metrics_calc.go:54-88](../internal/learn/metrics_calc.go)) — điều kiện để so người vs agent cùng thước đo.

**Nông:**
- **Cost không đối soát usage thật của OpenRouter.** Cost tính từ transcript local (token model tự báo × pricing config); OpenRouter usage thật có thể khác → cost là **ước lượng, chưa phải ground-truth**. Mức độ: **QUAN TRỌNG** (cost attribution là một trụ).

### ② GOVERN (~90%, mạnh nhất)

**Thật:**
- Guardrail chặn tại tool-call, không bypass ([internal/govern/engine.go:51-72](../internal/govern/engine.go)): chain kill → sandbox → block → budget → gate, first-hit-wins.
- Kill switch giết process thật cho run console (v6) qua process-group signal ([internal/runner/kill.go:31-57](../internal/runner/kill.go)); run hook/wrap thì mark DB (không có process handle).
- Sandbox (giới hạn write-scope), budget (hard stop theo scope), permission gate (approval loop poll DB / Slack reaction) — đều thật.
- Audit hash-chain tamper-evident ([internal/govern/audit.go](../internal/govern/audit.go)): `hash = sha256(prev || ts || actor || action || subject || detail)`, `dandori audit verify` walk & validate.
- Closed-loop tự động ([internal/govern/closed_loop.go:34-61](../internal/govern/closed_loop.go)): F→auto-demote supervised + flag + Jira; D→flag + Jira + demote proposal (chờ người duyệt); recovery→auto-resolve.

**Gap quan trọng nhất (chính vision đánh dấu):**
- Điều khiển là **pre-call, không phải in-call.** Agent bắt đầu tool-call ở 99/100 budget thì call đó vẫn chạy xong (overshoot); kill chặn ở *tool-call kế tiếp*, không cắt được tool đang chạy. Không có tín hiệu pause giữa tool. Đủ tốt cho "chặn trước khi hại", chưa đạt "pause giữa chừng" như vision nói. Mức độ: **TRUNG BÌNH**.
- **Quality gate hiện advisory (chỉ flag), chưa block.** Gate chạy *sau* finalize nên không chặn được run dưới ngưỡng — yếu hơn lời hứa "chặn run dưới ngưỡng" của vision. Mức độ: **QUAN TRỌNG**.

### ③ LEARN (~80%, yếu nhất nhưng vẫn thật)

**Thật:**
- 4 chỉ số tính từ event thật + có provenance ([internal/learn/metrics.go:12-19](../internal/learn/metrics.go)): Acceptance, Success, Autonomy, Reliability.
- Grade calibrate theo phân vị fleet ([internal/learn/grade.go:23-58](../internal/learn/grade.go)) — A≥p80…F<p20, fleet<5 fallback static. Không magic number.
- ROI truy từng đô lãng phí về run_id ([internal/learn/roi.go:26-75](../internal/learn/roi.go)): failed/killed 100% phí + open-flag 100% phí + clean×(1−acceptance%).
- Leaderboard toàn fleet + human baseline (ẩn danh, opt-in).

**Nông:**
- **Acceptance phụ thuộc git** — revert phát hiện qua `git log --grep`; repo không-git (notebook, Terraform, DB migration) → không phát hiện revert → chỉ số méo. Mức độ: **QUAN TRỌNG**.
- **Agent read-heavy/test trông "hoàn hảo giả"** — acceptance chỉ đo rejection của Edit/Write/NotebookEdit; agent không sửa file → neutral 100. Mức độ: **TRUNG BÌNH**.
- **ROI chưa tính time-cost** ("agent 2h vs người 10h") — mà đây chính là lõi câu chuyện x100. Mức độ: **QUAN TRỌNG**.
- Reliability là trung bình cộng không trọng số — 1 kill thảm hoạ = nhiều lỗi nhỏ. Mức độ: **NICE-TO-HAVE**.

## Cái THIẾU chặn adopt thật — xếp theo mức độ

### Tier 1 — Blocker (không ship được nếu thiếu)

1. **Không có auth/identity.** Web console single-principal localhost; `execActor()` trả `UserName + "@console"` cứng ([internal/web/handlers_exec.go](../internal/web/handlers_exec.go)); originGuard chỉ bind localhost ([internal/web/server.go:58-79](../internal/web/server.go)). Mọi phê duyệt đều `@console` — audit không biết *ai* quyết. **Mâu thuẫn trực tiếp với lời hứa GOVERN** ("separation of duties", "compliance hỏi 6 tháng sau có câu trả lời").
2. **Central-mode dùng 1 token chung** ([internal/ingest/server.go:52-83](../internal/ingest/server.go)). `X-Dandori-Principal` là header client tự khai, không verify → bất kỳ máy nào có token đều mạo danh máy khác. Multi-máy thật không an toàn.
3. **Không RBAC** — ai cũng kill được run của ai, promote context của ai. Trước khi mở "các nút ghi" cho một đội phải có phân quyền.
4. **Data at rest không mã hoá** — mọi prompt/code lưu SQLite trần. Với org bị quản lý (compliance) là vấn đề.

### Tier 2 — Quan trọng (ship đội đầu rồi vá)

5. Acceptance cho non-git · 6. Đối soát cost OpenRouter thật · 7. Quality gate blocking · 8. Time-cost trong ROI.

### Tier 3 — Nice-to-have (backlog)

9. Cursor/Codex adapters (C2, đang [Sau]) · 10. Confluence write-back · 11. Knowledge marketplace · 12. Sliding-window budget.

## 3 câu hỏi mở của vision — đã trả lời

Từ [03-features.md:255-257](03-features.md):

| Câu hỏi | Trả lời | Trạng thái |
|---|---|---|
| (a) Độ sâu điều khiển runtime | Chặn pre-tool thật (kill/budget/sandbox/gate); **không** cắt được tool đang chạy | Gap trung bình |
| (b) Quyền ghi lên service | v7 làm xong: jira-transition, pr-review, calendar, sheets, drive-import — đều qua duyệt | ✅ Hoàn thành |
| (c) RBAC / multi-user | **Chưa có gì** — single-principal localhost, 1 token chung | **Blocker** |

## Verdict giao vision

| Trụ | Đạt | Điểm | Bằng chứng |
|---|---|---|---|
| ① CAPTURE | ~85% | A− | Hook thật (fail-open), cost dedup, context đa tầng, unified schema. Thiếu: ground-truth OpenRouter |
| ② GOVERN | ~90% | A− | Guard tại tool-call không bypass, kill/sandbox/budget/gate/audit thật. Thiếu: pre-call không in-call |
| ③ LEARN | ~80% | B+ | 4 metric + provenance, grade calibrate, ROI truy phí. Thiếu: acceptance non-git, time-cost ROI |

**Với 1 đội / 1 máy:** giao đủ cả 3 trụ end-to-end, metric có provenance đáng tin, closed-loop phản xạ thật, guardrail chặn cái nguy hiểm thật.

**Với quy mô tổ chức:** central-mode có nhưng thiếu RBAC (1 token = không an toàn), không truy được *ai* quyết, không cô lập theo team. **Chưa ship được** cho một đội trước khi có auth.

## Điểm mấu chốt & đề xuất

Cái thiếu lớn nhất **không phải feature** — 13 feature vision đã gần đủ. Cái thiếu là **tầng danh tính & phân quyền**. Vision bán "hạ tầng dùng chung của tổ chức" nhưng ở khía cạnh auth code vẫn là "công cụ cá nhân". Gợi ý sẵn trong vision (**Google SSO + Directory** của GWS) là đường đi đúng — và `gws` auth đã chạy thật trong repo, nên là mảnh ghép khả thi nhất.

**Đề xuất v8 — Identity & RBAC:** Google SSO login · principal thật gắn vào mọi audit entry (thay `@console`) · per-operator token cho central-mode · role gate cho các nút ghi (kill/demote/override). Đây là thứ biến Dandori từ "PoC chứng minh vision khả thi" thành "sản phẩm một tổ chức dám giao quyền".

## Câu hỏi chưa giải quyết

- v8 làm RBAC đầy đủ ngay, hay chỉ identity (login + principal vào audit) trước rồi RBAC sau?
- Ground-truth cost: đợi OpenRouter webhook, hay periodic fetch usage API?
- Quality gate blocking: chặn *finalize* hay chặn *merge/deploy* (đúng chỗ CI/CD)?
- Time-cost ROI cần baseline "người làm mất bao lâu" — lấy từ đâu (Jira estimate? lịch sử?)?
