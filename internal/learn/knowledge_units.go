package learn

import (
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"strings"

	"github.com/phuc-nt/dandori/internal/contexthub"
	"github.com/phuc-nt/dandori/internal/store"
)

// Knowledge Units: ONE pipeline envelope (state machine + review + adoption +
// provenance) wrapping the existing distribution rails. kind ∈
// {context, rule, playbook, skill}. context/rule/playbook are ref-not-
// duplicate (ref_kind+ref_id point at the existing store); skill's body lives
// here because it is a genuinely new surface. Content is IMMUTABLE after
// approve — an edit is a new row with supersedes_id, not an in-place update.
//
// This package never calls the observer package directly (observer already
// imports learn — a reverse import would cycle). RequestPublish/Mandate/
// Retire take a requestAction func value with the same shape as
// observer.RequestAction; callers in the web/CLI layer (which import both
// packages) pass observer.RequestAction in.

// Knowledge unit kinds.
const (
	KindContext  = "context"
	KindRule     = "rule"
	KindPlaybook = "playbook"
	KindSkill    = "skill"
)

// Knowledge unit states.
const (
	StateDetected   = "detected"
	StateNominated  = "nominated"
	StateInReview   = "in_review"
	StatePublished  = "published"
	StateAdopted    = "adopted"
	StateMeasured   = "measured"
	StateRejected   = "rejected"
	StateRetired    = "retired"
	StateRolledBack = "rolled_back"
	StateSuperseded = "superseded"
)

// RefKindRetireTarget marks a NominateRetireProposals draft (M2): its RefKind
// is this sentinel (not the target unit's own kind) so RequestPublish can
// refuse to open a "knowledge-publish" approval for it. A retire-proposal is
// a SIGNAL for a human to review, never a unit meant to go live itself — its
// RefID still points at the target unit (so the review UI can link to it),
// but "approved intent" for this unit type means "go retire the target,"
// never "publish this draft." Approving one via /reviews (before this fix)
// could otherwise create a real playbooks row for a proposal whose body is
// empty, or dead-end at apply for skill/context (errPermanentApply, audit
// noise) — same class of bug as H1's "approved intent ≠ executed effect."
const RefKindRetireTarget = "retire_target"

// MaxUnitBodySize caps skill-kind body content (F9): large bodies bloat the
// review queue and the DB row; 64KB comfortably fits a real SKILL.md.
const MaxUnitBodySize = 64 * 1024

// MaxSlugLen caps a nominate-time (kind,name) slug (M6): it is used as a
// path/compliance/match key downstream (P5 skill pull, P6 mandate
// hash-check) and as a URL segment (/knowledge/unit/{id} does not embed it,
// but detector-generated slugs like playbookSlug(runID) could otherwise grow
// unbounded from a long run id) — 64 chars is generous for a kebab-case name
// while keeping it a sane index/URL key.
const MaxSlugLen = 64

// ErrDuplicateDraft is returned by NominateUnit when a draft (nominated or
// in_review) already exists for the same (kind,name) — the caller-visible
// half of M1's race-safe dedup (idx_ku_kind_name_draft, migration 016). A
// batch caller like DetectKnowledgeUnits (M2) uses this to distinguish "this
// one candidate was already proposed, skip it" from a real failure that
// must propagate instead of being silently absorbed into a skip count.
var ErrDuplicateDraft = errors.New("a draft is already pending review")

// ErrStateMismatch marks an apply-time state-check failure (concurrent
// transition raced ahead, or the target row is gone) — the H2 caller
// classifies this as permanent (never retryable), same as a UNIQUE
// violation, since retrying cannot make a stale state fresh again.
var ErrStateMismatch = errors.New("unit not in expected state")

// isUniqueViolation reports whether err is a SQLite UNIQUE constraint
// failure (modernc.org/sqlite surfaces these as a message string) — mirrors
// contexthub.isUniqueViolation; duplicated here (2-line string match) rather
// than exported cross-package, since the two packages have no other shared
// error-classification surface worth a new dependency for.
func isUniqueViolation(err error) bool {
	return err != nil && strings.Contains(err.Error(), "UNIQUE constraint failed")
}

// MinSampleForKnowledge is the nominate-gate floor for org-wide knowledge
// proposals (F17) — deliberately higher than MinSampleForInsight=3, which is
// meant for a single dashboard observation, not "propose this to the whole
// org." A 3-vs-3 split is too thin to ask an admin to review and publish.
const MinSampleForKnowledge = 10

var slugRe = regexp.MustCompile(`^[a-z0-9][a-z0-9-]*$`)

// ValidSlug reports whether name is a valid knowledge-unit slug: kebab-case,
// starting with a lowercase letter or digit, at most MaxSlugLen chars (M6).
// It is the path/compliance/match key downstream (P5 skill pull, P6 mandate
// hash-check), so it must be filesystem- and URL-safe.
func ValidSlug(name string) bool {
	return name != "" && len(name) <= MaxSlugLen && slugRe.MatchString(name)
}

