// Package store owns the SQLite database: connection, migrations, queries.
// Pure-Go driver (modernc.org/sqlite) — no CGO, cross-compiles anywhere.
package store

import (
	"database/sql"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"time"

	_ "modernc.org/sqlite"
)

type Store struct {
	DB *sql.DB
}

// Open opens (creating if needed) the SQLite file at path with WAL mode,
// a 5s busy timeout and foreign keys on, then applies pending migrations.
func Open(path string) (*Store, error) {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return nil, fmt.Errorf("create db dir: %w", err)
	}
	// _txlock=immediate: transactions take the write lock at BEGIN, so
	// read-then-insert sequences (audit hash-chain tip) are atomic even
	// across processes (hook commands run outside the serve process).
	dsn := fmt.Sprintf("file:%s?%s", url.PathEscape(path), url.Values{
		"_txlock": []string{"immediate"},
		"_pragma": []string{"busy_timeout(5000)", "journal_mode(WAL)", "foreign_keys(1)", "synchronous(NORMAL)"},
	}.Encode())
	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, err
	}
	// Hook processes are short-lived and the server is single-user:
	// one connection avoids SQLITE_BUSY between in-process goroutines.
	db.SetMaxOpenConns(1)
	s := &Store{DB: db}
	if err := s.migrate(); err != nil {
		db.Close()
		return nil, fmt.Errorf("migrate: %w", err)
	}
	return s, nil
}

func (s *Store) Close() error { return s.DB.Close() }

// Now returns the canonical timestamp format stored everywhere (UTC RFC3339).
func Now() string { return time.Now().UTC().Format(time.RFC3339) }

// Setting reads a key from the settings table ("" when absent).
func (s *Store) Setting(key string) string {
	var v string
	_ = s.DB.QueryRow(`SELECT value FROM settings WHERE key = ?`, key).Scan(&v)
	return v
}

// SetSetting upserts a settings key.
func (s *Store) SetSetting(key, value string) error {
	_, err := s.DB.Exec(`INSERT INTO settings(key, value) VALUES(?, ?)
		ON CONFLICT(key) DO UPDATE SET value = excluded.value`, key, value)
	return err
}
