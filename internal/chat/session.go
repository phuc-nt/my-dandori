// Package chat is the CEO assistant: an OpenRouter tool-calling loop over
// Dandori's own read functions, with action tools that can only CREATE
// approval requests — never execute or decide anything. Vietnamese-first.
package chat

import (
	"time"

	"github.com/phuc-nt/dandori/internal/redact"
	"github.com/phuc-nt/dandori/internal/store"
)

// Message is one chat turn as stored/rendered.
type Message struct {
	ID      int64
	Role    string // user | assistant
	Content string
}

// Session returns today's session id for a principal, creating it lazily —
// the day granularity doubles as the token-budget window.
func Session(st *store.Store, principal string) (int64, error) {
	day := time.Now().UTC().Format("2006-01-02")
	_, err := st.DB.Exec(`INSERT INTO chat_sessions(principal, day, created_at)
		VALUES(?, ?, ?) ON CONFLICT(principal, day) DO NOTHING`, principal, day, store.Now())
	if err != nil {
		return 0, err
	}
	var id int64
	err = st.DB.QueryRow(`SELECT id FROM chat_sessions WHERE principal = ? AND day = ?`, principal, day).Scan(&id)
	return id, err
}

// AddMessage persists one turn, redacted — chat text can quote tool output.
func AddMessage(st *store.Store, sessionID int64, role, content string) error {
	_, err := st.DB.Exec(`INSERT INTO chat_messages(session_id, role, content, created_at)
		VALUES(?, ?, ?, ?)`, sessionID, role, redact.String(content), store.Now())
	return err
}

// History returns a session's turns in order (bounded — old turns fall off
// the model context anyway).
func History(st *store.Store, sessionID int64, limit int) ([]Message, error) {
	rows, err := st.Read().Query(`SELECT id, role, content FROM (
		SELECT id, role, content FROM chat_messages WHERE session_id = ? ORDER BY id DESC LIMIT ?
	) ORDER BY id`, sessionID, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Message
	for rows.Next() {
		var m Message
		if err := rows.Scan(&m.ID, &m.Role, &m.Content); err != nil {
			return nil, err
		}
		out = append(out, m)
	}
	return out, rows.Err()
}

// AddTokens counts a completed request's usage against the daily budget.
func AddTokens(st *store.Store, sessionID int64, n int64) {
	_, _ = st.DB.Exec(`UPDATE chat_sessions SET tokens_used = tokens_used + ? WHERE id = ?`, n, sessionID)
}

// TokensToday sums today's usage across the principal's sessions.
func TokensToday(st *store.Store, principal string) int64 {
	day := time.Now().UTC().Format("2006-01-02")
	var n int64
	_ = st.Read().QueryRow(`SELECT COALESCE(sum(tokens_used),0) FROM chat_sessions
		WHERE principal = ? AND day = ?`, principal, day).Scan(&n)
	return n
}

// AuditToolCall logs one tool invocation (args/result redacted + truncated).
func AuditToolCall(st *store.Store, sessionID int64, tool, args, result string, approvalID int64) {
	if len(result) > 2000 {
		result = result[:2000] + "…"
	}
	var appr any
	if approvalID > 0 {
		appr = approvalID
	}
	_, _ = st.DB.Exec(`INSERT INTO tool_audits(session_id, tool, args, result, approval_id, ts)
		VALUES(?, ?, ?, ?, ?, ?)`,
		sessionID, tool, redact.String(args), redact.String(result), appr, store.Now())
}
