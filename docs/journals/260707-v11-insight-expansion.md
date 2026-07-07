# v11 — Insight expansion (260707)

Commit TBD. Plan `260707-0832-dandori-v11-insight-expansion`.

## Expansion strategy

Không overhaul công thức grade (v10 chờ dữ liệu dày). Thay vào: 8 phân tích mô tả (descriptive-only) trên data Dandori đã capture. Hành động: mở tầng LEARN — đo ROI sửa CLAUDE.md/rules (duy nhất trong ngành, natural experiment từ schema hôm nay); honesty infrastructure (Wilson CI thay vì silent badges); shadow-work + guardrail cross-schema; steering economics. **Ranh giới cứng:** KHÔNG gì chảy vào grade composite.

## Signature: Context-Version ROI

Nối `context_injected(version)` × outcome → so done-rate/cost-per-done/steering trước/sau version bump per layer (company/team/agent). Trả lời "vừa sửa rules — có đáng không?" bằng SQL khó giả mạo, drill-to-raw run IDs. Dedup(run, first-injection) bắt buộc vì resume/mid-run-bump sinh nhiều event; dùng `MIN(id)` per run để chọn version ĐẦU. Live hôm nay: 0 rows context_injected (cơ chế xây từ v5 nhưng fleet chưa cấu hình) → empty-state bắt buộc, chờ Context Hub v12.

## Phát hiện đỏ (red-team vs live DB, 40 runs, 36 done/0 failed)

**FOUR CRITICAL — tất cả bị lỗi query, đều sẽ in số nói dối:**

1. **Guardrail ledger: token ≠ rule id.** `[dandori GN]` là CHECK CLASS (G2=sandbox, G3=budget, G4=gate), KHÔNG rule number. Live 15 blocks: G2×11 (4 runs) + G3×3 (1 run) + G1×1. Naive join `guardrail_rules BY id` mislabel tất cả budget blocks thành rule #3 ("DROP TABLE"). **Fix:** parse `\(rule #(\d+)\)` suffix → per-rule row, join; token GN → class-level row (sandbox/budget/gate/regex), KHÔNG BAO GIỜ join guardrail_rules. Kiến trúc hai tầng, F1.

2. **Approval latency: 9/9 "decided" là expiry sweep, không human decision.** Live 10 approvals: 9 expired + 1 pending, 0 approved/rejected. Gate.go:117 set `decided_at=Now()` khi expire sweep. Báo cái đó là "latency người" = 100% artifact. **Fix:** latency chỉ tính `status IN ('approved','rejected')`. Hiện empty-state (0 human decision), F2 honest.

3. **Shadow-work timestamps: Z vs +0700-no-colon → SQLite julianday NULL.** `work_items.updated_at` = 50/50 RFC3339-Zulu vs non-colon offset (2026-06-21T22:51:17.581+0700). SQLite `julianday('...+0700')` trả NULL → row rớt âm thầm, so timespan bị gap 7h. THÊM: PO done-transition LUÔN sau run-end → mọi correctness-done-task false-flag shadow-work. **Fix:** parse multi-layout trong Go (RFC3339 + RFC3339Nano + `-0700` colon-less), so time.Time; loại item có post-run update chỉ là done-transition. Coverage <1% (1/40 có task_key) → empty-state + honesty label "chưa đủ mẫu", F3.

4. **Steering numerator: user_msg trên live DB bị meta-inflation.** Premise: steering chỉ-đếm. Reality: 552 steering_msg text events live (29 runs, local mode), 3629 user_msg counted (36 runs). Audit tay (F6): MidRunMsgs=320/run khả nghi — 51% non-human (slash-command echoes, IDE file-opens, interrupt markers trên run nặng). **Fix:** canonical numerator local = COUNT(steering_msg), central = user_msg fallback (do central từ ingest không có text). P2 steering dùng user_msg (cũ), P5 audit manual, controller align P2 xuống steering_msg canonical. Ghi rõ cấp độ, gán nhãn per-mode. F6 honest.

**PLUS F7:** 36/36 run=done, 0 failed/killed → outcome contrast degenerate 100% (horizon bucket, steering vs no-steering, ledger→done/→killed). KHÔNG "chỉ hôm nay chịu khó 100%, chứng tỏ bọn nó mau". UI: "chưa có contrast" thay vì 0%-vs-100%, cấm theater.

