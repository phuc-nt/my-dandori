// Package integrations holds the external-service legs (Jira, Slack, GitHub)
// and the single write gate they all pass through.
package integrations

import (
	"log"

	"github.com/phuc-nt/dandori/internal/config"
	"github.com/phuc-nt/dandori/internal/store"
)

// Guard is the one gate for every outbound write. Order of authority:
// AGENT_WRITE_DISABLED kills all writes; DRY_RUN logs what would happen.
type Guard struct {
	Cfg *config.Config
	St  *store.Store
}

// Allow reports whether a real external write may proceed. When it returns
// false the caller must treat the operation as done (logged, not sent).
func (g *Guard) Allow(action, detail string) bool {
	switch {
	case g.Cfg.AgentWriteDisabled:
		g.logSkip("AGENT_WRITE_DISABLED", action, detail)
		return false
	case g.Cfg.DryRun:
		g.logSkip("DRY_RUN", action, detail)
		return false
	}
	return true
}

func (g *Guard) logSkip(gate, action, detail string) {
	msg := "[" + gate + "] would " + action + ": " + detail
	log.Println("integrations:", msg)
	if g.St != nil {
		_, _ = g.St.DB.Exec(`INSERT INTO events(run_id, ts, kind, tool_name, ok, payload)
			VALUES(NULL, ?, 'write_skipped', ?, 1, ?)`, store.Now(), action, msg)
	}
}
