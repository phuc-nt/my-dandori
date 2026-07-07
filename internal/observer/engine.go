// Package observer is the Master Observer: a periodic engine that watches
// the fleet (grades, behavior, budgets, playbooks) and turns what it sees
// into typed insights. Safe conclusions surface to the inbox by themselves
// (INTERNAL-only — never an external write); sensitive ones become approval
// rows a human decides. Same no-bypass contract as the closed loop it
// generalizes: state changes happen ONLY in the applier, after approval,
// consume-once, audited.
package observer

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/mail"
	"os/exec"
	"strconv"
	"strings"
	"time"

	"github.com/phuc-nt/dandori/internal/config"
	"github.com/phuc-nt/dandori/internal/contexthub"
	"github.com/phuc-nt/dandori/internal/govern"
	"github.com/phuc-nt/dandori/internal/integrations"
	"github.com/phuc-nt/dandori/internal/integrations/ghub"
	"github.com/phuc-nt/dandori/internal/integrations/gws"
	"github.com/phuc-nt/dandori/internal/integrations/jira"
	"github.com/phuc-nt/dandori/internal/redact"
	"github.com/phuc-nt/dandori/internal/store"
)

// applyWriteTimeout bounds every external client call made from an apply
// case — a hung Jira/gh/gws call must not wedge the observer applier loop.
const applyWriteTimeout = 30 * time.Second

const actor = "dandori-observer"

// Insight is one observed condition, ready for the inbox or approval queue.
type Insight struct {
	Type     string
	Subject  string
	Summary  string         // plain Vietnamese, redacted before persist
	Evidence map[string]any // metric values + structured action params
	Class    string         // auto | approval
	Surface  string         // ceo | operator
}

type ObserveResult struct {
	Surfaced, Proposed, Applied, Deduped int
	Details                              []string
}

// RunObserver executes one cycle: apply what humans approved, then detect.
func RunObserver(st *store.Store, cfg *config.Config) (*ObserveResult, error) {
	res := &ObserveResult{}
	applied, err := RunObserverApplier(st)
	if err != nil {
		return res, err
	}
	res.Applied = applied

	insights, err := detectAll(st, cfg)
	if err != nil {
		return res, err
	}
	for _, in := range insights {
		if openInsightExists(st, in.Type, in.Subject) {
			res.Deduped++
			continue
		}
		id, err := persistInsight(st, in)
		if err != nil {
			return res, err
		}
		a := &govern.Audit{St: st, Actor: actor}
		switch in.Class {
		case "auto":
			// INTERNAL-only: the insight is now visible in the inbox. No
			// governance state changes, no external write — ever.
			if _, err := st.DB.Exec(`UPDATE insights SET status = 'surfaced' WHERE id = ?`, id); err != nil {
				return res, err
			}
			_, _ = a.Append("observer_surfaced", in.Subject, in.Type)
			res.Surfaced++
		case "approval":
			action := fmt.Sprintf("observer:%s:%d", shortType(in.Type), id)
			ar, err := st.DB.Exec(`INSERT INTO approvals(run_id, action, reason, requested_at)
				VALUES(NULL, ?, ?, ?)`, action, in.Summary, store.Now())
			if err != nil {
				return res, err
			}
			approvalID, _ := ar.LastInsertId()
			if _, err := st.DB.Exec(`UPDATE insights SET approval_id = ? WHERE id = ?`, approvalID, id); err != nil {
				return res, err
			}
			_, _ = a.Append("observer_proposed", in.Subject, action+" — "+in.Summary)
			res.Proposed++
		}
		res.Details = append(res.Details, in.Type+": "+in.Subject)
	}
	return res, nil
}

// shortType maps insight types to the compact action namespace token.
func shortType(t string) string {
	switch t {
	case "budget_overshoot_trend":
		return "budget"
	default:
		return t
	}
}

func openInsightExists(st *store.Store, typ, subject string) bool {
	var n int
	_ = st.Read().QueryRow(`SELECT count(*) FROM insights
		WHERE type = ? AND subject = ? AND status IN ('open','surfaced')`, typ, subject).Scan(&n)
	return n > 0
}

func persistInsight(st *store.Store, in Insight) (int64, error) {
	ev, err := json.Marshal(in.Evidence)
	if err != nil {
		return 0, err
	}
	res, err := st.DB.Exec(`INSERT INTO insights(type, subject, summary, evidence, class, surface, created_at)
		VALUES(?, ?, ?, ?, ?, ?, ?)`,
		in.Type, in.Subject, redact.String(in.Summary), string(ev), in.Class, in.Surface, store.Now())
	if err != nil {
		return 0, err
	}
	return res.LastInsertId()
}

