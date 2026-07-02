package govern

import (
	"encoding/csv"
	"encoding/json"
	"fmt"
	"io"
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

// ComplianceBundle is the auditor-facing export: the full tamper-evident
// trail with its live verification result, plus governance state.
type ComplianceBundle struct {
	GeneratedAt string          `json:"generated_at"`
	GeneratedBy string          `json:"generated_by"`
	Verify      VerifyResult    `json:"verify"`
	AuditLog    []AuditEntry    `json:"audit_log"`
	Approvals   []ApprovalEntry `json:"approvals"`
	Flags       []FlagEntry     `json:"flags"`
	RunsSummary []RunSummary    `json:"runs_summary"`
}

type VerifyResult struct {
	OK       bool  `json:"ok"`
	BrokenAt int64 `json:"broken_at,omitempty"`
}

type AuditEntry struct {
	ID       int64  `json:"id"`
	TS       string `json:"ts"`
	Actor    string `json:"actor"`
	Action   string `json:"action"`
	Subject  string `json:"subject"`
	Detail   string `json:"detail"`
	PrevHash string `json:"prev_hash"`
	Hash     string `json:"hash"`
}

type ApprovalEntry struct {
	ID        int64  `json:"id"`
	RunID     string `json:"run_id"`
	Action    string `json:"action"`
	Status    string `json:"status"`
	DecidedBy string `json:"decided_by"`
	Note      string `json:"note"`
	Channel   string `json:"channel"`
	Requested string `json:"requested_at"`
	Decided   string `json:"decided_at"`
}

type FlagEntry struct {
	ID      int64  `json:"id"`
	RunID   string `json:"run_id"`
	Reason  string `json:"reason"`
	Status  string `json:"status"`
	JiraKey string `json:"jira_key"`
}

type RunSummary struct {
	ID      string  `json:"id"`
	AgentID string  `json:"agent_id"`
	Project string  `json:"project"`
	Status  string  `json:"status"`
	CostUSD float64 `json:"cost_usd"`
	Started string  `json:"started_at"`
}

// BuildComplianceBundle assembles the export and records the export itself
// in the audit trail (exports are governance actions too).
func BuildComplianceBundle(st *store.Store, actor string) (*ComplianceBundle, error) {
	b := &ComplianceBundle{GeneratedAt: store.Now(), GeneratedBy: actor}
	broken, err := Verify(st)
	if err != nil {
		return nil, err
	}
	b.Verify = VerifyResult{OK: broken == 0, BrokenAt: broken}

	if err := queryInto(st, `SELECT id, ts, actor, action, COALESCE(subject,''), COALESCE(detail,''), prev_hash, hash
		FROM audit_log ORDER BY id`, func(rows scanner) error {
		var e AuditEntry
		if err := rows.Scan(&e.ID, &e.TS, &e.Actor, &e.Action, &e.Subject, &e.Detail, &e.PrevHash, &e.Hash); err != nil {
			return err
		}
		e.Detail = redactStr(e.Detail)
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

	a := &Audit{St: st, Actor: actor}
	if _, err := a.Append("compliance_export", "audit_log", fmt.Sprintf("%d audit entries exported", len(b.AuditLog))); err != nil {
		return nil, err
	}
	return b, nil
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
	if err := cw.Write([]string{"id", "ts", "actor", "action", "subject", "detail", "prev_hash", "hash"}); err != nil {
		return err
	}
	for _, e := range b.AuditLog {
		if err := cw.Write([]string{strconv.FormatInt(e.ID, 10), e.TS, e.Actor,
			csvCell(e.Action), csvCell(e.Subject), csvCell(e.Detail), e.PrevHash, e.Hash}); err != nil {
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
