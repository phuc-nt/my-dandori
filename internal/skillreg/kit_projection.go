// Kit is the pull-facing projection of a published kind=kit knowledge unit
// and the KitPublished/GetKit lookups — the kit-pull (P5) counterpart to
// Skill/Published/Get. Kept in its own file (mirrors kit_path.go) rather
// than growing skillreg.go past its existing size.
package skillreg

import (
	"github.com/phuc-nt/dandori/internal/learn"
	"github.com/phuc-nt/dandori/internal/store"
)

// Kit is the pull-facing projection of a published kit-kind knowledge unit.
// Body is the canonical KitManifest JSON (learn.ParseKitManifest parses it);
// the per-file bodies live in knowledge_kit_files, loaded separately via
// learn.KitFiles — Kit itself only carries what `kit list`/`kit pull` need
// to identify and verify the manifest as a whole.
type Kit struct {
	UnitID int64
	Name   string
	Body   string // canonical KitManifest JSON
	Hash   string // content_hash pinned on the knowledge_units row
}

// KitPublished lists all currently-published kit-kind knowledge units.
func KitPublished(st *store.Store) ([]Kit, error) {
	units, err := learn.ListUnits(st, learn.StatePublished)
	if err != nil {
		return nil, err
	}
	out := make([]Kit, 0, len(units))
	for _, u := range units {
		if u.Kind != learn.KindKit {
			continue
		}
		out = append(out, Kit{UnitID: u.ID, Name: u.Name, Body: u.Body, Hash: u.ContentHash})
	}
	return out, nil
}

// GetKit fetches one published kit unit by name or numeric unit-id. Mirrors
// Get's F3 fail-open contract: ErrNotFound (not a crash) when the unit does
// not exist, is not kind=kit, or is not in state=published.
func GetKit(st *store.Store, key string) (*Kit, error) {
	var u *learn.KnowledgeUnit
	if id, err := parseUnitID(key); err == nil {
		got, err := learn.GetUnit(st, id)
		if err != nil {
			return nil, err
		}
		u = got
	} else {
		units, err := learn.ListUnits(st, learn.StatePublished)
		if err != nil {
			return nil, err
		}
		for i := range units {
			if units[i].Kind == learn.KindKit && units[i].Name == key {
				u = &units[i]
				break
			}
		}
	}
	if u == nil || u.Kind != learn.KindKit || u.State != learn.StatePublished {
		return nil, ErrNotFound
	}
	return &Kit{UnitID: u.ID, Name: u.Name, Body: u.Body, Hash: u.ContentHash}, nil
}