// RunObserverApplier executes human-approved observer actions consume-once.
// Structured params come from insights.evidence — NEVER parsed out of the
// action string (an agent can influence text that ends up in summaries).
func RunObserverApplier(st *store.Store) (int, error) {
	rows, err := st.DB.Query(`SELECT id, action, COALESCE(decided_by,'') FROM approvals
		WHERE action LIKE 'observer:%' AND status = 'approved' AND consumed_at IS NULL`)
	if err != nil {
		return 0, err
	}
	type prop struct {
		id            int64
		action, actor string
	}
	var props []prop
	for rows.Next() {
		var p prop
		if err := rows.Scan(&p.id, &p.action, &p.actor); err != nil {
			rows.Close()
			return 0, err
		}
		props = append(props, p)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return 0, err
	}

	applied := 0
	for _, p := range props {
		parts := strings.Split(p.action, ":") // observer:<type>:<insight_id>
		insightID, convErr := strconv.ParseInt(parts[len(parts)-1], 10, 64)
		if len(parts) != 3 || convErr != nil {
			consumeApproval(st, p.id)
			a := &govern.Audit{St: st, Actor: actor}
			a.Append("observer_malformed", fmt.Sprintf("approval:%d", p.id), p.action)
			continue
		}
		cr, err := st.DB.Exec(`UPDATE approvals SET consumed_at = ? WHERE id = ? AND consumed_at IS NULL`,
			store.Now(), p.id)
		if err != nil {
			return applied, err
		}
		if n, _ := cr.RowsAffected(); n != 1 {
			continue // another worker won the consume race
		}
		if err := applyInsightAction(st, parts[1], insightID, p.actor); err != nil {
			if permanentApplyError(err) {
				// Bad params / gone insight will never succeed — stay
				// consumed, audit, move on (don't loop forever).
				a := &govern.Audit{St: st, Actor: actor}
				a.Append("observer_apply_failed", fmt.Sprintf("approval:%d", p.id), err.Error())
				continue
			}
			// Transient (e.g. DB hiccup): un-consume so it retries.
			st.DB.Exec(`UPDATE approvals SET consumed_at = NULL WHERE id = ?`, p.id)
			return applied, err
		}
		st.DB.Exec(`UPDATE insights SET status = 'resolved', resolved_at = ? WHERE id = ?`, store.Now(), insightID)
		applied++
	}
	return applied, nil
}

func consumeApproval(st *store.Store, id int64) {
	st.DB.Exec(`UPDATE approvals SET consumed_at = ? WHERE id = ?`, store.Now(), id)
}

// errPermanentApply marks a failure that retrying cannot fix (invalid
// params, unknown type, missing insight) — as opposed to a transient DB
// error worth re-running next cycle.
type errPermanentApply struct{ err error }

func (e errPermanentApply) Error() string { return e.err.Error() }

func permanentApplyError(err error) bool {
	_, ok := err.(errPermanentApply)
	return ok
}

