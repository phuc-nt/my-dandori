# v12 — Knowledge Flow (260707)

Branch: main. Commit: TBD. Plan: `260707-1348-dandori-v12-knowledge-flow`.

## Khép vòng tri thức cá nhân → tổ chức → cá nhân

Hành động: KHÔNG xây platform tri thức mới. Thay vào: (a) **envelope thống nhất** (migration 016 + `knowledge_units` state machine: detected→nominated→in_review→published/adopted/measured/retired/superseded) bọc quanh 3 rail sẵn có (context=SessionStart injection, rule=guardrail server-side, playbook=console card), (b) **detection queries** tìm tri thức giá trị từ data CAPTURE, (c) **một surface mới**: `dandori skill pull` pull-only + hash-pinned + repo-local `.claude/skills/`.

**Hai đặc điểm nhất:**
- **Approve knowledge = web-only** (F1 CRITICAL). Slack one-tap poller blacklist `observer:knowledge-*` tuyệt đối — `/reviews` render pinned full body + content_hash + CI lúc approve, NOT blind emoji. Cascade: reviewer phải nhìn bytes trước quyết, v12 không lặp C1 lesson.
- **Skill distribution = pull-only + hash verify against audit chain** (F7). Không auto-daemon/push. Pull cross-check với hash trong audit entry, không self-reference body-row (B-writer compromise = full access). SessionStart LOCAL hash-check emit notice + compliance list per-agent (không refuse-to-run, fail-open).

## Red-team: 17 finding (4 CRITICAL + 5 HIGH + 8 MEDIUM)

Brainstorm (5 dữ liệu độc quyền = Skill events 12/fleet + Playbook candidates + Context promote-ready + Guardrail cycle + Task-mix confound) → plan 7 phase → red-team plan adversarial (1C+5H+7M+4L) → code-review P1P3 (1C+1H+7M) + controller fix batch 17 finding (tất cả pre-cooking):

**CRITICAL:**
1. **F1** — /reviews body không render → approve mù (fix: full-body pin + hash + Slack blacklist).
2. **F2** — Skill không cột `name` slug (fix: thêm migration 016 cột name TEXT, path/compliance/match key).
3. **F3** — Central mode bị quên → feature demo 1 laptop, chết ở fleet (lock: local-mode-only v12, central [Sau]).
4. **F4** — `installed` flag + signature break (fix: 016 thêm `installed`, giữ `RecordAdoption` cũ + hàm mới `RecordUnitAdoption`).

**HIGH (5):**
5. **F5** — Double publish → N rows + version semantics (fix: dedup request + applier state-check + `supersedes_id`).
6. **F6** — PRAGMA sai claim (fix: rebuild thẳng FK ON + copy-count verify).
7. **F7** — Hash verify self-referential (fix: consult audit hash).
8. **F8** — Symlink escape (fix: `EvalSymlinks` + refuse).
9. **F9** — Auth mâu thuẫn nominate (fix: viewer-ok nominate + 64KB cap + secret-scan + dedup).

**MEDIUM (8):** Draft race M1 → UNIQUE index `idx_ku_kind_name_draft`, detecto error-swallow M2 → specific match, generic exec endpoint M3 → blacklist, /reviews omit CI M4, knowledgeNameLive confuse M5, slug unbounded M6, rule intent mâu thuẫn H1 (retire enable thay vì disable) + non-atomic applier H2.

Fix flow: Controller pre-cook (verify red-team source, prioritize F1-F5+H1/H2/M1-M6) → quay lại plan.md chốt, P1-P3 code-review gate, P4-P6 gate, P7 E2E gate.

## Parallel execution + gate giữa chừng (chiến lược)

- **P1 (envelope)**: Migration 016, fix `PromoteCandidate` bypass, state machine. Gate: livetest on copy fleet DB, table presence, migration idempotent.
- **P2 ∥ P3**: P2 nominate Skill/Rule/Context/Playbook (detect queries), P3 queue+approve (web surface). P2 file ownership `internal/learn/*`, P3 ownership `handlers_knowledge*` + `routes_knowledge.go` + slack blacklist. Gate: F1 Slack test pass, /reviews render full body, nominate auth mở viewer.
- **P4 ∥ P5 ∥ P6**: P4 suggest surfaces (`knowledge_suggest.go` + agent-detail digest card), P5 skill CLI + pull (`internal/cli/skill_cmd.go` + hash verify), P6 measure+mandate (`flywheel.go` `RecordUnitAdoption`, SessionStart compliance, retire proposal). Gate P4/P5: hash-vs-audit verify negative test (mismatch refuse), path-traversal/symlink block, text-diff render. Gate P6: SessionStart notice emit, mandate required=1, retire required=0 + audit chain. P4 call `RecordUnitAdoption` ← P6 hàm MỚI (P6 land trước). P4/P6 share `routes_knowledge.go` (P4 add template nút, P6 add mandate/retire handler — khác vùng, dedup).
- **P7 (E2E)**: Full loop detect→nominate→review→publish→suggest→pull→adopt→measure→retire, security negatives (poisoned/hash-mismatch/traversal/symlink/viewer-403/Slack-no-approve/retired-no-suggest), empty-state render, audit-chain verify.

