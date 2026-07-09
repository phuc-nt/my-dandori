# v14 — GOVERN Hardening (260709)

Branch: feat/v14-govern-hardening. Commit: 5d4168e. Plan: `260708-2156-dandori-v14-govern-hardening`.

## Adopt 4 Omnigent ideas without breaking light/pure-Go/single-binary constraint

**Hành động:** red-team against omnigent-ai/omnigent comparison (plans/reports/comparison-260708-2147-omnigent-vs-my-dandori-report.md) yielded 4 mature GOVERN patterns worth integrating: (1) fail-closed contract as source-of-truth, (2) strict PII detection + Luhn validation, (3) budget downgrade-gate (deny expensive, downgrade to cheap), (4) risk-score sliding window + self-exclusion. None required Python, PostgreSQL, or CGO — all folded into single Go binary, pure-SQLite internal schema.

**Key différence:** Omnigent has strong GOVERN runtime but NO LEARN pillar and no hash-chain audit. Dandori's moat: LEARN (mining/kit/import from v13) + audit hash-chain (audit_log with immutable kind discriminator). GOVERN hardening closes the 4 Omnigent gaps without surrendering that moat.

## Red-team: 24 findings → 10 unique (4 Critical)

Adversarial review by 3 independent reviewers before any code. All 10 folded into plan before touching internal/govern/. Output: plans/reports/red-team-260708-0330-v14-govern-findings-red-team-report.md (4 Critical, 6 High).

**Pattern:** each finding independently verified file:line. 4 Critical all shared the thread: default-on guardrails risk re-triggering exact pain that made user disable G1/G2 earlier (user had disabled guardrails for obstructing workflow on real runs). Through-line mitigation: **replay/calibrate against REAL fleet data before defaulting on, plus config escape hatches everywhere.**

## Four hardening phases + sharpest red-team insight

### Phase 1: Fail-closed contract (internal/govern/contract.go)

Systematized fail-closed/open into one source-of-truth map. **Critical finding:** local hook path had 4 fail-open holes:
- openStore error (SQLite open fails → no deny layer)
- ing.PreTool coupling verdict to capture (capture error → guardrail verdict NULL → pass)
- input-decode (malformed JSON → no verdict → pass)
- config-load (config parse error → defaults not loaded → all verdicts NULL)

**Brutal truth:** a sick/locked SQLite DB silently disarms ALL guardrails. No retry, no alert, just fail-open Permit() forever.

**Fix:** unified contractLayers map with explicit fail-open/closed designation per layer + added escape hatch env `DANDORI_GOVERN_FAIL_OPEN` so fail-closed deny can't brick a machine in production (operator can temporarily elevate to fail-open if guardrail layer stuck). Added seed block rule protecting ~/.dandori/ itself — agent could otherwise `echo secrets_guard_enabled:false >> config.yaml` to self-disarm, since Bash isn't sandboxed and config is re-read per hook.

### Phase 2: Secret/PII guardrail G1.5 (strict Deny + Luhn Gate)

Strict-secret Deny + Luhn-validated PII Gate. **Critical finding:** PII gate LEAKED the very PII it detected into approvals table + Slack. Root cause: redact.String had no PII patterns, so "mask it" mitigation was a no-op — approval strings were recording unredacted PII.

**Fix:** make redact a superset of all Deny/Gate patterns + mask approval strings before writing to `approvals` and `slack_events` tables. **Replay against REAL fleet DB:** 0 false positives in 6520 recent tool_use events — evidence the default-on Deny won't reproduce earlier G1/G2 pain.

### Phase 3: Budget downgrade-gate G3 (hard-stop → downgrade)

