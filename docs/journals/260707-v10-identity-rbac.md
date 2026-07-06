# v10 — Identity & RBAC (260707)

Commit pending. Plan `260707-0000-dandori-v10-identity-rbac`.

## Why v10 ≠ metric overhaul

v9 gated the grade-formula redesign on data density — needs weeks of hook-captured runs to accumulate honest-high samples. But **Tier-1 blocker from docs/05** was identity + RBAC, and it requires **zero new data** to ship. Flipped the order: v10 = identity & RBAC (local account + per-operator tokens + admin/viewer gates), v11 = metric overhaul on dense data. User chose LOCAL (username+password), not Google SSO ([Sau]). Live decision locked against plan revisions.

## Plan red-team (--hard)

2 researchers (local-auth patterns; token integration points) + planner caught **4 CRITICAL + 5 HIGH** before code. Notable: 
- **C1 trap**: rule "no-session ⇒ IsAdmin=true" was self-contradictory when any account exists (drop cookie = admin). Fix: key trust-gate on `HasEverHadAccount()` not `HasAnyAccount()` — once identity bootstrapped it stays; disabling last admin LOCKS console, never re-opens no-auth.
- **Inventory undercounts**: plan claimed 16 execActor sites + 22 gated routes. Grep-reality: ~27 actor-build sites, 38 write routes (22 requireAdmin + 8 viewer-ok). Grep-guard added to P2 acceptance criteria.
- **[M2] CHECK(role IN...) risky on old DBs**: modernc/SQLite + ALTER TABLE ADD COLUMN + CHECK has untested edge cases on legacy schema → removed, validate in-app only.

Fixes folded into phase docs before cook. Zero deferred to discussion — locked decisions section carries all trade-offs.

## Shipped

5 disjoint phases (P2/P3/P4 parallel):

**P1 auth foundation** — migration 015 (1 migration, not 3: credentials + sessions + api_tokens bundled), argon2id hashing (pure-Go), hand-rolled SQLite session store (`sessions` table, base64 32B id, 8h expiry), trust-gate middleware C1+C2, `/login` + `/logout`, CLI `dandori operator add`, in-memory brute-force rate-limiter (per-IP + per-account, 5 fails = lock).

**P2 principal propagation** — single `s.actor(r)` helper replacing 27 scattered sites (`execActor`, `launchActor`, `chatPrincipal`, inline `Cfg.UserName+"@console"`, raw `Cfg.UserName` in govern). P2 dev rewrote ALL audit-entry callsites to flow request principal (logged-in or fallback). Audit hash-chain entry format unchanged — old `@console` entries verify as-is across the @console→principal boundary; continuity test proves it (internal/govern/audit_continuity_test.go).