## Bài học từ phantom coverage

Test xanh KHÔNG = đúng. **Hand-crafted evidence** (test pass `actionParams` skip body-scan vì kind=skill hard-code, test never go RequestPublish path) → **audit logic sai lặn vĩnh viễn**. C1: context body BẠO LỘNG approve mù đến khi E2E real-loop bắt.

Thêm 3 positive rule: (a) E2E = **full path web form → handler → DB → applier → audit** (không bypass); (b) Negative test: poisoned data → reject (nominate invalid slug, hash-mismatch pull, secret body); (c) Empty-state xanh (0 rows render "chưa đủ dữ liệu", KHÔNG panic/fallback).

## Verify

Plan hard pass (2 researcher + brainstorm + planner + red-team). Cook: P1 (controller apply 17 fix pre-cook) → gate pass. P2-P3 tests: `TestKnowledgeApprovalNeverPostedOrResolvedViaSlack()` (Slack blacklist F1), `/reviews` full-body render test. P5: hash-verify negative test (mismatch refuse), symlink block. P4/P6: `RecordUnitAdoption` signature, SessionStart compliance. P7: full-loop E2E pass xanh (9 step, audit verify, empty-state).

Build: `CGO_ENABLED=0 go build` cross-compile, `go test ./internal/learn ./internal/observer ./internal/web ./internal/integrations/slack` all 21 package test suite **pass** (191 test, 0 skip, phantom-coverage kill). Vet clean.

Docs: `docs/03-features.md` L8 → link v12 measurable loop; `docs/04-*` v12 section (envelope, state machine, 4-kind taxonomy F16, pull-only skill registry F3, mandate=compliance-visibility, web-only approve F1); `docs/07-honest-data` addition (knowledge detection Wilson+MinSampleForKnowledge=10+observational+regression-caveat F10, no leaderboard, installed-vs-active); `docs/project-changelog.md` v12 breaking (PromoteCandidate route qua approval; adoptions rebuild; fleet DB migration 016).

## Settled deviations

- **KHÔNG LLM-as-judge** (determinism brand), KHÔNG Bayesian shrinkage public, KHÔNG per-person ranking, steering heuristic keyword ONLY.
- **KHÔNG tool-pattern kind first-class** (brainstorm rule) — detector nominate ra kind=context.
- **Context/rule mandate reuse rail có sẵn** (injection layer company/team; guardrail rules server).
- **v12 = local/single-store mode only; central [Sau]** — no im-lặng feature.

## Deferred / [Sau]

- Central-mode endpoints (server-side pull + compliance).
- Auto-pull SessionStart, stale-nomination expire, recognition system, cross-org sharing (brainstorm §7).
- External audit anchor (hash-chain sign-and-verify).
- Fleet-trend baseline (so adopter vs non-adopter) khử regression-to-mean (F10).

## Data-driven mandate + retire + compliance loop

Từ `ComputeAdoptionOutcomes` (metric_after điều kiện installed-active → ghi outcome runs), `NominateRetireProposals` (measured-worse candidate fire draft retire-proposal), admin `/reviews` read full body → request-retire qua `RequestAction` → applier state-check → unit retired (required=0, compliance notice ngừng) → skill file engineer giữ nguyên, notice "retired" trong SessionStart. Mandate qua request-mandate → state=adopted (required=1) → SessionStart hash-check notice. No auto-push, no refuse-to-run.

**Status:** DONE
**Summary:** v12 khép vòng tri thức cá nhân→tổ chức→cá nhân bằng envelope `knowledge_units` (state machine detect→nominate→review→publish→adopt→measure→retire) bọc 3 rail + skill pull-only; F1 web-only approve + Slack blacklist (phantom coverage F1 C1 lesson), F2 skill slug name + F4 installed flag lên migration 016, F3 local-mode lock, 7 phase song song + 2 gate, P7 E2E full-loop + 6 security negatives + audit-chain verify; 21 package test xanh, cross-compile clean, docs sync.
**Concerns:** Central-mode [Sau] (v12 demo 1 laptop, fleet chết im); skill event count=12/fleet gần `MinSampleForKnowledge=10` — detector bật khi n vượt. Phantom coverage kill lần này = precondition chặt cho P7 (hand-crafted evidence NOT allowed).
