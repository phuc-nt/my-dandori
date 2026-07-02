package store

import (
	"embed"
	"fmt"
	"sort"
)

//go:embed migrations/*.sql
var migrationFS embed.FS

// migrate applies embedded migrations in filename order, tracking progress
// via PRAGMA user_version. Each migration runs in its own transaction.
func (s *Store) migrate() error {
	entries, err := migrationFS.ReadDir("migrations")
	if err != nil {
		return err
	}
	names := make([]string, 0, len(entries))
	for _, e := range entries {
		names = append(names, e.Name())
	}
	sort.Strings(names)

	var version int
	if err := s.DB.QueryRow(`PRAGMA user_version`).Scan(&version); err != nil {
		return err
	}
	for i, name := range names {
		v := i + 1
		if v <= version {
			continue
		}
		sqlBytes, err := migrationFS.ReadFile("migrations/" + name)
		if err != nil {
			return err
		}
		tx, err := s.DB.Begin()
		if err != nil {
			return err
		}
		if _, err := tx.Exec(string(sqlBytes)); err != nil {
			tx.Rollback()
			return fmt.Errorf("migration %s: %w", name, err)
		}
		if _, err := tx.Exec(fmt.Sprintf("PRAGMA user_version = %d", v)); err != nil {
			tx.Rollback()
			return err
		}
		if err := tx.Commit(); err != nil {
			return err
		}
	}
	return nil
}
