# v7 — Write-back & Vision Closeout

**Date:** 2026-07-04
**Branch:** `feat/v7-writeback-closeout` (commit `70f6436`, local — not pushed)

## What shipped

Closed every remaining deferred vision feature except the Cursor/Copilot
adapters (no testable binaries). Twelve features across the three pillars:

- **CAPTURE/GOVERN write-back** — Jira transition, GitHub PR comment/approve,
  Calendar event, all as approval-gated observer actions.
- **LEARN delivery** — Sheets cost/ROI export, Slack+Gmail digest.
- **GOVERN** — per-check quality-gate override, global gate thresholds, Drive→context import.
- **LEARN** — post-action check, agent-assignment suggestions.
- **CAPTURE UI** — saved run views, TV-mode wallboard.

Built as MVP→v6 was: plan → red-team → apply fixes → cook phase-by-phase with
tester + reviewer gates. Seven phases, six run in three parallel waves on
disjoint files (strict per-phase ownership; route registration split into
`routes_phaseNN.go` files so concurrent phases never raced `server.go`).

## Design decisions worth remembering

- **Double-gate for state changes.** External writes pass the DRY_RUN Guard
  *and* human approval. Params pinned in the approval at request time; the
  apply case re-validates against live state (Jira by transition **name** not
  id, PR by **head SHA**) so a stale target fails re-openably instead of firing
  a wrong write.
- **The operator inbox already existed.** The plan assumed we'd build one; the
  existing `/reviews` page turned out to have no surface filter and already ran
  the applier on approve. Verified, then reused — no redundant inbox.
- **Config-pinned destinations, not request-supplied.** Digest recipients and
  the export spreadsheet come only from config. The console has no auth and a
  local agent could POST a trigger, so the destination is the trust boundary —
  the request can trigger a send but never choose where it goes.
- **Imported Drive text is untrusted instructions.** Context becomes SessionStart
  `additionalContext` that the model treats as instructions. Every layer's
  import is approval-gated with a **full-body** review (a payload past a
  truncation must not hide), never a direct `SaveContext`.

## Bugs caught — and where

- **Calendar idempotency (review, pre-live).** Dedup was checked *after* the
  external insert → a crash between insert and record could duplicate the event.
  Reordered to reserve the dedup row *before* the write, releasing it on failure.
- **Sheets "Summary" tab — only reproducible live.** Offline fakes happily
  accepted a `values update` to `Summary!A1`; the real Sheets API rejected it
  because a freshly created spreadsheet has only `Sheet1`. Fixed the create
  request to provision a `Summary` tab, added a regression test. **Lesson: a
  fake that echoes argv proves we *called* the API correctly, not that the API
  *accepts* it — the live E2E is not optional for external-write features.**
- **Secret fragment leak (review, pre-live).** The "found a secret here" hint
  echoed 24 raw characters of the detected secret to the browser. Now returns
  the redacted fragment.

## Live verification

`scripts/e2e_v7_writeback.sh` under `DANDORI_LIVE=1 DRY_RUN=false` — **4/4 legs**
against real services: created and transitioned Jira SCRUM-26, inserted a real
Google Calendar event, exported a real Sheet. Digest skipped (no recipients
configured — the correct config-pinned behavior). Scratch objects are created
by the script and flagged for manual cleanup; the seeded SCRUM/MPM fixtures are
never touched.

## Open follow-ups (non-blocking)

- `gate_min_pass_pct` is persisted/validated but has no consumer yet — a lever
  for a future gate site, not wired to avoid faking a consumer.
- Two review MEDIUMs left as hygiene: wallboard 5s poll uses the writer conn
  instead of `.Read()`; the send-digest handler uses `context.Background()`.
- Manual cleanup of the live scratch objects (Jira SCRUM-25/26, two Calendar
  events, two auto-created Sheets).
