package ingest

import (
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"time"

	"github.com/phuc-nt/dandori/internal/govern"
)

// Policy cache: the dev machine pulls governance state (rules, kill, bands,
// budget) and evaluates pre-tool checks locally. Freshness tiers:
//
//	fresh (< TTL)            → use as-is, no network
//	stale + refresh works    → refreshed
//	stale + server down      → use stale (better than blind)
//	no cache + server down   → nil; caller denies mutating tools only
//	                           (the narrow fail-closed path, red-team C3)
const policyTTL = 30 * time.Second

func policyCachePath() string {
	home, _ := os.UserHomeDir()
	return filepath.Join(home, ".dandori", "policy_cache.json")
}

type cachedPolicy struct {
	Snapshot govern.PolicySnapshot `json:"snapshot"`
	CachedAt time.Time             `json:"cached_at"`
}

// Policy returns the freshest snapshot available, or nil when there has
// never been one and the server is unreachable.
func (c *Client) Policy() *govern.PolicySnapshot {
	if cp := readPolicyCache(); cp != nil && time.Since(cp.CachedAt) < policyTTL {
		return &cp.Snapshot
	}
	if snap := c.fetchPolicy(); snap != nil {
		writePolicyCache(snap)
		return snap
	}
	if cp := readPolicyCache(); cp != nil {
		return &cp.Snapshot // stale beats blind
	}
	return nil
}

func (c *Client) fetchPolicy() *govern.PolicySnapshot {
	req, err := http.NewRequest(http.MethodGet, c.cfg.ServerURL+"/ingest/policy", nil)
	if err != nil {
		return nil
	}
	c.authorize(req)
	resp, err := c.http.Do(req)
	if err != nil {
		return nil
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil
	}
	var snap govern.PolicySnapshot
	if json.NewDecoder(resp.Body).Decode(&snap) != nil {
		return nil
	}
	return &snap
}

func readPolicyCache() *cachedPolicy {
	b, err := os.ReadFile(policyCachePath())
	if err != nil {
		return nil
	}
	var cp cachedPolicy
	if json.Unmarshal(b, &cp) != nil {
		return nil
	}
	return &cp
}

func writePolicyCache(snap *govern.PolicySnapshot) {
	b, err := json.Marshal(cachedPolicy{Snapshot: *snap, CachedAt: time.Now()})
	if err != nil {
		return
	}
	path := policyCachePath()
	_ = os.MkdirAll(filepath.Dir(path), 0o755)
	_ = os.WriteFile(path, b, 0o600)
}