// applyInsightAction executes one approved action from its evidence params.
func applyInsightAction(st *store.Store, typ string, insightID int64, decidedBy string) error {
	var evidence string
	if err := st.DB.QueryRow(`SELECT evidence FROM insights WHERE id = ?`, insightID).Scan(&evidence); err != nil {
		return errPermanentApply{fmt.Errorf("insight %d: %w", insightID, err)}
	}
	a := &govern.Audit{St: st, Actor: decidedBy}
	switch typ {
	case "budget":
		var ev struct {
			ScopeType      string  `json:"scope_type"`
			ScopeID        string  `json:"scope_id"`
			SuggestedLimit float64 `json:"suggested_limit"`
		}
		if err := json.Unmarshal([]byte(evidence), &ev); err != nil {
			return errPermanentApply{err}
		}
		if ev.SuggestedLimit <= 0 || ev.ScopeType == "" {
			return errPermanentApply{fmt.Errorf("insight %d: invalid budget params", insightID)}
		}
		if _, err := st.DB.Exec(`INSERT INTO budgets(scope_type, scope_id, limit_usd)
			VALUES(?, ?, ?) ON CONFLICT(scope_type, scope_id) DO UPDATE SET limit_usd = excluded.limit_usd`,
			ev.ScopeType, ev.ScopeID, ev.SuggestedLimit); err != nil {
			return err
		}
		_, err := a.Append("observer_budget_applied", ev.ScopeType+":"+ev.ScopeID,
			fmt.Sprintf("limit → $%.2f (insight #%d)", ev.SuggestedLimit, insightID))
		return err
	case "kill":
		var ev struct {
			RunID string `json:"run_id"`
		}
		if err := json.Unmarshal([]byte(evidence), &ev); err != nil {
			return errPermanentApply{err}
		}
		if ev.RunID == "" {
			return errPermanentApply{fmt.Errorf("insight %d: missing run_id", insightID)}
		}
		if _, err := st.DB.Exec(`UPDATE runs SET status = 'killed', ended_at = COALESCE(ended_at, ?)
			WHERE id = ?`, store.Now(), ev.RunID); err != nil {
			return err
		}
		_, err := a.Append("observer_kill_applied", ev.RunID, fmt.Sprintf("run killed (insight #%d)", insightID))
		return err
	case "band":
		var ev struct {
			AgentID string `json:"agent_id"`
			Band    string `json:"band"`
		}
		if err := json.Unmarshal([]byte(evidence), &ev); err != nil {
			return errPermanentApply{err}
		}
		if ev.AgentID == "" || !govern.ValidBand(ev.Band) {
			return errPermanentApply{fmt.Errorf("insight %d: invalid band params", insightID)}
		}
		return govern.SetBand(st, ev.AgentID, ev.Band, decidedBy, fmt.Sprintf("approved request (insight #%d)", insightID))
	case "context-promote", "context-company-edit":
		return applyContextWrite(st, typ, evidence, insightID, decidedBy)
	case "context-import":
		return applyContextImport(st, evidence, insightID, decidedBy)
	case "jira-transition":
		return applyJiraTransition(st, evidence, insightID, decidedBy)
	case "pr-review":
		return applyPRReview(st, evidence, insightID, decidedBy)
	case "calendar-event":
		return applyCalendarEvent(st, evidence, insightID, decidedBy)
	case "knowledge-publish":
		return applyKnowledgePublish(st, evidence, insightID, decidedBy)
	case "knowledge-mandate": // P6 fills in the real apply
		return applyKnowledgeMandate(st, evidence, insightID, decidedBy)
	case "knowledge-retire": // P6 fills in the real apply
		return applyKnowledgeRetire(st, evidence, insightID, decidedBy)
	default:
		return errPermanentApply{fmt.Errorf("unknown observer action type %q", typ)}
	}
}

// jiraTransitionParams is the shared evidence shape for UC2, written by the
// request handler (pinning the transition NAME at request time) and read
// back here at apply time. The id is intentionally NOT pinned — Jira
// transition ids are per-workflow and can be reassigned; only the name is a
// stable human-meaningful pin (H3).
type jiraTransitionParams struct {
	Key            string `json:"key"`
	TransitionName string `json:"transition_name"`
}

// applyJiraTransition re-fetches the issue's current transitions and matches
// by the pinned NAME (H3 — the id captured at request time may be stale by
// approval time). If the named transition is gone, the target legitimately
// moved: this is a re-openable failure, not a silent loss — audit
// jira_transition_apply_stale and file a fresh advisory insight so the human
// can see what changed and re-request, then return errPermanentApply so the
// spent approval isn't retried forever against a name that will never return.
func applyJiraTransition(st *store.Store, evidence string, insightID int64, decidedBy string) error {
	var ev jiraTransitionParams
	if err := json.Unmarshal([]byte(evidence), &ev); err != nil {
		return errPermanentApply{err}
	}
	if ev.Key == "" || ev.TransitionName == "" {
		return errPermanentApply{fmt.Errorf("insight %d: invalid jira-transition params", insightID)}
	}
	cfg, err := config.Load("")
	if err != nil {
		return err // transient — retry
	}
	i := cfg.Integrations
	if i.AtlassianSite == "" || i.AtlassianToken == "" {
		return errPermanentApply{fmt.Errorf("insight %d: atlassian credentials not configured", insightID)}
	}
	c := jira.New(i.AtlassianSite, i.AtlassianEmail, i.AtlassianToken)
	transitions, err := c.Transitions(ev.Key)
	if err != nil {
		return err // transient — retry
	}
	freshID := ""
	for _, tr := range transitions {
		if tr.Name == ev.TransitionName {
			freshID = tr.ID
			break
		}
	}
	if freshID == "" {
		return staleJiraTransition(st, ev, insightID, decidedBy)
	}
	guard := &integrations.Guard{Cfg: cfg, St: st}
	if !guard.Allow("jira.transition", ev.Key+"->"+ev.TransitionName) {
		return nil
	}
	if err := c.Transition(ev.Key, freshID); err != nil {
		return err // transient — retry
	}
	a := &govern.Audit{St: st, Actor: decidedBy}
	_, err = a.Append("jira_transition_applied", ev.Key, fmt.Sprintf("→ %s (insight #%d)", ev.TransitionName, insightID))
	return err
}

