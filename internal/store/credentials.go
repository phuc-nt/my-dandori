package store

import "database/sql"

// Console login accounts, layered onto the existing operators table (H3:
// canonical id = username, set by `operator add`, distinct from the
// machine-principal rows ResolveOperator auto-creates like alice@dev-laptop).

// CreateOperatorAccount inserts a new login-capable operator row. id is the
// canonical operator id (== username, per H3); passwordHash is the argon2id
// encoded hash from auth.HashPassword. role must already be validated by the
// caller (admin|viewer) — this layer does not re-validate (M2: no CHECK
// constraint on the column).
func (s *Store) CreateOperatorAccount(id, username, passwordHash, role string) error {
	_, err := s.DB.Exec(`INSERT INTO operators(id, display, username, password_hash, role, created_at)
		VALUES(?, ?, ?, ?, ?, ?)`, id, username, username, passwordHash, role, Now())
	return err
}

// OperatorCredential is the row shape needed to authenticate a login attempt.
type OperatorCredential struct {
	ID           string
	Username     string
	PasswordHash string
	Role         string
	DisabledAt   string // "" when active
}

// OperatorByUsername looks up a login account by username. Returns
// (nil, nil) when no such username exists — callers must not distinguish
// "unknown user" from "wrong password" in the response (generic error).
func (s *Store) OperatorByUsername(username string) (*OperatorCredential, error) {
	var c OperatorCredential
	var passwordHash, disabledAt sql.NullString
	err := s.DB.QueryRow(`SELECT id, username, password_hash, role, disabled_at
		FROM operators WHERE username = ?`, username).
		Scan(&c.ID, &c.Username, &passwordHash, &c.Role, &disabledAt)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	c.PasswordHash = passwordHash.String
	c.DisabledAt = disabledAt.String
	return &c, nil
}

// SetPassword updates an operator's password hash. Callers must also call
// auth.SessionStore.DeleteForOperator(id) afterward (H4) — this layer only
// touches the credential row.
func (s *Store) SetPassword(id, passwordHash string) error {
	res, err := s.DB.Exec(`UPDATE operators SET password_hash = ? WHERE id = ?`, passwordHash, id)
	if err != nil {
		return err
	}
	return checkRowsAffected(res)
}

// HasAnyAccount reports whether at least one enabled login account exists.
// An account with a NULL password_hash (a plain machine-principal row) does
// not count; a disabled account does not count. This is NOT the C1
// trust-gate input (see HasEverHadAccount) -- use this only for "is there an
// enabled account right now" checks (e.g. onboarding hints).
func (s *Store) HasAnyAccount() (bool, error) {
	var n int
	err := s.DB.QueryRow(`SELECT COUNT(*) FROM operators
		WHERE password_hash IS NOT NULL AND disabled_at IS NULL`).Scan(&n)
	return n > 0, err
}

// HasEverHadAccount reports whether a login account has ever been created,
// including ones since disabled. This is the C1 trust-gate input (not
// HasAnyAccount): once an org bootstraps identity, disabling the last admin
// must lock the console, never fall back to no-auth local-trust.
func (s *Store) HasEverHadAccount() (bool, error) {
	var n int
	err := s.DB.QueryRow(`SELECT COUNT(*) FROM operators WHERE password_hash IS NOT NULL`).Scan(&n)
	return n > 0, err
}

// OperatorRole returns the role for an operator id ("" if not found).
// Does NOT check disabled_at — callers that gate access on being an active
// operator must also call OperatorEnabled.
func (s *Store) OperatorRole(id string) (string, error) {
	var role string
	err := s.DB.QueryRow(`SELECT role FROM operators WHERE id = ?`, id).Scan(&role)
	if err == sql.ErrNoRows {
		return "", nil
	}
	return role, err
}

// OperatorEnabled reports whether operator id exists and is not disabled.
// found is false when the id is unknown; enabled is only meaningful when
// found is true. Used by `token create` so an off-boarded operator cannot
// be issued a fresh ingest token (LookupToken already blocks disabled
// operators' existing tokens; this closes the "mint a new one" gap).
func (s *Store) OperatorEnabled(id string) (found, enabled bool, err error) {
	var disabledAt sql.NullString
	err = s.DB.QueryRow(`SELECT disabled_at FROM operators WHERE id = ?`, id).Scan(&disabledAt)
	if err == sql.ErrNoRows {
		return false, false, nil
	}
	if err != nil {
		return false, false, err
	}
	return true, !disabledAt.Valid, nil
}

// DisableOperator marks an operator's login account disabled (off-board).
// Callers must also call auth.SessionStore.DeleteForOperator(id) (H4).
func (s *Store) DisableOperator(id string) error {
	res, err := s.DB.Exec(`UPDATE operators SET disabled_at = ? WHERE id = ?`, Now(), id)
	if err != nil {
		return err
	}
	return checkRowsAffected(res)
}

func checkRowsAffected(res sql.Result) error {
	n, err := res.RowsAffected()
	if err != nil {
		return err
	}
	if n == 0 {
		return sql.ErrNoRows
	}
	return nil
}
