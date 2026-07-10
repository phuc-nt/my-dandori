package ingest

import (
	"fmt"
	"log"
	"time"

	"github.com/phuc-nt/dandori/internal/capture"
	"github.com/phuc-nt/dandori/internal/govern"
	"github.com/phuc-nt/dandori/internal/store"
)

// coverageFlagPrefix / staleSnapshotFlagPrefix tag flags rows created by
// this detector so hasOpenFlag can dedup them (same pattern
// govern/closed_loop.go uses for its own flag kinds) and so /risk +
// compliance export can tell them apart from other flag reasons at a
// glance.
const (
	coverageFlagPrefix      = "central coverage gap"
	staleSnapshotFlagPrefix = "central stale snapshot"

	// snapshotStalenessThreshold: a policy snapshot older than this when a
	// guardrail decision (or a batch of activity) references it suggests the
	// dev machine's client is running on a frozen/never-refreshed cache —
	// which central mode's advisory model already documents as a known
	// architectural limit (see phase-04 Risk Assessment). This is a review
	// signal, not a hard cutoff.
	snapshotStalenessThreshold = 5 * time.Minute
)

// detectCoverageGaps runs AFTER a batch has committed (read-only cross-check
// — cheap, uses the read pool). It flags, but never blocks or auto-demotes,
// two review signals:
//
//  1. Coverage gap: a mutating tool_use in this batch matches an enabled
//     block rule but the same run has no guardrail-decision event at all —
//     evidence a central client may be evaluating against a stale/edited
//     policy cache (or skipping evaluation) instead of the real snapshot.
//  2. Stale snapshot: a guardrail-decision record in this batch echoes a
//     SnapshotFetchedAt older than snapshotStalenessThreshold.
//
// Both are heuristic (a block-rule match is not proof a call SHOULD have
// been denied under whatever band/scope applied, and a stale echo is not
// proof of tampering) — hence flags, reviewed by a human, never an
// automatic band downgrade or a block. Errors are logged, not returned:
// this is best-effort observability that must never fail the batch that
// already committed.
func detectCoverageGaps(st *store.Store, recs []Record) {
	for i := range recs {
		rec := &recs[i]
		if rec.Type != "event" || rec.SessionID == "" {
			continue
		}
		if rec.Kind == "tool_use" && rec.Action == "" {
			checkCoverageGap(st, rec)
		}
		if rec.Action != "" && rec.SnapshotFetchedAt != "" {
			checkSnapshotFreshness(st, rec)
		}
	}
}

// checkCoverageGap flags rec's run when its tool_use matches an enabled
// block rule but the run has no guardrail-decision event on record.
// The record's attribution (agent, project) is threaded through so
// agent- and project-scoped block rules resolve — an empty agentID/project
// would silently skip every scoped rule and only ever match global ones.
func checkCoverageGap(st *store.Store, rec *Record) {
	tc := govern.ExtractToolCall(rec.SessionID, capture.AgentID(rec.AgentName), rec.Project, rec.CWD, rec.Tool, []byte(rec.Payload))
	hit, err := govern.MatchesEnabledBlockRule(st, tc)
	if err != nil {
		log.Println("ingest: coverage detector rule check:", err)
		return
	}
	if !hit {
		return
	}
	var guardrailEvents int
	err = st.Read().QueryRow(`SELECT count(*) FROM events
		WHERE run_id = ? AND kind IN (?, ?, ?, ?, ?)`,
		rec.SessionID, govern.ActionKillBlock, govern.ActionGuardrailBlock,
		govern.ActionSecretsBlock, govern.ActionBudgetBlock, govern.ActionPermissionAsk).Scan(&guardrailEvents)
	if err != nil {
		log.Println("ingest: coverage detector guardrail-event check:", err)
		return
	}
	if guardrailEvents > 0 {
		return // this run DID produce a guardrail decision somewhere in its history — covered
	}
	reason := fmt.Sprintf("%s: run %s tool_use (%s) matched an enabled block rule but no guardrail-decision event was recorded for this run — review whether the central client evaluated against the real policy snapshot",
		coverageFlagPrefix, rec.SessionID, rec.Tool)
	insertFlagIfAbsent(st, rec.SessionID, coverageFlagPrefix, reason)
}

// checkSnapshotFreshness flags rec's run when its echoed snapshot age
// exceeds snapshotStalenessThreshold.
func checkSnapshotFreshness(st *store.Store, rec *Record) {
	fetched, err := time.Parse(time.RFC3339, rec.SnapshotFetchedAt)
	if err != nil {
		return // unparseable timestamp — nothing to flag, not this detector's job to validate wire format
	}
	age := time.Since(fetched)
	if age <= snapshotStalenessThreshold {
		return
	}
	reason := fmt.Sprintf("%s: run %s guardrail decision (%s) referenced a policy snapshot fetched %s ago — client may be running on a frozen cache",
		staleSnapshotFlagPrefix, rec.SessionID, rec.Action, age.Round(time.Second))
	insertFlagIfAbsent(st, rec.SessionID, staleSnapshotFlagPrefix, reason)
}

// insertFlagIfAbsent writes an open flag unless one with the same prefix is
// already open for this run — same not-open-loop pattern
// govern/closed_loop.go's openLowGradeFlag uses, so repeated batches don't
// spam duplicate flags for a condition that hasn't been resolved yet.
func insertFlagIfAbsent(st *store.Store, runID, prefix, reason string) {
	var existing int64
	err := st.Read().QueryRow(`SELECT id FROM flags WHERE run_id = ? AND status = 'open' AND reason LIKE ? || '%'`,
		runID, prefix).Scan(&existing)
	if err == nil {
		return // already flagged and open — no spam
	}
	if _, err := st.DB.Exec(`INSERT INTO flags(run_id, reason, created_at) VALUES(?, ?, ?)`,
		runID, reason, store.Now()); err != nil {
		log.Println("ingest: coverage detector flag insert:", err)
	}
}