## Parallel execution + cross-phase conflict

4 devs P2–P5 song song (P1 chặn mềm, rồi disjoint files). Hai vấn đề thực tế:

- **(a) Steering numerator:** P2 viết `user_msg` (scope cũ), P5 audit discover data reality, manual count chứng minh canonical = `steering_msg`. Controller align P2 xuống match P5, retest. Không merge conflict git, nhưng data inconsistency ngầm logic.
- **(b) seedApproval collision:** P5 test-helper `seedApproval` tồn tại → rename thành `seedApprovalRow`, merge xanh.

Both resolved by controller pre-merge, không block long.

## Honesty infrastructure

- **Wilson CI everywhere:** `FormatWilson(low, mid, high float64)` (P1 foundational) — thay vì silent "n<3" badge, CI width lên/xuống ngoài mẫu nhỏ. Áp lên leaderboard + metrics + cost_per_outcome (S7 full).
- **Insufficient() bool per struct:** mọi phân tích (context-ROI, shadow-work, guardrail, horizon, Pareto, steering, approval) có `Insufficient()` → UI render "chưa đủ mẫu" thay vì số 0 giả. MinSampleForInsight=3.
- **Per-section degrade:** query 1 section lỗi (SQL error, nil pointer) → section render "không tải được", KHÔNG 500 cả trang. Đổi pattern fail-fast hiện tại (F13).
- **Drill-to-raw:** mọi phân tích kèm list run IDs/event IDs, queryable (`?layer=company&target=agent-1&version=3`), provenance complete.

## Verify

Kế hoạch --hard pass (2 researcher + brainstorm + planner + red-team 4 Critical/4 High/5 Medium/2 Low + 15 fix applied). Implementation: 2 code-review rounds — P1 (wilson.go + insightWindowClauseCol) clean; P2–P5 merged (parallel, cross-phase resolve steering) ship-ready sau fix. Go test ./internal/learn/... + vet + CGO_ENABLED=0 build xanh. E2E ground-truth-vs-SQL per section (seed fixture vs same SQL on in-mem) caught 1 real bug (dedupe logic).

Docs: `/insights` page mở mới sections (context-ROI card, shadow-work, guardrail ledger, time-horizon, Pareto, steering-econ, approval-funnel); templates HTMX fragment + server render (không SPA); `/dashboards` (F9) leaderboard/metrics dùng Wilson format. docs/04 (feature summary) + docs/07 (LEARN principle) bổ sung v11 ngắn.

## Deferred / [Sau]

- **Real steering timestamp:** steering_msg.ts hiện sync-artifact (delete+reinsert cùng timestamp) → wall-clock timeline bỏ; v11 dùng sequence-index (ORDER BY id). Optional capture step (P5) bật parseTS thật unlock v12.
- **Shadow-work finish-vs-review:** schema KHÔNG changelog → không phân biệt "người làm nốt" vs "PO review click Done". v11 label "activity sau agent done — CÓ review"; sắc hơn cần workshop (5 ví dụ thật) → v12.
- **Kaplan-Meier code-survival:** cần ≥30 run/cohort (hôm nay 40 total, chỉ 1 done-state interesting). Defer.
- **Context-ROI thật:** chờ fleet cấu hình Context Hub + new context_injected rows (hôm nay 0). Chạy infrastructure xanh, chỉ empty-state ship v11.

## Locked deviations

KHÔNG LLM-as-judge (phá determinism/provenance brand — rejected). KHÔNG Bayesian shrinkage công khai (Wilson CI width). KHÔNG per-person ranking steering/latency (Goodhart); human baseline ẩn danh, chỉ fleet/action-type. Steering taxonomy heuristic keyword ONLY (KHÔNG NLP/LLM).

**Status:** DONE
**Summary:** 8 phân tích mô tả (context-ROI signature, shadow-work, guardrail ledger, time-horizon, Pareto, steering, approval, Wilson CI) đóng tầng LEARN; 4 Critical red-team kill, all fixed; parallel P2–P5 (1 cross-phase align); per-section degrade + drill-to-raw honesty.
**Concerns:** Context-ROI ship empty-state (0 row hôm nay); shadow-work coverage 1/40 (setup gap task_key). Steering numerator audit F6 discovery, không phải capture bug — thực tế data, sửa logic canonical local-side.