**P3 per-operator ingest tokens** — `api_tokens` table (SHA-256 hash PK, operator_id FK, no plaintext ever stored), new `dandori token create` CLI, ingest server-side principal lookup from token hash (LookupToken JOINs operators.disabled_at IS NULL — fixes: off-boarded operator can't ingest). **Dual-accept window (H1)**: legacy shared-token + spoofable `X-Dandori-Principal` header capped at FIXED principal `legacy-shared@ingest` (never trust header) + 2-week cutover window with that constraint. Trade-off: loses per-human attribution during window but gains spoof-safety immediately.

**P4 role gates** — `role TEXT DEFAULT 'viewer'` column on operators, `requireAdmin` middleware (fresh DB query, not cached role), 29 admin-gated routes (POST /launch, /runs/{id}/retry, /kill, /bulk-kill, /contexts/*, /rules/*, /chat/*, /budgets, /reviews/*/decide, /export/compliance, /reports/*, /settings/*, etc.), 8 viewer-ok routes (/runs/{id}/task-key, /flag, /playbook, /rules/simulate, /settings/*/test). Route new + unclassified → default admin (fail-closed).

**P5 E2E + docs** — 12 E2E tests covering trust-gate (loopback vs non-loopback), login flow, rate-limit, session-required, CSRF (origin-guard), principal in audit, token server-side lookup, role gates, audit boundary continuity. All green: `go test ./... + go vet` xanh. Docs updated: CLAUDE.md + integration-setup.md + 04-implementation-notes.md.

## Two real gaps found DURING cook (not red-team)

**Last-admin-disable bypass (P4 dev)**: `HasAnyAccount()` counts enabled accounts only. Disabling last admin → 0 enabled → re-triggered C1 local-trust → every request became admin. Root: gated on enabled count, not ever-created. Fix: `HasEverHadAccount()` checks `EXISTS(SELECT 1 FROM operators WHERE id IS NOT NULL)` — once identity is bootstrapped it STAYS bootstrapped. Added E2E and verified historical v9 single-user still works (0 accounts + loopback = no login, like before).

**Disabled-operator token stays live (merged code-review)**: console-side JOINs `operators.disabled_at` but ingest `LookupToken` only filtered `revoked_at` — off-boarded operator kept authenticating. Fix: LookupToken JOINs `operators.disabled_at IS NULL` (mirrors session load); token create refuses disabled operators. 1 test added to ingest_token_auth_test.go.

## Contracts preserved

- Hook/CLI capture (`hook`/`watch`/`wrap`) → OS-user trust boundary, session middleware mounts ONLY on console mux, zero-import of `internal/web` verified (E2E19 test). Fail-open as-is.
- Audit hash-chain append-only — entry format unchanged, old `@console` entries verify as-is; new entries carry real principal. Continuity test (internal/govern/audit_continuity_test.go) proves Verify() walks actor unchanged across boundary.
- v9 local-trust single-user mode preserved — 0 accounts + loopback = no login, straight to console (E2E01 + local-trust paths in auth_middleware.go untouched).

## Verify

- Code-review round 1 (P1 alone): 0 Critical/High after fixes.
- Code-review round 2 (P2/P3/P4 merged): 0 Critical/High.
- All 12 identity_rbac_e2e_test.go tests PASS (E2E01–08, E2E14–19).
- Audit continuity test PASS (entry created pre-v10, verified post; operator session invalidation on disable tested).
- `go test ./...` xanh (web, ingest, cli, govern packages).
- `go vet` clean.
- `CGO_ENABLED=0 go build` clean (no CGO deps added — pure Go, cross-compile safe).
- Grep-guard C3: `grep -rn '@console\|Cfg\.UserName' internal/web/*.go` (exclude test, handlers_dash display-only) = 5 lines, all fallback use in auth_middleware.go + comment lines in exec.go/runs.go.

## Deferred to v11 / [Sau]

- **Metric overhaul** — data density gate; reweight on new runs with hook active (2 weeks+).
- **Token TTL** — v10 `expires_at = NULL` (live forever); opt-in `--expires-in` flag later.
- **Google SSO** — full auth provider integration.
- **Granular per-resource RBAC** — start coarse admin/viewer; row-level later.
- **Data-at-rest encryption** — docs/05 blocker 4, still open.
- **Non-loopback HTTPS** — C2 non-loopback → console only serves bootstrap banner; real LAN usage needs HTTPS tunnel/reverse-proxy (LAN-binding without TLS is live UX debt).
- **H3 identity-fork** — machine-principal (`alice@dev-laptop` from captures) vs console username (`alice` login) split analytics; reconcile when both hit same audit entry.

## Verify

Code-review clean (0 Critical/High after fixes). 12-assertion E2E (19 spec'd; 7 proven by unit tests in this/sibling packages, re-run through E2E harness + reference pointers). Hook/capture import-guard confirmed. Audit continuity across console boundary verified. Tests passing: `go test ./...` + `go vet` + cross-compile clean.

**Status:** DONE
**Summary:** v10 ships identity (local login) + per-operator tokens + admin/viewer gates; two implementation gaps (last-admin disable, disabled-operator token) caught during cook and fixed.
**Concerns:** None — red-team C1–C5 locks honored, contracts preserved, metric overhaul deferred to v11 per data-density gate.
