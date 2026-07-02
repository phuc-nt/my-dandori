package integrations

import (
	"path/filepath"
	"testing"

	"github.com/phuc-nt/dandori/internal/config"
	"github.com/phuc-nt/dandori/internal/store"
)

func TestGuardStates(t *testing.T) {
	st, err := store.Open(filepath.Join(t.TempDir(), "g.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	cfg, _ := config.Load(filepath.Join(t.TempDir(), "no.yaml"))

	cases := []struct {
		name          string
		dry, disabled bool
		want          bool
	}{
		{"live", false, false, true},
		{"dry-run", true, false, false},
		{"disabled", false, true, false},
		{"disabled wins over dry", true, true, false},
	}
	for _, c := range cases {
		cfg.DryRun, cfg.AgentWriteDisabled = c.dry, c.disabled
		g := &Guard{Cfg: cfg, St: st}
		if got := g.Allow("test action", "detail"); got != c.want {
			t.Errorf("%s: Allow = %v, want %v", c.name, got, c.want)
		}
	}
	// Skipped writes must leave an event trail.
	var n int
	st.DB.QueryRow(`SELECT count(*) FROM events WHERE kind = 'write_skipped'`).Scan(&n)
	if n != 3 {
		t.Errorf("write_skipped events: %d, want 3", n)
	}
}
