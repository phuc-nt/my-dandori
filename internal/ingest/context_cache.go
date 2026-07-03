package ingest

import (
	"encoding/json"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"time"

	"github.com/phuc-nt/dandori/internal/contexthub"
)

// Context cache mirrors the policy cache, keyed per agent. Freshness tiers:
//
//	fresh (< TTL)         → use cached
//	stale + refresh works → refresh
//	stale + server down   → use stale (a rolled-back bad version lingers at
//	                        most contextTTL — the exposure window; acceptable
//	                        for advisory guidance)
//	no cache + server down → "" (no context; session starts fine — fail OPEN,
//	                          because context is guidance, not a gate)
//
// A non-200 (incl. 500 on server DB error) is treated as unreachable so a
// good stale cache is never overwritten with empty (M4).
const contextTTL = 30 * time.Second

func contextCachePath() string {
	home, _ := os.UserHomeDir()
	return filepath.Join(home, ".dandori", "context_cache.json")
}

type cachedContext struct {
	Text     string                `json:"text"`
	Prov     contexthub.Provenance `json:"prov"`
	CachedAt time.Time             `json:"cached_at"`
}

// Context returns the freshest effective context for an agent, or ("", zero)
// when there is no cache and the server is unreachable.
func (c *Client) Context(agent string) (string, contexthub.Provenance) {
	all := readContextCache()
	if cc, ok := all[agent]; ok && time.Since(cc.CachedAt) < contextTTL {
		return cc.Text, cc.Prov
	}
	if cc := c.fetchContext(agent); cc != nil {
		all[agent] = *cc
		writeContextCache(all)
		return cc.Text, cc.Prov
	}
	if cc, ok := all[agent]; ok {
		return cc.Text, cc.Prov // stale beats blind
	}
	return "", contexthub.Provenance{}
}

func (c *Client) fetchContext(agent string) *cachedContext {
	req, err := http.NewRequest(http.MethodGet, c.cfg.ServerURL+"/ingest/context?agent="+url.QueryEscape(agent), nil)
	if err != nil {
		return nil
	}
	c.authorize(req)
	resp, err := c.http.Do(req)
	if err != nil {
		return nil
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK { // 500 on server DB error → keep stale (M4)
		return nil
	}
	var body struct {
		Context    string                `json:"context"`
		Provenance contexthub.Provenance `json:"provenance"`
	}
	if json.NewDecoder(resp.Body).Decode(&body) != nil {
		return nil
	}
	return &cachedContext{Text: body.Context, Prov: body.Provenance, CachedAt: time.Now()}
}

func readContextCache() map[string]cachedContext {
	out := map[string]cachedContext{}
	b, err := os.ReadFile(contextCachePath())
	if err != nil {
		return out
	}
	_ = json.Unmarshal(b, &out)
	return out
}

func writeContextCache(all map[string]cachedContext) {
	b, err := json.Marshal(all)
	if err != nil {
		return
	}
	path := contextCachePath()
	_ = os.MkdirAll(filepath.Dir(path), 0o755)
	_ = os.WriteFile(path, b, 0o600)
}
