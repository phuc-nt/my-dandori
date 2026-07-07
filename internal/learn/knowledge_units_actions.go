package learn

import (
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/phuc-nt/dandori/internal/store"
)

// Rule lifecycle intents (H1). "enable" is the default publish-and-turn-on
// path every other kind uses; "retire" and "scope-up" are the two lifecycle
// signals detectRuleLifecycle nominates (knowledge_detect.go) — each must
// execute a DIFFERENT effect at apply time, never the plain "enable" UPDATE,
// or an approved "gỡ rule" silently re-enables the very rule the human just
// asked to remove.
const (
	RuleIntentEnable  = "enable"
	RuleIntentRetire  = "retire"
	RuleIntentScopeUp = "scope-up"
)

// RuleIntentFromName derives the pinned rule_intent from the unit's slug —
// detectRuleLifecycle names its two lifecycle nominations deterministically
// ("rule-<id>-retire" / "rule-<id>-scope-up"); any other rule nomination
// (a brand-new rule body, RefID==0) is a plain publish-and-enable. Exported
// so the web layer's knowledge-detail template can show the same prominent
// intent banner /reviews shows, without duplicating the suffix logic.
func RuleIntentFromName(name string) string {
	switch {
	case strings.HasSuffix(name, "-retire"):
		return RuleIntentRetire
	case strings.HasSuffix(name, "-scope-up"):
		return RuleIntentScopeUp
	default:
		return RuleIntentEnable
	}
}

// SubmitForReview moves a nominated unit into admin review. INTERNAL write.
func SubmitForReview(st *store.Store, unitID int64, actor string) error {
	return transition(st, unitID, StateNominated, StateInReview, actor, "submitted for review")
}

// RejectUnit moves a unit to rejected with a reason. INTERNAL write, valid
// from nominated or in_review.
func RejectUnit(st *store.Store, unitID int64, actor, why string) error {
	u, err := GetUnit(st, unitID)
	if err != nil {
		return err
	}
	if u == nil {
		return fmt.Errorf("unit %d not found", unitID)
	}
	if u.State != StateNominated && u.State != StateInReview {
		return fmt.Errorf("unit %d in state %q cannot be rejected", unitID, u.State)
	}
	return transition(st, unitID, u.State, StateRejected, actor, why)
}

// RequestActionFunc matches observer.RequestAction's signature. Injected by
// the caller (web/CLI layer) so this package never imports observer (which
// already imports learn — a reverse import would cycle).
type RequestActionFunc func(st *store.Store, typ, subject, summary string, params map[string]any, requestedBy, surface string) (int64, error)

// unitActionParams is the RequestAction evidence shape for all three
// knowledge-* action types: enough for the applier to act (unit_id/kind/
// name/ref) plus, for EVERY body-carrying unit (skill always; context/rule
// when the detector proposed brand-new text with no existing ref — P2's
// RefID==0-with-Body case; playbook has no body), the PINNED body+
// content_hash so /reviews can render the full content at approval time (F1)
// regardless of what the live row looks like by then, and so
// gatedKnowledgeWrite (which requires ev.Body for context/rule-without-ref)
// actually receives it (C1 — this was skill-only and left every
// body-carrying context/rule permanently stuck: F1 blind-approve + apply
// dead-end).
type unitActionParams struct {
	UnitID      int64  `json:"unit_id"`
	Kind        string `json:"kind"`
	Name        string `json:"name"`
	RefKind     string `json:"ref_kind,omitempty"`
	RefID       int64  `json:"ref_id,omitempty"`
	Body        string `json:"body,omitempty"`
	ContentHash string `json:"content_hash,omitempty"`
	Layer       string `json:"layer,omitempty"`
	LayerTarget string `json:"layer_target,omitempty"`
	RuleIntent  string `json:"rule_intent,omitempty"`
	// Origin/OriginModel (v13 P2 anti-Goodhart badge) are PINNED here the same
	// way Body/ContentHash are (F1 rationale above) so the /reviews card can
	// show a reviewer who/what authored the content at approval time, without
	// re-reading the live (possibly since-changed) knowledge_units row.
	Origin      string `json:"origin,omitempty"`
	OriginModel string `json:"origin_model,omitempty"`
}

