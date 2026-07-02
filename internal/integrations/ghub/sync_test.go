package ghub

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/phuc-nt/dandori/internal/store"
)

// fakeGH puts a stub `gh` on PATH that prints the fixture PR list.
func fakeGH(t *testing.T, fixture string) {
	t.Helper()
	dir := t.TempDir()
	script := "#!/bin/sh\ncat <<'EOF'\n" + fixture + "\nEOF\n"
	if err := os.WriteFile(filepath.Join(dir, "gh"), []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
}

func TestSyncPRsRevertMapping(t *testing.T) {
	st, err := store.Open(filepath.Join(t.TempDir(), "g.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	fixture := `[
	  {"number":1,"title":"feat: a","state":"MERGED","author":{"login":"p"},"createdAt":"2026-06-01T00:00:00Z","mergedAt":"2026-06-02T00:00:00Z","body":""},
	  {"number":2,"title":"Revert \"feat: a\"","state":"MERGED","author":{"login":"p"},"createdAt":"2026-06-03T00:00:00Z","mergedAt":"2026-06-03T01:00:00Z","body":"Reverts owner/repo#1"},
	  {"number":3,"title":"feat: b","state":"OPEN","author":{"login":"q"},"createdAt":"2026-06-04T00:00:00Z","mergedAt":"","body":""}
	]`
	fakeGH(t, fixture)

	n, err := SyncPRs(st, "owner/repo")
	if err != nil {
		t.Fatal(err)
	}
	if n != 3 {
		t.Errorf("synced: %d", n)
	}
	var payload string
	st.DB.QueryRow(`SELECT payload FROM work_items WHERE source='github' AND key='owner/repo#1'`).Scan(&payload)
	var p PR
	json.Unmarshal([]byte(payload), &p)
	if p.RevertedBy != 2 {
		t.Errorf("PR#1 reverted_by = %d, want 2 (payload %s)", p.RevertedBy, payload)
	}
	st.DB.QueryRow(`SELECT payload FROM work_items WHERE source='github' AND key='owner/repo#2'`).Scan(&payload)
	json.Unmarshal([]byte(payload), &p)
	if p.RevertOf != 1 {
		t.Errorf("PR#2 revert_of = %d, want 1", p.RevertOf)
	}
	// Idempotent resync.
	if _, err := SyncPRs(st, "owner/repo"); err != nil {
		t.Fatal(err)
	}
	var count int
	st.DB.QueryRow(`SELECT count(*) FROM work_items WHERE source='github'`).Scan(&count)
	if count != 3 {
		t.Errorf("duplicated on resync: %d", count)
	}
}

func TestRevertTargetPatterns(t *testing.T) {
	cases := []struct {
		body string
		want int
	}{
		{"Reverts owner/repo#42", 42},
		{"reverts #7", 7},
		{"no reference here", 0},
		{"Reverts other/repo#9", 0}, // cross-repo reference must not map locally
	}
	for _, c := range cases {
		if got := revertTarget(c.body, "owner/repo"); got != c.want {
			t.Errorf("revertTarget(%q) = %d, want %d", c.body, got, c.want)
		}
	}
}