// staleJiraTransition audits the stale target and re-emits a fresh advisory
// insight (H3) so the human can see the transition disappeared and decide
// whether to re-request against the issue's now-current transitions — the
// consumed approval itself can never be re-approved (engine.go consume-once).
func staleJiraTransition(st *store.Store, ev jiraTransitionParams, insightID int64, decidedBy string) error {
	a := &govern.Audit{St: st, Actor: decidedBy}
	detail := fmt.Sprintf("transition %q no longer available on %s (insight #%d)", ev.TransitionName, ev.Key, insightID)
	if _, err := a.Append("jira_transition_apply_stale", ev.Key, detail); err != nil {
		return err
	}
	if _, err := persistInsight(st, Insight{
		Type: "jira_transition_stale", Subject: ev.Key,
		Summary:  fmt.Sprintf("Không thể chuyển %s sang %q — trạng thái đã đổi. Vui lòng kiểm tra và gửi duyệt lại.", ev.Key, ev.TransitionName),
		Evidence: map[string]any{"key": ev.Key, "transition_name": ev.TransitionName, "prior_insight_id": insightID},
		Class:    "auto", Surface: "operator",
	}); err != nil {
		return err
	}
	return errPermanentApply{fmt.Errorf("insight %d: %s", insightID, detail)}
}

// prReviewParams is the shared evidence shape for UC4, pinning the PR's head
// SHA at request time (H3 TOCTOU guard — a review approved against an old
// diff must not silently apply to a since-changed PR).
type prReviewParams struct {
	Repo     string `json:"repo"`
	Num      int    `json:"num"`
	Decision string `json:"decision"`
	Body     string `json:"body"`
	HeadSHA  string `json:"head_sha"`
}

var validPRDecisions = map[string]bool{"approve": true, "request-changes": true, "comment": true}

// applyPRReview re-checks the PR's current head SHA and state before
// applying (H3) — merged, closed, or head-changed since the request means
// the pinned diff no longer matches what the human approved, so the review
// is refused rather than applied against a moving target.
func applyPRReview(st *store.Store, evidence string, insightID int64, decidedBy string) error {
	var ev prReviewParams
	if err := json.Unmarshal([]byte(evidence), &ev); err != nil {
		return errPermanentApply{err}
	}
	if ev.Repo == "" || ev.Num <= 0 || ev.HeadSHA == "" || !validPRDecisions[ev.Decision] {
		return errPermanentApply{fmt.Errorf("insight %d: invalid pr-review params", insightID)}
	}
	cfg, err := config.Load("")
	if err != nil {
		return err // transient — retry
	}
	ctx, cancel := context.WithTimeout(context.Background(), applyWriteTimeout)
	defer cancel()
	state, headSHA, err := PRCurrentState(ctx, ev.Repo, ev.Num)
	if err != nil {
		return err // transient — retry (gh transient failure, not a stale-target signal)
	}
	if state == "merged" || state == "closed" || headSHA != ev.HeadSHA {
		return stalePRReview(st, ev, insightID, decidedBy, state, headSHA)
	}
	guard := &integrations.Guard{Cfg: cfg, St: st}
	if err := ghub.PRReview(ctx, guard, ev.Repo, ev.Num, ev.Decision, ev.Body); err != nil {
		if errors.Is(err, ghub.ErrPRReview) {
			// gh exited 1 (self-approve/permissions/network blip) — cannot
			// self-heal on retry with the same actor, but is not a
			// legitimately-stale target either. Audit and stop retrying.
			a := &govern.Audit{St: st, Actor: decidedBy}
			_, _ = a.Append("pr_review_apply_failed", fmt.Sprintf("%s#%d", ev.Repo, ev.Num), err.Error())
			return errPermanentApply{err}
		}
		return err // transient — retry
	}
	a := &govern.Audit{St: st, Actor: decidedBy}
	_, err = a.Append("pr_review_applied", fmt.Sprintf("%s#%d", ev.Repo, ev.Num),
		fmt.Sprintf("%s (insight #%d)", ev.Decision, insightID))
	return err
}