func actionParams(u *KnowledgeUnit) map[string]any {
	p := unitActionParams{
		UnitID: u.ID, Kind: u.Kind, Name: u.Name,
		RefKind: u.RefKind, Layer: u.Layer, LayerTarget: u.LayerTarget,
		Origin: u.Origin, OriginModel: u.OriginModel,
	}
	if u.RefID != nil {
		p.RefID = *u.RefID
	}
	if u.Body != "" {
		p.Body = u.Body
		p.ContentHash = u.ContentHash
	}
	if u.Kind == KindRule {
		p.RuleIntent = RuleIntentFromName(u.Name)
	}
	b, _ := json.Marshal(p)
	var m map[string]any
	_ = json.Unmarshal(b, &m)
	return m
}

// ErrRetireProposalNotPublishable marks a RequestPublish call against a
// NominateRetireProposals draft (M2) — its RefKind is the RefKindRetireTarget
// sentinel, never a real target for a live publish. The correct action for
// one of these is retiring the TARGET unit (RefID) directly.
var ErrRetireProposalNotPublishable = errors.New("retire-proposal units cannot be published — retire the target unit instead")

// RequestPublish opens a gated "knowledge-publish" approval. Does NOT change
// unit state — the applier (P3) moves state to published (and, for a
// supersedes_id chain, auto-supersedes the prior live unit) after approval.
// M2: refuses outright for a retire-proposal draft (RefKind ==
// RefKindRetireTarget) — those units exist only to surface a signal at
// /knowledge, never to go live themselves.
func RequestPublish(st *store.Store, req RequestActionFunc, unitID int64, actor string) (int64, error) {
	u, err := GetUnit(st, unitID)
	if err != nil {
		return 0, err
	}
	if u == nil {
		return 0, fmt.Errorf("unit %d not found", unitID)
	}
	if u.RefKind == RefKindRetireTarget {
		return 0, ErrRetireProposalNotPublishable
	}
	return requestUnitAction(st, req, unitID, actor, "knowledge-publish", "Đề xuất publish tri thức", false)
}

// ErrUnitNotLive marks an L2 request-time rejection: RequestMandate/
// RequestRetire were called against a unit that is not currently in one of
// the live states (published/adopted/measured) the applier itself requires
// (applyKnowledgeMandate/applyKnowledgeRetire, knowledge_apply.go). Without
// this check, a direct admin POST against a draft (nominated/in_review) or
// an already-retired/superseded/rejected unit opens an approval that is
// doomed to dead-end at apply time with no explanation — same class of bug
// as M2's retire-proposal gate, just for a state mismatch instead of a
// wrong-kind draft.
var ErrUnitNotLive = errors.New("unit is not in a live state (published/adopted/measured) — mandate/retire request refused")

func isLiveState(state string) bool {
	switch state {
	case StatePublished, StateAdopted, StateMeasured:
		return true
	default:
		return false
	}
}

// RequestMandate opens a gated "knowledge-mandate" approval. Does NOT
// auto-supersede any prior version (mandate is approved per-version to avoid
// silently downgrading an agent that already pulled a newer one).
func RequestMandate(st *store.Store, req RequestActionFunc, unitID int64, actor string) (int64, error) {
	return requestUnitAction(st, req, unitID, actor, "knowledge-mandate", "Đề xuất bắt buộc tri thức", true)
}

// RequestRetire opens a gated "knowledge-retire" approval.
func RequestRetire(st *store.Store, req RequestActionFunc, unitID int64, actor string) (int64, error) {
	return requestUnitAction(st, req, unitID, actor, "knowledge-retire", "Đề xuất gỡ tri thức", true)
}

// requestUnitAction opens the approval. requireLive (L2) rejects the
// request outright when the unit is not currently published/adopted/
// measured — the same live-state check the applier enforces, moved earlier
// so a doomed request never reaches /reviews at all.
func requestUnitAction(st *store.Store, req RequestActionFunc, unitID int64, actor, typ, summaryPrefix string, requireLive bool) (int64, error) {
	if req == nil {
		return 0, fmt.Errorf("requestAction func not provided")
	}
	u, err := GetUnit(st, unitID)
	if err != nil {
		return 0, err
	}
	if u == nil {
		return 0, fmt.Errorf("unit %d not found", unitID)
	}
	if requireLive && !isLiveState(u.State) {
		return 0, ErrUnitNotLive
	}
	subject := fmt.Sprintf("%s:%s", u.Kind, u.Name)
	summary := fmt.Sprintf("%s — %s (%s v%d)", summaryPrefix, u.Title, u.Kind, u.VersionN)
	return req(st, typ, subject, summary, actionParams(u), actor, "operator")
}
