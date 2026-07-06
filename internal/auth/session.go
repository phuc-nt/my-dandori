package auth

import (
	"crypto/rand"
	"database/sql"
	"encoding/base64"
	"errors"
	"fmt"
	"time"

	"github.com/phuc-nt/dandori/internal/store"
)

// Idle timeout: no activity for this long invalidates the session even if
// the absolute timeout hasn't been reached. Absolute: hard ceiling regardless
// of activity — bounds a stolen-but-actively-replayed cookie.
const (
	IdleTimeout     = 20 * time.Minute
	AbsoluteTimeout = 8 * time.Hour
	sessionIDBytes  = 32 // >=256-bit entropy (OWASP minimum is 64-bit)
)

// ErrSessionExpired distinguishes "found but expired" from "not found" for
// callers that want to tell the two apart (both mean "not authenticated").
var ErrSessionExpired = errors.New("auth: session expired")

// Session is one authenticated browser session, loaded fresh on every
// request from SQLite (no server-side cache — single-user tool, DB is fast).
type Session struct {
	ID             string
	OperatorID     string
	Role           string
	CreatedAt      time.Time
	LastActivityAt time.Time
	ExpiresAt      time.Time
}

// SessionStore wraps the shared *store.Store for session lifecycle queries.
type SessionStore struct {
	st *store.Store
}

func NewSessionStore(st *store.Store) *SessionStore { return &SessionStore{st: st} }

// newSessionID returns a URL-safe, base64-encoded 32-byte random token.
func newSessionID() (string, error) {
	b := make([]byte, sessionIDBytes)
	if _, err := rand.Read(b); err != nil {
		return "", fmt.Errorf("generate session id: %w", err)
	}
	return base64.RawURLEncoding.EncodeToString(b), nil
}

// Create starts a new session for operatorID and returns the plaintext id to
// set as the cookie value.
func (ss *SessionStore) Create(operatorID string) (string, error) {
	id, err := newSessionID()
	if err != nil {
		return "", err
	}
	now := store.Now()
	expires := time.Now().UTC().Add(AbsoluteTimeout).Format(time.RFC3339)
	_, err = ss.st.DB.Exec(`INSERT INTO sessions(id, operator_id, created_at, last_activity_at, expires_at)
		VALUES(?, ?, ?, ?, ?)`, id, operatorID, now, now, expires)
	if err != nil {
		return "", err
	}
	return id, nil
}

// Load fetches a session by id, validates idle+absolute expiry, and bumps
// last_activity_at. Returns (nil, nil) when the id is unknown or expired —
// callers treat both as "no session" (redirect to login), never distinguish
// to avoid leaking whether an id ever existed.
func (ss *SessionStore) Load(id string) (*Session, error) {
	if id == "" {
		return nil, nil
	}
	var s Session
	var createdAt, lastActivity, expiresAt string
	err := ss.st.DB.QueryRow(`SELECT sessions.id, sessions.operator_id, sessions.created_at, sessions.last_activity_at, sessions.expires_at, operators.role
		FROM sessions JOIN operators ON operators.id = sessions.operator_id
		WHERE sessions.id = ? AND operators.disabled_at IS NULL`, id).
		Scan(&s.ID, &s.OperatorID, &createdAt, &lastActivity, &expiresAt, &s.Role)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	s.CreatedAt, _ = time.Parse(time.RFC3339, createdAt)
	s.LastActivityAt, _ = time.Parse(time.RFC3339, lastActivity)
	s.ExpiresAt, _ = time.Parse(time.RFC3339, expiresAt)

	now := time.Now().UTC()
	if now.After(s.ExpiresAt) || now.Sub(s.LastActivityAt) > IdleTimeout {
		_ = ss.Delete(id) // best-effort cleanup; expiry stands regardless
		return nil, nil
	}
	if _, err := ss.st.DB.Exec(`UPDATE sessions SET last_activity_at = ? WHERE id = ?`, store.Now(), id); err != nil {
		return nil, err
	}
	s.LastActivityAt = now
	return &s, nil
}

// Delete removes a single session (logout).
func (ss *SessionStore) Delete(id string) error {
	_, err := ss.st.DB.Exec(`DELETE FROM sessions WHERE id = ?`, id)
	return err
}

// DeleteForOperator invalidates every session belonging to operatorID.
// Called by set-password, role change, and operator disable (H4) so a
// stolen or now-invalid session dies immediately instead of at the 8h
// absolute expiry.
func (ss *SessionStore) DeleteForOperator(operatorID string) error {
	_, err := ss.st.DB.Exec(`DELETE FROM sessions WHERE operator_id = ?`, operatorID)
	return err
}

// Rotate deletes oldID and creates a fresh session for the same operator,
// returning the new plaintext id. Used on login to mitigate session
// fixation (an attacker-supplied pre-login cookie must not carry over).
func (ss *SessionStore) Rotate(oldID, operatorID string) (string, error) {
	if oldID != "" {
		if err := ss.Delete(oldID); err != nil {
			return "", err
		}
	}
	return ss.Create(operatorID)
}

// GC deletes sessions past their absolute expiry. Call periodically
// (e.g. hourly) from a background goroutine; safe to call from CLI too.
func (ss *SessionStore) GC() error {
	_, err := ss.st.DB.Exec(`DELETE FROM sessions WHERE expires_at < ?`, store.Now())
	return err
}