// stalePRReview audits the moved target and files a fresh advisory insight
// (H3) — never a silent loss of the human's spent approval.
func stalePRReview(st *store.Store, ev prReviewParams, insightID int64, decidedBy, state, headSHA string) error {
	a := &govern.Audit{St: st, Actor: decidedBy}
	detail := fmt.Sprintf("%s#%d state=%s head=%.12s (pinned head=%.12s, insight #%d)",
		ev.Repo, ev.Num, state, headSHA, ev.HeadSHA, insightID)
	if _, err := a.Append("pr_review_apply_stale", fmt.Sprintf("%s#%d", ev.Repo, ev.Num), detail); err != nil {
		return err
	}
	if _, err := persistInsight(st, Insight{
		Type: "pr_review_stale", Subject: fmt.Sprintf("%s#%d", ev.Repo, ev.Num),
		Summary:  fmt.Sprintf("Không thể review %s#%d — PR đã thay đổi (state=%s). Vui lòng kiểm tra và gửi duyệt lại.", ev.Repo, ev.Num, state),
		Evidence: map[string]any{"repo": ev.Repo, "num": ev.Num, "state": state, "head_sha": headSHA, "prior_insight_id": insightID},
		Class:    "auto", Surface: "operator",
	}); err != nil {
		return err
	}
	return errPermanentApply{fmt.Errorf("insight %d: %s", insightID, detail)}
}

// calendarEventParams is the shared evidence shape for UC9. IdemKey is
// request-derived (hash of run+purpose+date) and checked against the
// notifications dedup table before insert (H3) — a transient retry of an
// already-applied insert must be a no-op, never a double booking.
type calendarEventParams struct {
	Title       string   `json:"title"`
	Start       string   `json:"start"`
	End         string   `json:"end"`
	TZ          string   `json:"tz"`
	Attendees   []string `json:"attendees"`
	SendUpdates string   `json:"send_updates"`
	IdemKey     string   `json:"idem_key"`
}

// applyCalendarEvent validates the pinned params, skips if this idem_key
// already produced an event (H3 idempotent retry), then inserts via gws.
func applyCalendarEvent(st *store.Store, evidence string, insightID int64, decidedBy string) error {
	var ev calendarEventParams
	if err := json.Unmarshal([]byte(evidence), &ev); err != nil {
		return errPermanentApply{err}
	}
	if ev.Title == "" || ev.Start == "" || ev.End == "" || ev.IdemKey == "" {
		return errPermanentApply{fmt.Errorf("insight %d: invalid calendar-event params", insightID)}
	}
	startT, err := time.Parse(time.RFC3339, ev.Start)
	if err != nil {
		return errPermanentApply{fmt.Errorf("insight %d: bad start time: %w", insightID, err)}
	}
	endT, err := time.Parse(time.RFC3339, ev.End)
	if err != nil {
		return errPermanentApply{fmt.Errorf("insight %d: bad end time: %w", insightID, err)}
	}
	if !startT.Before(endT) {
		return errPermanentApply{fmt.Errorf("insight %d: start must be before end", insightID)}
	}
	// Reserve the idem_key BEFORE the external write, using the UNIQUE
	// dedup constraint as the lock. If our INSERT wins (1 row affected) we
	// own this event and proceed; if it was a no-op (row already present),
	// a prior attempt already claimed — and inserted — this event, so we
	// must NOT insert again. This closes the crash window between the
	// external CalendarInsert and recording the dedup row (review HIGH-1):
	// a check-then-write ordering could duplicate the calendar event on a
	// retry that crashed after inserting but before recording.
	res, err := st.DB.Exec(`INSERT INTO notifications(kind, dedup, sent_at, detail)
		VALUES('calendar_event', ?, ?, ?) ON CONFLICT(dedup) DO NOTHING`,
		ev.IdemKey, store.Now(), ev.Title)
	if err != nil {
		return err // transient — retry
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return nil // already claimed under this idem_key — retry is a no-op
	}
	cfg, err := config.Load("")
	if err != nil {
		return err // transient — retry
	}
	guard := &integrations.Guard{Cfg: cfg, St: st}
	runner := gws.NewRunner(guard)
	tz := ev.TZ
	if tz == "" {
		tz = "UTC"
	}
	var attendees []gws.CalendarAttendee
	for _, email := range ev.Attendees {
		attendees = append(attendees, gws.CalendarAttendee{Email: email})
	}
	calEv := gws.CalendarEvent{
		Summary:   ev.Title,
		Start:     gws.CalendarDateTime{DateTime: ev.Start, TimeZone: tz},
		End:       gws.CalendarDateTime{DateTime: ev.End, TimeZone: tz},
		Attendees: attendees,
	}
	ctx, cancel := context.WithTimeout(context.Background(), applyWriteTimeout)
	defer cancel()
	if _, _, err = runner.CalendarInsert(ctx, calEv, ev.SendUpdates); err != nil {
		// The external write failed — release our reservation so a genuine
		// retry can attempt again rather than being masked as "done".
		st.DB.Exec(`DELETE FROM notifications WHERE dedup = ?`, ev.IdemKey)
		return err // transient — retry
	}
	a := &govern.Audit{St: st, Actor: decidedBy}
	_, err = a.Append("calendar_event_applied", ev.Title, fmt.Sprintf("insight #%d", insightID))
	return err
}