Changed from hard-stop to downgrade: deny only expensive-model runs over budget, cheap models continue, "switch via /model". **Critical findings:**
- NULL-model gave infinite free pass (restart session to keep model NULL, no budget charged)
- default expensive_models list had phantom "gpt-5" not in pricing table or fleet (copying Omnigent's hypothetical; real fleet uses ["opus","fable","haiku"])

**Fix:** agent-scoped NULL fallback (default to Haiku if model NULL) + per-agent-month cap + derived expensive_models from real pricing table (SELECT DISTINCT model FROM pricing). 

### Phase 4: Risk-score guardrail G5 (trickiest — self-amplifying ratchet)

Original design (from Omnigent's risk_score pattern) was self-amplifying: record() writes every action to `kind='guardrail_block'`, so +25 per block meant escalations and even transient engine errors fed their own score, monotonically locking runs into gate-everything.

**Fix:**
- **Sliding window** (last 40 events, not cumulative) — prevents ratchet from monotonic escalation
- **Self-exclusion via audit_log.action discriminator** (excludes `risk_gate/engine_error/budget_block` from scoring) — breaking feedback loop
- **DEFAULT LOG-ONLY, not gate** — deliberate adjustment from user's original "enable by default" decision. No real calibration data exists (fleet is seed, p99=50 events). Observes first, gates later after calibration. **Independent verification:** reviewer ran anti-ratchet test (2019 denial correctly fell out of window after 40 events).

## Recurring lesson baked into every phase

Every default-on guardrail risked re-triggering user's exact "guardrails obstruct my workflow" pain from earlier (user had disabled G1/G2 for blocking too many runs). Through-line mitigation: **replay/calibrate against REAL fleet data before defaulting on** (P2's 0/6520, P4's log-only) **+ config escape hatches everywhere** (DANDORI_GOVERN_FAIL_OPEN, ~/ self-protection block, budget downgrade-not-deny, risk gate log-only).

Lesson: a guardrail that can't be escaped is a guardrail that will be disabled/circumvented.

## Verify

`CGO_ENABLED=0 go build ./...` clean. `go vet ./...` clean. `go test ./... -count=1` green across all packages. Cross-compile `GOOS=linux CGO_ENABLED=0 go build ./cmd/dandori` succeeds (binary 24.1MB, +0.1MB vs v13).

33 files changed: internal/govern/contract.go, internal/govern/guardrail_*.go, internal/audit/redact.go (PII patterns extended), internal/db/schema/ (audit_log kind discriminator), cmd/dandori/main.go (escape hatch env check), + tests. All code-review changes (C1/C2/...) committed inline; no separate fix commits.

## Settled deviations

- **Risk gate ships log-only, not block** — deliberate; calibration data insufficient on 12-event fleet. Gate once fleet matures.
- **Fail-open escape hatch env required** — rejects pure fail-closed deny without operator override. Trade-off: slightly more complex (one env check), vastly safer (won't brick machine if SQLite sticks).
- **NULL-model fallback to Haiku** — user chose Opus in config, but fallback picks cheapest. Explicit in code comment + config docs; user can set DANDORI_DEFAULT_MODEL if Haiku fallback feels wrong.

## Deferred / [Sau]

- Centralized GOVERN metric dashboard (observability, not guardrail).
- PII pattern learn/calibrate loop (currently static Luhn + keyword list).
- Cross-run risk scoring (current window is per-session, not per-agent-per-month).
- Budget carryover / monthly reset policy (currently hard boundary).

## Bug production caught in red-team → phase 1 (not deferred)

Four fail-open holes in contract.go (openStore, ing.PreTool coupling, input-decode, config-load) — all file:line identified by ≥2 reviewers independently. None made it to code because red-team ran before implementation. Process worked: adversarial → plan → code, not code → test → find-bug.

## Comparison vs Omnigent

Omnigent has 4 mature GOVERN guardrails (contract fail-closed, PII+secrets Deny, budget gate, risk-score) but lacks:
- LEARN pillar (no mining, kit, import, knowledge-unit versioning)
- Hash-chain audit trail (no immutable audit_log.kind discriminator, events can be rewritten)
- Fail-open escape hatches (production operator can get bricked if guardrail layer fails)

Dandori now adopts Omnigent's 4 patterns + keeps LEARN + hash-chain audit + escape hatches. Single binary, pure SQLite, no new dependencies.

**Status:** DONE
**Summary:** v14 hardens GOVERN layer by adopting 4 Omnigent ideas (fail-closed contract, strict PII + Luhn, budget downgrade-gate, risk-score sliding window) without breaking light/pure-Go/single-binary design. Red-team identified 24 findings → 10 unique (4 Critical) before any code; all folded into plan. Sharpest insight: every default-on guardrail risked re-triggering user's earlier "guardrails block my workflow" pain. Mitigation: replay/calibrate against REAL fleet before defaulting on (P2: 0/6520 false positives, P4: log-only) + escape hatches everywhere (fail-open env, NULL-model fallback, budget downgrade not deny, risk gate log-only). Build/vet/test green. 33 files, 24.1MB binary.
**Concerns:** P4's risk-gate ships log-only (no real calibration data on seed fleet ~12 events). Mitigation: observes first, gates later. P2's 0/6520 replay validates no false-positive flood risk, but real data pattern unknown until fleet scales.