func validKind(k string) bool {
	switch k {
	case KindContext, KindRule, KindPlaybook, KindSkill, KindKit:
		return true
	}
	return false
}

// StatsSnapshot is the nominate-time audit/gate snapshot (F11: the live
// suggest card recomputes fresh — this is only for the review record).
type StatsSnapshot struct {
	NPresent, NAbsent        int
	DonePresent, DoneAbsent  float64
	CIPresentLo, CIPresentHi int
	CIAbsentLo, CIAbsentHi   int
	CostPresent, CostAbsent  float64
}

// NominateParams is the input to NominateUnit. Exactly one of RefID (for
// context/rule/playbook) or Body (for skill) should be set, matched to Kind.
type NominateParams struct {
	Kind          string
	Name          string // slug
	Title         string
	RefKind       string
	RefID         int64
	Body          string // skill only
	Layer         string
	LayerTarget   string
	Stats         StatsSnapshot
	ProvenanceRun []string
	NominatedBy   string
	// Origin labels how this unit came to exist (v13 anti-Goodhart badge):
	// "human" (default — empty string here means the DB DEFAULT 'human'
	// applies, migration 017), "import-memory"/"import-journal" (dandori
	// knowledge import), "ai-draft" (LLM-draft assistant, P3 — OriginModel
	// carries the model id), or "detector" (auto-nominated candidates).
	Origin      string
	OriginModel string
	// TransitionNote overrides the default "nominated" note recorded on the
	// detected→nominated transition row (YAGNI: no new column — the spec
	// asks for "source path in transition note," and knowledge_transitions.
	// note already exists for exactly this purpose). Empty keeps the default.
	TransitionNote string
}

