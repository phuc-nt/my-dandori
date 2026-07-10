package ingest

import (
	"sync"
	"time"

	"github.com/phuc-nt/dandori/internal/govern"
)

// policySnapshotTTL bounds how stale a served snapshot can be. The client
// already caches for 30s (policy_cache.go's policyTTL) on top of this — this
// TTL only protects the SERVER from rebuilding (rescoring every active run
// via RiskScoreCentral) on every single poll from every machine in the fleet.
// Short enough that G5's log-mode observation and G4 gate rules still feel
// near-real-time to an operator watching the console.
const policySnapshotTTL = 15 * time.Second

// policySnapshotCache is a small per-operator, TTL-bounded rebuild cache for
// BuildPolicySnapshot. It exists purely to fix the "rebuild on every request"
// contention/cost problem (G) — handlePolicy would otherwise recompute every
// active run's risk score, for every operator, on every fleet machine's poll.
type policySnapshotCache struct {
	mu   sync.Mutex
	byOp map[string]*cachedSnapshot
}

type cachedSnapshot struct {
	snap    *govern.PolicySnapshot
	builtAt time.Time
}

func newPolicySnapshotCache() *policySnapshotCache {
	return &policySnapshotCache{byOp: map[string]*cachedSnapshot{}}
}

// get returns the cached snapshot for operatorID if it is still within TTL,
// otherwise calls build, caches the result, and returns it. build errors are
// never cached — a transient DB error must not poison the cache for the next
// TTL window.
func (c *policySnapshotCache) get(operatorID string, build func() (*govern.PolicySnapshot, error)) (*govern.PolicySnapshot, error) {
	c.mu.Lock()
	if cs, ok := c.byOp[operatorID]; ok && time.Since(cs.builtAt) < policySnapshotTTL {
		c.mu.Unlock()
		return cs.snap, nil
	}
	c.mu.Unlock()

	snap, err := build()
	if err != nil {
		return nil, err
	}

	c.mu.Lock()
	c.byOp[operatorID] = &cachedSnapshot{snap: snap, builtAt: time.Now()}
	c.mu.Unlock()
	return snap, nil
}
