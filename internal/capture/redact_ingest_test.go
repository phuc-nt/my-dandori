package capture

import (
	"database/sql"
	"strings"
	"testing"
)

// Agents type secrets into commands; the DB must never store the raw value.
func TestAddEventRedactsSecrets(t *testing.T) {
	g := testIngestor(t)
	if _, err := g.EnsureRun("red-1", "/work/p", "", "hook"); err != nil {
		t.Fatal(err)
	}
	payload := `{"command":"curl -H 'Authorization: Bearer sk-secret-token-12345' https://api.x.com"}`
	if _, err := g.AddEvent("red-1", "tool_use", "Bash", sql.NullInt64{}, payload); err != nil {
		t.Fatal(err)
	}
	var stored string
	g.St.DB.QueryRow(`SELECT payload FROM events WHERE run_id='red-1'`).Scan(&stored)
	if strings.Contains(stored, "sk-secret-token-12345") {
		t.Errorf("raw secret persisted: %s", stored)
	}
	if !strings.Contains(stored, "[REDACTED]") {
		t.Errorf("redaction marker missing: %s", stored)
	}
}