// PRCurrentState reads a PR's current state and head SHA via `gh pr view`
// (read-only, no Guard needed — matches ghub's read pattern) — arg-slice,
// ctx-bounded, no shell. Exported so the request handler (UC4) can pin the
// head SHA at request time using the exact same read apply-time re-validates
// against (H3 — one implementation, no schema drift between the two calls).
func PRCurrentState(ctx context.Context, repo string, num int) (state, headSHA string, err error) {
	ctx, cancel := context.WithTimeout(ctx, applyWriteTimeout)
	defer cancel()
	out, err := exec.CommandContext(ctx, "gh", "pr", "view", strconv.Itoa(num), "--repo", repo,
		"--json", "headRefOid,state").Output()
	if err != nil {
		return "", "", fmt.Errorf("gh pr view %s#%d: %w", repo, num, err)
	}
	var resp struct {
		HeadRefOid string `json:"headRefOid"`
		State      string `json:"state"`
	}
	if err := json.Unmarshal(out, &resp); err != nil {
		return "", "", fmt.Errorf("gh pr view %s#%d: parse: %w", repo, num, err)
	}
	return strings.ToLower(resp.State), resp.HeadRefOid, nil
}

// ValidateEmail is used by the request handler (M5) to reject non-email
// attendees before they ever reach RequestAction. Kept here (not in web) so
// the same validation guards both the request path and any future caller.
func ValidateEmail(s string) bool {
	addr, err := mail.ParseAddress(s)
	return err == nil && addr.Address == s
}

// applyContextWrite applies an approved company-context write (a promote from
// a team doc, or a direct company edit). It loads the PINNED source version —
// the exact bytes the approver saw — never the current head (H3 TOCTOU), and
// re-checks for secrets (M1 defense in depth) before writing a new company
// version. If the source has since advanced, an audit note records it.
func applyContextWrite(st *store.Store, typ, evidence string, insightID int64, decidedBy string) error {
	var ev struct {
		SourceLayer   string `json:"source_layer"`
		SourceTarget  string `json:"source_target"`
		SourceVersion int    `json:"source_version_n"`
		Content       string `json:"content"` // company-edit pins content directly
	}
	if err := json.Unmarshal([]byte(evidence), &ev); err != nil {
		return errPermanentApply{err}
	}
	hub := contexthub.New(st)
	content := ev.Content
	// Promote pins a team version → load that immutable snapshot.
	if ev.SourceLayer != "" && ev.SourceVersion > 0 {
		d, err := hub.Version(ev.SourceLayer, ev.SourceTarget, ev.SourceVersion)
		if err != nil {
			return err // transient — retry
		}
		if d == nil {
			return errPermanentApply{fmt.Errorf("insight %d: pinned version %s/%s v%d gone",
				insightID, ev.SourceLayer, ev.SourceTarget, ev.SourceVersion)}
		}
		content = d.Content
	}
	if content == "" {
		return errPermanentApply{fmt.Errorf("insight %d: no content to apply", insightID)}
	}
	note := fmt.Sprintf("duyệt #%d", insightID)
	if ev.SourceLayer == contexthub.LayerTeam {
		if head, _ := hub.Head(contexthub.LayerTeam, ev.SourceTarget); head != nil && head.VersionN > ev.SourceVersion {
			note += fmt.Sprintf(" (đội đã cập nhật lên v%d sau khi đề xuất — áp bản đã duyệt)", head.VersionN)
		}
	}
	if _, err := hub.SaveContext(contexthub.LayerCompany, contexthub.CompanyTarget, content, decidedBy, note); err != nil {
		// A secret in the pinned content is permanent (won't fix on retry).
		if err == contexthub.ErrSecretInContent {
			return errPermanentApply{err}
		}
		return err
	}
	auditAction := "context_company_edited"
	if typ == "context-promote" {
		auditAction = "context_promoted"
	}
	a := &govern.Audit{St: st, Actor: decidedBy}
	_, err := a.Append(auditAction, "company:*", note)
	return err
}

