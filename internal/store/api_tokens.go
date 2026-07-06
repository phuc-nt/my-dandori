package store

import "database/sql"

// Per-operator ingest tokens (v10). id is the SHA-256 hex hash of the
// plaintext token — the plaintext itself is never stored (show-once at
// creation, see internal/cli/token_cmd.go).

// ApiToken is one issued token record (never includes the plaintext).
type ApiToken struct {
	ID          string // sha256 hex hash
	OperatorID  string
	DisplayName string
	CreatedAt   string
	LastUsedAt  *string
	RevokedAt   *string
}

// CreateToken inserts a new token row bound to operatorID. hash is the
// SHA-256 hex of the plaintext (see auth.TokenHash) — callers must never
// pass the plaintext itself.
func (s *Store) CreateToken(hash, operatorID, displayName string) error {
	_, err := s.DB.Exec(`INSERT INTO api_tokens(id, operator_id, display_name, created_at)
		VALUES(?, ?, ?, ?)`, hash, operatorID, displayName, Now())
	return err
}

// LookupToken resolves a token hash to its operator id. ok is false when the
// hash is unknown, the token was revoked, or the owning operator has since
// been disabled — callers must treat all three as "unauthenticated" (401),
// never distinguishing which to avoid a probing oracle. Disabling an
// operator must invalidate their tokens immediately (mirrors
// auth.SessionStore.Load's operators.disabled_at IS NULL join) without
// requiring an explicit revoke. The lookup still starts from the api_tokens
// PK (id), so the join only adds a PK lookup on operators — no table scan.
func (s *Store) LookupToken(hash string) (operatorID string, ok bool) {
	err := s.DB.QueryRow(`SELECT api_tokens.operator_id FROM api_tokens
		JOIN operators ON operators.id = api_tokens.operator_id
		WHERE api_tokens.id = ? AND api_tokens.revoked_at IS NULL AND operators.disabled_at IS NULL`, hash).Scan(&operatorID)
	if err != nil {
		return "", false
	}
	return operatorID, true
}

// TouchToken best-effort updates last_used_at. Errors are intentionally
// swallowed by the caller (ingest auth path) — a failed touch must never
// fail the request it is piggy-backing on.
func (s *Store) TouchToken(hash string) error {
	_, err := s.DB.Exec(`UPDATE api_tokens SET last_used_at = ? WHERE id = ?`, Now(), hash)
	return err
}

// ListTokens returns every token (including revoked) for operatorID, newest
// first, for `dandori token list`.
func (s *Store) ListTokens(operatorID string) ([]ApiToken, error) {
	rows, err := s.Read().Query(`SELECT id, operator_id, display_name, created_at, last_used_at, revoked_at
		FROM api_tokens WHERE operator_id = ? ORDER BY created_at DESC`, operatorID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []ApiToken
	for rows.Next() {
		var t ApiToken
		if err := rows.Scan(&t.ID, &t.OperatorID, &t.DisplayName, &t.CreatedAt, &t.LastUsedAt, &t.RevokedAt); err != nil {
			return nil, err
		}
		out = append(out, t)
	}
	return out, rows.Err()
}

// RevokeToken marks a token id (full hash, or a caller-resolved prefix
// match — see token_cmd.go) revoked. Idempotent: revoking an already-revoked
// or unknown id is not an error, matching the CLI's forgiving semantics.
func (s *Store) RevokeToken(id string) error {
	_, err := s.DB.Exec(`UPDATE api_tokens SET revoked_at = ? WHERE id = ? AND revoked_at IS NULL`, Now(), id)
	return err
}

// TokenByPrefix resolves a short id prefix (as printed by `token list`) to
// the full hash, for `token revoke <prefix>`. Returns sql.ErrNoRows if zero
// or more than one token matches (ambiguous prefixes must not silently
// revoke the wrong token).
func (s *Store) TokenByPrefix(prefix string) (string, error) {
	rows, err := s.DB.Query(`SELECT id FROM api_tokens WHERE id LIKE ? || '%'`, prefix)
	if err != nil {
		return "", err
	}
	defer rows.Close()
	var id string
	count := 0
	for rows.Next() {
		if err := rows.Scan(&id); err != nil {
			return "", err
		}
		count++
	}
	if err := rows.Err(); err != nil {
		return "", err
	}
	if count != 1 {
		return "", sql.ErrNoRows
	}
	return id, nil
}
