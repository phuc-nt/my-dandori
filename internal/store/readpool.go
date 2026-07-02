package store

import (
	"database/sql"
	"fmt"
	"net/url"
)

// EnableReadPool opens a second, read-only connection pool against the same
// database file. WAL mode allows readers to proceed while the single writer
// connection holds its lock, so console pages and policy GETs never queue
// behind ingest/audit writes. Call once from long-lived processes (serve);
// short-lived hook commands don't need it.
func (s *Store) EnableReadPool() error {
	if s.readDB != nil || s.Path == "" {
		return nil
	}
	dsn := fmt.Sprintf("file:%s?%s", url.PathEscape(s.Path), url.Values{
		"mode":    []string{"ro"},
		"_pragma": []string{"busy_timeout(5000)", "journal_mode(WAL)"},
	}.Encode())
	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		return err
	}
	db.SetMaxOpenConns(4)
	if err := db.Ping(); err != nil {
		db.Close()
		return err
	}
	s.readDB = db
	return nil
}

// Read returns the read-only pool when enabled, else the writer connection —
// callers can always use it for SELECTs without caring about the mode.
func (s *Store) Read() *sql.DB {
	if s.readDB != nil {
		return s.readDB
	}
	return s.DB
}