// contextImportParams is UC6's evidence shape: the FULL Drive doc body
// pinned at request time (C1 — the human approved these exact bytes), plus
// the Drive provenance fields written into SaveContext's note.
type contextImportParams struct {
	Layer   string `json:"layer"`
	Target  string `json:"target"`
	Content string `json:"content"`
	DocID   string `json:"doc_id"`
	DocName string `json:"doc_name"`
}

// applyContextImport is UC6's only path from an approved Drive import to a
// written context version — Search/Review (internal/integrations/gws) never
// call SaveContext, and neither does the request handler; this is the sole
// call site (C1). Re-scans for secrets as defense in depth (the reviewer's
// pre-render scan already ran once at request time, but content is
// untrusted external input so a second check costs nothing) and tags the
// saved version with a Drive provenance note so imported layers are
// visibly distinct from operator-authored policy in the effective-context
// view.
func applyContextImport(st *store.Store, evidence string, insightID int64, decidedBy string) error {
	var ev contextImportParams
	if err := json.Unmarshal([]byte(evidence), &ev); err != nil {
		return errPermanentApply{err}
	}
	if ev.Content == "" {
		return errPermanentApply{fmt.Errorf("insight %d: no content to import", insightID)}
	}
	if frag := contexthub.SecretFragment(ev.Content); frag != "" {
		return errPermanentApply{fmt.Errorf("insight %d: %w", insightID, contexthub.ErrSecretInContent)}
	}
	hub := contexthub.New(st)
	note := fmt.Sprintf("imported from Drive: %s (%s)", ev.DocName, ev.DocID)
	if _, err := hub.SaveContext(ev.Layer, ev.Target, ev.Content, decidedBy, note); err != nil {
		if err == contexthub.ErrSecretInContent {
			return errPermanentApply{err}
		}
		return err
	}
	a := &govern.Audit{St: st, Actor: decidedBy}
	_, err := a.Append("context_imported_drive", ev.Layer+":"+ev.Target,
		fmt.Sprintf("doc %s (%s), insight #%d", ev.DocName, ev.DocID, insightID))
	return err
}

// RequestAction is the shared "propose, never execute" entry point: it
// persists an insight holding the structured params and opens an approval
// request in the observer namespace. Used by the CEO chatbot's action tools
// (surface "ceo") and by the Context Hub's company-edit/promote flows
// (surface "operator" — a technical doc action, never a CEO one-tap card).
// Neither may mutate state directly.
func RequestAction(st *store.Store, typ, subject, summary string, params map[string]any, requestedBy, surface string) (int64, error) {
	id, err := persistInsight(st, Insight{
		Type: "request_" + typ, Subject: subject, Summary: summary,
		Evidence: params, Class: "approval", Surface: surface,
	})
	if err != nil {
		return 0, err
	}
	action := fmt.Sprintf("observer:%s:%d", typ, id)
	ar, err := st.DB.Exec(`INSERT INTO approvals(run_id, action, reason, requested_at)
		VALUES(NULL, ?, ?, ?)`, action, summary, store.Now())
	if err != nil {
		return 0, err
	}
	approvalID, _ := ar.LastInsertId()
	if _, err := st.DB.Exec(`UPDATE insights SET approval_id = ? WHERE id = ?`, approvalID, id); err != nil {
		return 0, err
	}
	a := &govern.Audit{St: st, Actor: requestedBy}
	_, _ = a.Append("action_requested", subject, action+" — "+summary)
	return approvalID, nil
}
