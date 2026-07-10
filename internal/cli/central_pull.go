// Central-mode (P5) fetch-and-verify for `skill pull`/`kit pull` when a unit
// is not published in THIS machine's local store. Both commands previously
// printed "(central mode = [Sau])" and stopped; this file gives them the
// network path — reusing every existing local-pull safety check
// (skillreg.Verify / verifyKitManifest, KitLocalPath/LocalPath symlink-safe,
// deny-list-first) and adding the two checks a network response needs that a
// local DB read never did: the byte-hash gate (M) and the signed-checkpoint
// anchor (P1). See internal/ingest/skill_kit_anchor.go for why both are
// required and what each specifically stops.
//
// A central pull does NOT create a local knowledge_units row for the fetched
// unit (that would mean replicating the whole review/publish pipeline across
// machines — explicitly out of scope here, see plan.md's "--prune orphan"
// note for the same out-of-scope boundary applied to a different feature).
// Consequently RecordUnitAdoption (which requires a local unit_id FK) is
// skipped for central pulls; the audit_log append (FK-free — subject/detail
// are plain text) still records the pull for the fleet-wide audit trail.
package cli

import (
	"fmt"

	"github.com/phuc-nt/dandori/internal/config"
	"github.com/phuc-nt/dandori/internal/govern"
	"github.com/phuc-nt/dandori/internal/ingest"
	"github.com/phuc-nt/dandori/internal/learn"
	"github.com/phuc-nt/dandori/internal/skillreg"
	"github.com/phuc-nt/dandori/internal/store"
)

// centralKit is the central-pull projection mirroring what the local path
// assembles from skillreg.GetKit + learn.KitFiles: the verified manifest
// (skillreg.Kit shape, so verifyKitManifest/runKitPull's existing per-file
// loop can consume it unchanged) plus the per-file bodies fetched over the
// network.
type centralKit struct {
	Kit   skillreg.Kit
	Files []learn.KitFileRow
}

// centralPullSkill fetches, verifies, and returns a Skill ready for the same
// write path runKitPull's skill counterpart (skillPullCmd) already runs
// locally. Returns skillreg.ErrNotFound unchanged when the server itself
// reports the unit unknown, so the caller's existing "not published"
// messaging needs no special-casing for the central branch.
func centralPullSkill(cfg *config.Config, key string) (*skillreg.Skill, error) {
	c := ingest.NewClient(cfg)
	resp, err := c.FetchSkill(key)
	if err != nil {
		return nil, fmt.Errorf("central pull: fetch skill %q: %w", key, err)
	}
	if err := ingest.VerifySkillFetch(c, resp); err != nil {
		return nil, fmt.Errorf("central pull: %q failed verification — nothing written: %w", key, err)
	}
	return &skillreg.Skill{
		Name: resp.Name, Body: resp.Body, Hash: resp.ContentHash, Required: resp.Required,
	}, nil
}

// centralPullKit fetches, verifies, and returns a centralKit ready for the
// same per-file verify + write loop runKitPull already runs locally.
// Returns skillreg.ErrNotFound unchanged when the server reports the unit
// unknown.
func centralPullKit(cfg *config.Config, key string) (*centralKit, error) {
	c := ingest.NewClient(cfg)
	resp, err := c.FetchKit(key)
	if err != nil {
		return nil, fmt.Errorf("central pull: fetch kit %q: %w", key, err)
	}
	if err := ingest.VerifyKitFetch(c, resp); err != nil {
		return nil, fmt.Errorf("central pull: %q failed verification — nothing written: %w", key, err)
	}

	manifest, err := learn.ParseKitManifest(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("central pull: %q manifest did not parse: %w", key, err)
	}

	files := make([]learn.KitFileRow, 0, len(resp.Files))
	for _, f := range resp.Files {
		mf, ok := manifest.FileByPath(f.Path)
		if !ok {
			return nil, fmt.Errorf("central pull: %q — file %q served but not listed in its own manifest", key, f.Path)
		}
		// ContentHash/Size come from the manifest entry, not recomputed here:
		// the per-file hash check that actually matters (received body bytes
		// vs. this hash) runs downstream in runKitPull's existing loop, which
		// already recomputes sha256(f.Body) and compares — passing the
		// manifest's own hash through means that comparison is meaningful
		// (network bytes vs. what the manifest claims) rather than trivially
		// true (network bytes vs. itself).
		files = append(files, learn.KitFileRow{Path: f.Path, Body: f.Body, ContentHash: mf.ContentHash, Size: mf.Size})
	}

	return &centralKit{
		Kit:   skillreg.Kit{Name: resp.Name, Body: resp.Body, Hash: resp.ContentHash},
		Files: files,
	}, nil
}

// centralAuditFallback appends the skill_pulled/kit_pulled audit row for a
// central pull, where no local unit_id exists to pass RecordUnitAdoption
// (see file doc comment). unitDetail should already contain whatever
// identifying detail the local path's own Append call records (name/hash),
// plus "source=central" so the audit trail can distinguish the two paths.
func centralAuditFallback(st *store.Store, actor, action, subject, detail string) error {
	a := &govern.Audit{St: st, Actor: actor}
	_, err := a.Append(action, subject, detail+" source=central")
	return err
}
