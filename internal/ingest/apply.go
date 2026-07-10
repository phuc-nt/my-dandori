package ingest

import (
	"database/sql"
	"strconv"

	"github.com/phuc-nt/dandori/internal/capture"
	"github.com/phuc-nt/dandori/internal/govern"
	"github.com/phuc-nt/dandori/internal/redact"
	"github.com/phuc-nt/dandori/internal/store"
)

// applyBatch writes a batch inside one transaction. Attribution comes from
// the record (the client resolved it — cwd is meaningless on this machine);
// operator comes from the auth principal, never the body. Events dedup on
// the client ULID so spool replays count exactly once. Event ts is the
// client's clock (analytics follow the machine where work happened); audit
// entries elsewhere keep server time.
//
// Guardrail-decision records (rec.Action set, see record.go) additionally
// get a co-signed audit_log row via govern.AppendTx — called with THIS
// batch's tx, never a new Begin, so it cannot deadlock against the
// single-connection write pool (store.Open's SetMaxOpenConns(1)): a second
// Begin from the same process would block forever waiting for a connection
// this same call already holds.
func (s *Server) applyBatch(operatorID string, recs []Record) (int, error) {
	tx, err := s.St.DB.Begin()
	if err != nil {
		return 0, err
	}
	defer tx.Rollback()
	audit := &govern.Audit{St: s.St, Actor: operatorID}
	applied := 0
	for i := range recs {
		rec := &recs[i]
		if rec.SessionID == "" {
			continue
		}
		if err := ensureRunTx(tx, rec, operatorID); err != nil {
			return 0, err
		}
		switch rec.Type {
		case "event":
			if rec.ULID == "" {
				continue // no idempotency key, refuse to double-count
			}
			ts := rec.ClientTS
			if ts == "" {
				ts = store.Now()
			}
			var ok sql.NullInt64
			if rec.OK != nil {
				ok = sql.NullInt64{Int64: *rec.OK, Valid: true}
			}
			res, err := tx.Exec(`INSERT INTO events(run_id, ts, kind, tool_name, ok, payload, ulid)
				VALUES(?, ?, ?, ?, ?, ?, ?) ON CONFLICT(ulid) WHERE ulid IS NOT NULL DO NOTHING`,
				rec.SessionID, ts, rec.Kind, rec.Tool, ok, redact.String(rec.Payload), rec.ULID)
			if err != nil {
				return 0, err
			}
			if n, _ := res.RowsAffected(); n == 1 {
				applied++
			}
			if rec.Action != "" {
				if err := applyGuardrailAuditTx(tx, audit, operatorID, rec); err != nil {
					return 0, err
				}
			}
		case "finalize":
			if rec.Finalize == nil {
				continue
			}
			// SET semantics: idempotent on replay. Count it only when it
			// actually moved the run to a terminal state, so `applied` stays
			// an honest net-change signal (not "records seen").
			changed, err := applyFinalizeTx(tx, rec.SessionID, rec.Finalize)
			if err != nil {
				return 0, err
			}
			if changed {
				applied++
			}
		}
	}
	if err := tx.Commit(); err != nil {
		return 0, err
	}
	detectCoverageGaps(s.St, recs)
	return applied, nil
}

// ensureRunTx upserts agent + run for a record. transcript_path stays NULL:
// the server never sees, stores, or opens a transcript (red-team H4).
func ensureRunTx(tx *sql.Tx, rec *Record, operatorID string) error {
	agentName := rec.AgentName
	if agentName == "" {
		agentName = "unknown"
	}
	agentID := capture.AgentID(agentName)
	if _, err := tx.Exec(`INSERT INTO agents(id, name, created_at) VALUES(?, ?, ?)
		ON CONFLICT(name) DO NOTHING`, agentID, agentName, store.Now()); err != nil {
		return err
	}
	_, err := tx.Exec(`INSERT INTO runs(id, session_id, agent_id, project, cwd, started_at, source, operator_id)
		VALUES(?, ?, ?, ?, ?, ?, 'ingest', ?)
		ON CONFLICT(session_id) DO UPDATE SET operator_id = COALESCE(runs.operator_id, excluded.operator_id)`,
		rec.SessionID, rec.SessionID, agentID, rec.Project, rec.CWD, store.Now(), nullable(operatorID))
	return err
}

// applyFinalizeTx SETs the derived numerics (idempotent — replay-safe).
// Returns true only when this call actually finalized a still-running run,
// so callers can count net changes rather than replays.
func applyFinalizeTx(tx *sql.Tx, runID string, f *RunFinalize) (bool, error) {
	var prevStatus string
	_ = tx.QueryRow(`SELECT status FROM runs WHERE id = ?`, runID).Scan(&prevStatus)
	firstFinalize := prevStatus == "running"

	status := f.Status
	if status == "" {
		status = "done"
	}
	endedAt := f.EndedAt
	if endedAt == "" {
		endedAt = store.Now()
	}
	_, err := tx.Exec(`UPDATE runs SET
		model = COALESCE(NULLIF(?, ''), model),
		input_tokens = ?, output_tokens = ?, cache_read_tokens = ?, cache_write_tokens = ?,
		cost_usd = ?, task_key = COALESCE(NULLIF(task_key, ''), NULLIF(?, ''), task_key),
		lines_added = ?, lines_deleted = ?,
		head_before = COALESCE(NULLIF(?, ''), head_before), head_after = COALESCE(NULLIF(?, ''), head_after),
		status = CASE WHEN status = 'running' THEN ? ELSE status END,
		ended_at = COALESCE(ended_at, ?)
		WHERE id = ?`,
		f.Model, f.InputTokens, f.OutputTokens, f.CacheRead, f.CacheWrite,
		f.CostUSD, f.TaskKey, f.LinesAdded, f.LinesDeleted,
		f.HeadBefore, f.HeadAfter, status, endedAt, runID)
	if err != nil {
		return false, err
	}
	// Steering count + prompt proxy land as SET-semantics analytics events.
	if err := upsertCountEventTx(tx, runID, "user_msg", strconv.Itoa(f.MidRunMsgs)); err != nil {
		return false, err
	}
	if err := upsertCountEventTx(tx, runID, "prompt_proxy", capture.PromptProxyPayload(f.PromptWords, f.PromptSpec)); err != nil {
		return false, err
	}
	return firstFinalize, nil
}

// upsertCountEventTx keeps one numeric event of a kind per run up to date.
func upsertCountEventTx(tx *sql.Tx, runID, kind, payload string) error {
	res, err := tx.Exec(`UPDATE events SET payload = ? WHERE run_id = ? AND kind = ?`,
		payload, runID, kind)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		_, err = tx.Exec(`INSERT INTO events(run_id, ts, kind, tool_name, ok, payload)
			VALUES(?, ?, ?, '', NULL, ?)`, runID, store.Now(), kind, payload)
	}
	return err
}

func nullable(s string) any {
	if s == "" {
		return nil
	}
	return s
}
