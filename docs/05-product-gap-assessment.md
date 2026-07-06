# Đánh giá khoảng trống sản phẩm — Dandori vs Vision

> Rà soát từ vision gốc ([01-product-vision.md](01-product-vision.md)) đối chiếu với code thật (2026-07-04).
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

1. ✅ **CLOSED bởi v10 (260707).** Web console giờ có login local (username+password, argon2id) — mọi audit entry ghi principal thật (`<username>` hoặc `slack:<id>`), không còn `@console` cứng ngoài fallback local-trust khi chưa từng tạo account nào. Xem [04-implementation-notes.md § v10](04-implementation-notes.md). Lưu ý: v10 chọn **local account**, không phải Google SSO như đề xuất ban đầu bên dưới (SSO vẫn [Sau]) — quyết định đổi hướng vì SSO kéo thêm dep/flow phức tạp hơn mức tối thiểu cần để đóng blocker.
2. ✅ **CLOSED bởi v10.** Central-mode giờ có **per-operator ingest token** (`dandori token create <username>`, SHA-256 hash lookup) — mỗi máy có token riêng, server tự derive operator từ token, không tin header client-supplied. Token chung cũ (legacy) vẫn được chấp nhận song song trong giai đoạn chuyển đổi (`allow_legacy_ingest_token`, mặc định `true`) nhưng luôn attribute về hằng số `legacy-shared@ingest`, không mạo danh được máy khác qua header. Ngày tắt hẳn legacy chưa chốt (dự kiến v11).
3. ✅ **CLOSED bởi v10.** 2 role `admin`/`viewer` gate 29/38 route POST ghi (kill/demote/override/launch/budget/...), default-deny cho route mới chưa phân loại. Full granular RBAC (theo resource/scope, không chỉ 2 role toàn cục) vẫn [Sau].
4. **Data at rest không mã hoá** — mọi prompt/code lưu SQLite trần. Với org bị quản lý (compliance) là vấn đề. **Còn mở** — v10 không đụng tới (ngoài scope: v10 = identity/RBAC, không phải encryption-at-rest).

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
| (c) RBAC / multi-user | v10 (260707) đóng: login local + principal thật vào audit + per-operator token + 2 role admin/viewer trên 29 route ghi. Full granular RBAC + SSO vẫn [Sau] | ✅ Đóng (tối thiểu) — xem [04](04-implementation-notes.md) |

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

**Identity & RBAC — ĐÃ SHIP ở v10 (260707)** *(v8 rẽ sang onboarding/executive UX, v9 sang capture-gap trước khi tới lượt identity; metric overhaul lùi thành v11 — xem [04](04-implementation-notes.md))*: local login (không phải Google SSO như đề xuất ban đầu — đổi hướng vì SSO nặng hơn mức tối thiểu cần) · principal thật gắn vào mọi audit entry (thay `@console`) · per-operator token cho central-mode · role gate cho các nút ghi (kill/demote/override). Đây là thứ biến Dandori từ "PoC chứng minh vision khả thi" thành "sản phẩm một tổ chức dám giao quyền" — 3/4 Tier-1 blocker đã đóng, còn lại data-at-rest encryption.

## Câu hỏi chưa giải quyết

- ~~v10 làm RBAC đầy đủ ngay, hay chỉ identity trước rồi RBAC sau?~~ Trả lời: v10 làm cả hai ở mức tối thiểu (identity + 2-role gate) trong cùng plan; full granular RBAC + SSO để [Sau].
- Ground-truth cost: đợi OpenRouter webhook, hay periodic fetch usage API?
- Quality gate blocking: chặn *finalize* hay chặn *merge/deploy* (đúng chỗ CI/CD)?
- Time-cost ROI cần baseline "người làm mất bao lâu" — lấy từ đâu (Jira estimate? lịch sử?)?
