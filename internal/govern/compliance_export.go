package govern

import (
	"database/sql"
	"encoding/csv"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"strconv"
	"strings"

	"github.com/phuc-nt/dandori/internal/redact"
	"github.com/phuc-nt/dandori/internal/store"
)

// Redaction happens at ingest already; exporting redacts again as defense in
// depth — the bundle is handed to auditors/SIEM.
func redactStr(s string) string { return redact.String(s) }

// csvCell defuses spreadsheet formula injection (=,+,-,@ prefixes execute in
// Excel on an auditor's machine).
func csvCell(s string) string {
	if s != "" && strings.ContainsRune("=+-@", rune(s[0])) {
		return "'" + s
	}
	return s
}

// BuildComplianceBundle assembles the export and records the export itself
// in the audit trail (exports are governance actions too).
func BuildComplianceBundle(st *store.Store, actor string) (*ComplianceBundle, error) {
	b := &ComplianceBundle{GeneratedAt: store.Now(), GeneratedBy: actor}
	broken, reason, err := Verify(st)
	if err != nil {
		return nil, err
	}
	b.Verify = VerifyResult{OK: reason == "", BrokenAt: broken, Reason: reason}

	if err := queryInto(st, `SELECT id, ts, actor, action, COALESCE(subject,''), COALESCE(detail,''),
		prev_hash, hash, signature, key_id FROM audit_log ORDER BY id`, func(rows scanner) error {
		var e AuditEntry
		var sig []byte
		var keyID sql.NullInt64
		if err := rows.Scan(&e.ID, &e.TS, &e.Actor, &e.Action, &e.Subject, &e.Detail, &e.PrevHash, &e.Hash, &sig, &keyID); err != nil {
			return err
		}
		e.Machine = machineFromDetail(e.Detail)
		e.Detail = redactStr(e.Detail)
		e.Signed = len(sig) > 0
		if e.Signed {
			e.KeyID = int(keyID.Int64)
			b.SignedCount++
		} else {
			b.UnsignedCount++
		}
		b.AuditLog = append(b.AuditLog, e)
		return nil
	}); err != nil {
		return nil, err
	}
	if err := queryInto(st, `SELECT id, COALESCE(run_id,''), action, status, COALESCE(decided_by,''),
		COALESCE(decision_note,''), channel, requested_at, COALESCE(decided_at,'') FROM approvals ORDER BY id`,
		func(rows scanner) error {
			var e ApprovalEntry
			if err := rows.Scan(&e.ID, &e.RunID, &e.Action, &e.Status, &e.DecidedBy, &e.Note, &e.Channel, &e.Requested, &e.Decided); err != nil {
				return err
			}
			e.Action = redactStr(e.Action)
			b.Approvals = append(b.Approvals, e)
			return nil
		}); err != nil {
		return nil, err
	}
	if err := queryInto(st, `SELECT id, COALESCE(run_id,''), COALESCE(reason,''), status, COALESCE(jira_key,'')
		FROM flags ORDER BY id`, func(rows scanner) error {
		var e FlagEntry
		if err := rows.Scan(&e.ID, &e.RunID, &e.Reason, &e.Status, &e.JiraKey); err != nil {
			return err
		}
		b.Flags = append(b.Flags, e)
		return nil
	}); err != nil {
		return nil, err
	}
	if err := queryInto(st, `SELECT id, COALESCE(agent_id,''), COALESCE(project,''), status, cost_usd,
		COALESCE(started_at,'') FROM runs ORDER BY started_at`, func(rows scanner) error {
		var e RunSummary
		if err := rows.Scan(&e.ID, &e.AgentID, &e.Project, &e.Status, &e.CostUSD, &e.Started); err != nil {
			return err
		}
		b.RunsSummary = append(b.RunsSummary, e)
		return nil
	}); err != nil {
		return nil, err
	}

	cov, err := buildCoverageReport(st)
	if err != nil {
		return nil, err
	}
	b.Coverage = cov

	if pubB64, ok := PublicKeyFromSigningKey(); ok {
		if fp, err := PubkeyFingerprint(pubB64); err == nil {
			b.PubkeyFingerprint = fp
		}
	}
	b.TrustNote = pubkeyTrustNote
	b.Disclosures = buildDisclosures()

	dir := CheckpointDir()
	if cp, ok, err := LatestCheckpoint(dir); err == nil && ok {
		cpCopy := cp
		b.Checkpoint = &cpCopy
	}

	a := &Audit{St: st, Actor: actor}
	exportID, err := a.Append("compliance_export", "audit_log", fmt.Sprintf("%d audit entries exported", len(b.AuditLog)))
	if err != nil {
		return nil, err
	}
	writeExportCheckpoint(st, exportID)
	return b, nil
}

// writeExportCheckpoint anchors an on-demand checkpoint at export time (in
// addition to the every-N-rows cadence in AppendTx) so an auditor pulling a
// compliance bundle always has a checkpoint at least as recent as the bundle
// itself. Best-effort: no signing key configured means nothing to sign, and
// a disk error here must not fail the export the auditor is waiting on.
func writeExportCheckpoint(st *store.Store, tipID int64) {
	priv, ok := loadSigningKey()
	if !ok {
		return
	}
	var tipHash string
	_ = st.DB.QueryRow(`SELECT hash FROM audit_log WHERE id = ?`, tipID).Scan(&tipHash)
	if tipHash == "" {
		return
	}
	dir, offsite := resolveCheckpointDirs()
	if err := WriteCheckpoint(priv, dir, offsite, tipID, tipHash, store.Now(), firstSignedIDValue(st)); err != nil {
		log.Printf("audit: export checkpoint at id %d failed: %v", tipID, err)
	}
}

// WriteJSON / WriteCSV serialize the bundle. CSV is the flat audit trail
// (SIEM ingest); JSON is the full bundle.
func (b *ComplianceBundle) WriteJSON(w io.Writer) error {
	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")
	return enc.Encode(b)
}

func (b *ComplianceBundle) WriteCSV(w io.Writer) error {
	cw := csv.NewWriter(w)
	if err := cw.Write([]string{"id", "ts", "actor", "action", "subject", "detail", "prev_hash", "hash", "signed", "machine"}); err != nil {
		return err
	}
	for _, e := range b.AuditLog {
		if err := cw.Write([]string{strconv.FormatInt(e.ID, 10), e.TS, e.Actor,
			csvCell(e.Action), csvCell(e.Subject), csvCell(e.Detail), e.PrevHash, e.Hash,
			strconv.FormatBool(e.Signed), csvCell(e.Machine)}); err != nil {
			return err
		}
	}
	cw.Flush()
	return cw.Error()
}

type scanner interface{ Scan(dest ...any) error }

func queryInto(st *store.Store, q string, scan func(scanner) error) error {
	rows, err := st.DB.Query(q)
	if err != nil {
		return err
	}
	defer rows.Close()
	for rows.Next() {
		if err := scan(rows); err != nil {
			return err
		}
	}
	return rows.Err()
}
