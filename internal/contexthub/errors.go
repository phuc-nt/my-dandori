package contexthub

import "strings"

// isUniqueViolation reports whether err is a SQLite UNIQUE constraint failure
// (modernc.org/sqlite surfaces these as a message string). Used to retry a
// version_n collision from a concurrent SaveContext.
func isUniqueViolation(err error) bool {
	return err != nil && strings.Contains(err.Error(), "UNIQUE constraint failed")
}
