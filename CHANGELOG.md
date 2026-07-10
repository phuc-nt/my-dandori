# Changelog

Mọi thay đổi đáng kể của Dandori. Định dạng theo [Keep a Changelog](https://keepachangelog.com/);
mỗi mốc là một "version" nội bộ (một sprint có kế hoạch + red-team + review). Ngày theo `YYMMDD`.

## [v15] — Central-mode Parity — 260710

Đóng nhóm gap central-mode lặp qua v12/v13/v14 (governance + distribution parity), qua red-team 3 reviewer (25 finding — plan gốc bị chứng minh không implementable → rework toàn bộ) + code-review từng phase (một RCE thật bị bắt + vá ở phase distribution).

- **Audit anchor** (`internal/govern/audit_*.go`): Ed25519 co-sign mỗi audit row + **signed checkpoint đẩy ra ngoài box** (git-commit `docs/audit-checkpoints/` + offsite optional) làm trust-root. `chainHash` canonicalize (length-prefix, chống delimiter-shift). Monotonic signing (chống rebuild-unsigned). `Verify` reason-aware (chain/signature/truncated). Chống: sửa-1-row, rebuild-toàn-bộ, xoá-tail-rows.
- **Central audit cho ingested runs** (`internal/ingest/guardrail_audit.go`): central run tạo audit_log row **co-signed, atomic trong batch tx** (`AppendTx`, không deadlock 1-conn pool). Anti-spoof (run-owner check), anti-suppression (server-derived content-hash dedup, không client ULID), action từ Evaluate. Detector flag-only.
- **G5 risk-score central** (`policy_snapshot.go`): server tính per-run, snapshot **scoped per-operator** (không rò fleet data), read pool + server cache. Client-attested denial zero-weight (chống poison).
- **G3 budget central**: vượt trần → **Ask** (central active-run luôn NULL model), per-scope (agent/project). `budget.mode: hard` giữ hard-stop.
- **Skill/kit central distribution** (`internal/ingest/skill_kit_*.go`): pull qua network, verify `sha256(bytes nhận) == approve-hash` + **per-unit Ed25519 signature over (unit_id, approve_hash)** — chống RCE (server độc serve bytes tráo). Giữ deny-list + symlink-safe + per-file hash.
- **Fleet compliance export**: gồm central runs + signatures + checkpoint; coverage = decision-event-thiếu-audit (không flag oan clean run); pubkey **fingerprint out-of-band** là trust-root; disclose chain-order≠time-order + client-attested caveat.
- **Threat model honest**: trên single-box (key + DB cùng máy) anchor không chống insider-có-key — documented, không overclaim.

## [v14] — GOVERN Hardening — 260708

Bốn nâng cấp guardrail nhặt từ so sánh với Omnigent (meta-harness), qua red-team 3 reviewer (10 finding, 4 Critical) trước khi code.

- **Fail-closed contract** (`internal/govern/contract.go`): một nguồn sự thật cho hành vi mọi check khi không eval được. Vá 4 nhánh fail-open trên đường hook local (DB lỗi từng lặng lẽ tắt sạch guardrail). Break-glass env `DANDORI_GOVERN_FAIL_OPEN=1` để DB hỏng không brick máy. Seed rule chặn agent đụng `~/.dandori/` (tự tắt guardrail).
- **Secret/PII guardrail (G1.5)**: secret strict-set → deny (không echo giá trị); PII (thẻ Luhn / ≥5 email) → gate. `redact` thành superset đóng lỗ rò PII vào approvals/Slack; approvals unique index đóng TOCTOU. Replay fleet thật: **0/6520 false-positive**. Chạy độc lập G1/G2.
- **Budget downgrade-gate (G3)**: vượt trần → chỉ deny run model đắt (gợi ý `/model`), model rẻ chạy tiếp; `budget.mode: hard` giữ hard-stop cũ. Chống né qua NULL-model (agent-history fallback + cap/tháng). Default `["opus","fable"]` chốt từ pricing thật.
- **Risk-score guardrail (G5)**: điểm rủi ro cửa sổ trượt, self-exclusion qua `audit_log.action` (không ratchet tự khuếch đại). Mặc định **log-only**; gate opt-in sau calibrate. Badge run-detail.

## [v13] — Kit & Mining — 260707

- **Mining queue** (`/knowledge/mining`): 4 tín hiệu SQL on-demand (corrective-steering-rồi-done, fail→retry→success, guardrail-block-rồi-done, cost-outlier) surface "run đáng đọc". Không phải leaderboard, dismiss chỉ ẩn khỏi danh sách đọc.
- **Import** (`dandori knowledge import`): nâng memory/journal `.md` thành context unit, preview + khử trùng lặp per-file.
- **AI-draft**: soạn nháp practice từ bằng chứng DB đã che secret (không đọc raw transcript), form sửa được, nhãn `origin=ai-draft` suốt vòng đời, single-flight chống spam budget, fail-open.
- **Agent-kit** (`kind=kit`): đóng gói trọn bộ `.claude/` chuẩn với manifest Merkle-lite; `dandori kit pull` verify hash 3 lớp against audit chain, path-safety variable-depth symlink-safe, deny-list-first (`hooks/scripts/settings` không bao giờ distribute).
- Cột `origin` truy nguồn tri thức; provenance run-id bị forge → từ chối.

## [v12] — Knowledge Flow — 260707

- Vòng tri thức cá nhân→tổ chức→cá nhân: bảng `knowledge_units` (state machine, 4 kind), detector nominate-only (Wilson CI, ≥10 mẫu), review **web-only + full-body render** (chống duyệt-mù qua Slack emoji), publish qua approval, skill registry pull-only hash-pinned, mandate = compliance-visibility, đo adoption installed-vs-active với caveat hồi-quy-về-trung-bình.

## [v11] — Insight Expansion — 260707

- `/insights` mở rộng: Wilson CI mọi nơi (thay badge im lặng), context-version ROI, shadow-work, guardrail effectiveness ledger, time-horizon curve, Pareto spend, steering economics, approval funnel. Descriptive-only, per-section degrade, không faked-zero.

## [v10] — Identity & RBAC — 260707

- Đăng nhập console (argon2id, session SQLite), principal thật thay `@console` trong mọi audit entry, per-operator ingest token (bỏ header tự-khai spoofable), 2 role admin/viewer gate 29 write route. Disable admin cuối → khoá console (không rơi về no-auth).

## [v9] — Capture-gap & Dense Insight — 260706

- Vá gốc capture (watcher end-time, task_key linkage, git-delta, steering text) rồi ship insight trên data đã dense. Phân biệt honest-zero vs capture-gap.

## [v8] — Onboarding & Executive UX — 260705

- Wizard `/welcome`, credential UI `/settings/integrations` + healthz, thông báo chủ động Slack, trang `/risk`, ước lượng tác động trên approval card.

## [v7] — Write-back & Vision Closeout — 260704

- Ghi ra Jira/GitHub/Calendar/Sheets/Gmail/Drive (không chỉ đọc) — mọi write qua RequestAction → duyệt → applier. Post-action check, gate threshold UI, agent assignment, saved views, wallboard.

## [v6] — Commander — 260704

- Phóng & điều khiển agent-run trực tiếp từ console (launch/kill/retry/bulk), chống RCE (không shell, binary + cwd allowlist), kill giết process thật, live log poll.

## [v5] — Context Hub — 260703

- Context phân tầng Company→Team→Agent, version bất biến + diff + rollback, promote approval-gated, effective-context preview, tiêm qua SessionStart.

## [v2–v4] — 260702

- v2: vượt legacy (multi-runtime adapter, run comparison, cost-spike explain, revert detector). v3: đóng vòng vision (closed loop, trend, knowledge capture, rule builder, policy simulator). v4: executive layer (compliance export, escalation).

## [MVP] — Harness Console — 260702

- Ba trụ nền: CAPTURE (auto-capture qua hook, cost attribution, unified schema, watcher) · GOVERN (block/sandbox/budget/gate/kill, quality gate, approval workflow, audit hash-chain) · LEARN (grade A–F calibrate fleet, ROI, provenance, leaderboard). Console 3 tầng + CLI song song.