// NominateUnit creates a new pipeline row in state=nominated. INTERNAL write
// (no approval, no external side effect) — matches the observer "auto"
// class. Validates the name slug, caps skill body size, scans for secret-
// shaped content, and computes version_n/supersedes_id for the (kind,name)
// lineage.
func NominateUnit(st *store.Store, p NominateParams) (int64, error) {
	if !validKind(p.Kind) {
		return 0, fmt.Errorf("unknown kind %q", p.Kind)
	}
	if !ValidSlug(p.Name) {
		return 0, fmt.Errorf("invalid name slug %q — must match ^[a-z0-9][a-z0-9-]*$", p.Name)
	}
	if p.Title == "" {
		return 0, fmt.Errorf("title required")
	}
	// A retire-proposal draft (M2, RefKind == RefKindRetireTarget) is a
	// signal-only card pointing at a live unit — it never carries its own
	// body/ref content regardless of Kind (NominateRetireProposals mirrors
	// the target unit's Kind purely so the card sorts/displays alongside its
	// own kind's queue section), so the per-kind body/ref requirements below
	// do not apply to it.
	if p.RefKind != RefKindRetireTarget {
		switch p.Kind {
		case KindSkill:
			if p.Body == "" {
				return 0, fmt.Errorf("skill unit requires body")
			}
			if len(p.Body) > MaxUnitBodySize {
				return 0, fmt.Errorf("body exceeds %d bytes cap", MaxUnitBodySize)
			}
			if frag := contexthub.SecretFragment(p.Body); frag != "" {
				return 0, fmt.Errorf("body contains secret-shaped content: %s", frag)
			}
		case KindContext, KindRule:
			// Detectors usually observe an existing context_version/guardrail_rule,
			// so a ref must already exist at nominate time. The one exception is a
			// detector PROPOSING brand-new content with no existing row to point
			// at yet (P2 tool-pattern detector: "task dạng K: cân nhắc tool X" is a
			// new one-line suggestion, not a reference to a context_versions row) —
			// that path carries Body instead of RefID, same cap/secret-scan as
			// skill, and P3/human fills in ref_id at publish time by actually
			// writing the context doc.
			if p.RefID == 0 && p.Body == "" {
				return 0, fmt.Errorf("%s unit requires ref_id or body", p.Kind)
			}
			if p.RefID == 0 && p.Body != "" {
				if len(p.Body) > MaxUnitBodySize {
					return 0, fmt.Errorf("body exceeds %d bytes cap", MaxUnitBodySize)
				}
				if frag := contexthub.SecretFragment(p.Body); frag != "" {
					return 0, fmt.Errorf("body contains secret-shaped content: %s", frag)
				}
			}
		case KindPlaybook:
			// A flywheel candidate has no playbooks row yet — the applier creates
			// the real row and backfills ref_id only after knowledge-publish is
			// approved (spec: "Playbook row THẬT chỉ được tạo ở applier"). Neither
			// current caller (PromoteCandidate, handlePlaybookCreate) sets Body —
			// but the cap+secret-scan is applied whenever it IS set anyway (LOW
			// quick win) so a future internal caller can't slip an oversized or
			// secret-shaped playbook body past nominate-time validation the way
			// skill/context/rule already are.
			if p.Body != "" {
				if len(p.Body) > MaxUnitBodySize {
					return 0, fmt.Errorf("body exceeds %d bytes cap", MaxUnitBodySize)
				}
				if frag := contexthub.SecretFragment(p.Body); frag != "" {
					return 0, fmt.Errorf("body contains secret-shaped content: %s", frag)
				}
			}
		}
	}

	tx, err := st.DB.Begin()
	if err != nil {
		return 0, err
	}
	defer tx.Rollback()

	var nextN int
	if err := tx.QueryRow(`SELECT COALESCE(MAX(version_n),0)+1 FROM knowledge_units WHERE kind = ? AND name = ?`,
		p.Kind, p.Name).Scan(&nextN); err != nil {
		return 0, err
	}
	now := store.Now()

	// One draft at a time per (kind,name) — KISS dedup. Viewers may nominate
	// (F9), so without this a flood of junk nominations could each try to
	// claim the same slug while a real review is in flight; reject instead of
	// silently superseding anything (that stays an applier-only action, F5).
	// This SELECT-then-INSERT is a fast, friendly pre-check ONLY — it still
	// has a TOCTOU race window between two concurrent nominates for the same
	// (kind,name). The actual guarantee is idx_ku_kind_name_draft (migration
	// 016, M1): a genuine race falls through to the INSERT below, which then
	// fails on that partial UNIQUE index and gets mapped to ErrDuplicateDraft
	// the same as this pre-check.
	var draftID int64
	err = tx.QueryRow(`SELECT id FROM knowledge_units WHERE kind = ? AND name = ?
		AND state IN ('nominated','in_review')`, p.Kind, p.Name).Scan(&draftID)
	if err == nil {
		return 0, fmt.Errorf("%w: %s %q is already pending review (unit #%d)", ErrDuplicateDraft, p.Kind, p.Name, draftID)
	} else if err != sql.ErrNoRows {
		return 0, err
	}

	// supersedes_id is a POINTER ONLY here — it records lineage against the
	// currently published head, if any, so reviewers can see "this replaces
	// vN." It never mutates the published row's state; that transition is an
	// applier-only action (MarkSuperseded, called by P3 when knowledge-publish
	// for THIS unit is approved) — F5: "Publish v2 → v1 tự chuyển superseded"
	// happens at apply time, not at nominate time.
	var supersedes *int64
	var liveID int64
	err = tx.QueryRow(`SELECT id FROM knowledge_units WHERE kind = ? AND name = ?
		AND state IN ('published','adopted','measured')`, p.Kind, p.Name).Scan(&liveID)
	if err == nil {
		supersedes = &liveID
	} else if err != sql.ErrNoRows {
		return 0, err
	}

	prov, err := json.Marshal(p.ProvenanceRun)
	if err != nil {
		return 0, err
	}
	// Empty Origin resolves to "human" in Go rather than relying on the DB
	// column DEFAULT (migration 017): the INSERT below always names the
	// origin column explicitly, and an explicit NULL bound to a DEFAULT
	// column does NOT trigger the DEFAULT in SQLite — only omitting the
	// column from the statement does. Resolving here keeps one clear source
	// of truth (Go, not SQL-engine DEFAULT semantics) for "no origin passed
	// == human".
	origin := p.Origin
	if origin == "" {
		origin = "human"
	}
	res, err := tx.Exec(`INSERT INTO knowledge_units(
			kind, name, title, state, version_n, supersedes_id,
			ref_kind, ref_id, body, content_hash, layer, layer_target, required,
			n_present, n_absent, done_present, done_absent,
			ci_present_lo, ci_present_hi, ci_absent_lo, ci_absent_hi,
			cost_present, cost_absent, provenance_run_ids, nominated_by,
			origin, origin_model,
			created_at, updated_at)
		VALUES(?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, 0,
			?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		p.Kind, p.Name, p.Title, StateNominated, nextN, supersedes,
		nullStrIf(p.RefKind), nullInt64If(p.RefID), nullStrIf(p.Body), contentHashIf(p.Kind, p.Body),
		nullStrIf(p.Layer), nullStrIf(p.LayerTarget),
		p.Stats.NPresent, p.Stats.NAbsent, p.Stats.DonePresent, p.Stats.DoneAbsent,
		p.Stats.CIPresentLo, p.Stats.CIPresentHi, p.Stats.CIAbsentLo, p.Stats.CIAbsentHi,
		p.Stats.CostPresent, p.Stats.CostAbsent, string(prov), p.NominatedBy,
		origin, nullStrIf(p.OriginModel),
		now, now)
	if err != nil {
		if isUniqueViolation(err) {
			return 0, fmt.Errorf("%w: %s %q is already pending review", ErrDuplicateDraft, p.Kind, p.Name)
		}
		return 0, err
	}
	id, _ := res.LastInsertId()
	note := p.TransitionNote
	if note == "" {
		note = "nominated"
	}
	if err := recordTransitionTx(tx, id, StateDetected, StateNominated, p.NominatedBy, note); err != nil {
		return 0, err
	}
	if err := tx.Commit(); err != nil {
		return 0, err
	}
	return id, nil
}
