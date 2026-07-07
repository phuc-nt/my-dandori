package cli

import (
	"database/sql"
	"fmt"
	"os"

	"github.com/phuc-nt/dandori/internal/capture"
	"github.com/phuc-nt/dandori/internal/config"
	"github.com/phuc-nt/dandori/internal/contexthub"
	"github.com/phuc-nt/dandori/internal/ingest"
	"github.com/phuc-nt/dandori/internal/learn"
	"github.com/phuc-nt/dandori/internal/skillreg"
	"github.com/phuc-nt/dandori/internal/store"
)

// injectSources are the SessionStart sources that (re)inject context.
// compact/clear do NOT re-fire — the model keeps the earlier injection.
func injectsOn(source string) bool {
	return source == "startup" || source == "resume" || source == ""
}

// injectContextLocal emits the agent's effective context as SessionStart
// additionalContext and records a provenance event. Everything here is
// best-effort and fail-open: any error logs to stderr and the session
// proceeds with no context (context is guidance, not a gate).
func injectContextLocal(cfg *config.Config, st *store.Store, ing *capture.Ingestor, in capture.HookInput) {
	if !injectsOn(in.Source) {
		return
	}
	agentName, _ := capture.ResolveAttribution(in.CWD)
	agentID := capture.AgentID(agentName)
	text, prov, err := contexthub.New(st).EffectiveContext(agentID)
	if err != nil {
		fmt.Fprintln(os.Stderr, "dandori context:", err)
		return
	}
	text = appendComplianceNotice(st, in.CWD, text)
	emitContext(text)
	recordContextProvenance(ing, in, text, prov)
}

// appendComplianceNotice is the F13/F3 SessionStart mandate-visibility check:
// for each currently mandated (required=1, live) skill unit, compare the
// locally-pulled file's hash against the hash recorded at publish time
// (skillreg.ApproveHash — the F7 independent verification source). Missing
// or stale files get ONE combined notice line appended to text.
//
// ABSOLUTELY fail-open (F3): this is a visibility nudge, not a gate. Any
// error anywhere (no repo root, DB read failure, hash read failure) is
// logged to stderr and swallowed — the session must never be blocked or
// even delayed meaningfully by this check.
func appendComplianceNotice(st *store.Store, cwd, text string) string {
	notice := complianceNoticeLine(st, cwd)
	if notice == "" {
		return text
	}
	if text == "" {
		return notice
	}
	return text + "\n\n" + notice
}

// complianceNoticeLine returns "" when there is nothing to report (including
// on any error — logged to stderr, never propagated).
//
// M3: this runs on EVERY SessionStart, so it uses the narrow indexed
// learn.MandatedSkillUnitRefs query (id/name/content_hash only) instead of
// three full ListUnits scans (each loading every column, including a body up
// to MaxUnitBodySize, for every published/adopted/measured unit regardless
// of kind) and early-exits the instant there are zero mandated skills — the
// common case for most agents/repos. skillreg.ApproveHash (its own
// audit_log query) now only runs per mandated skill actually found locally
// present-but-possibly-stale, never per row up front.
func complianceNoticeLine(st *store.Store, cwd string) string {
	units, err := learn.MandatedSkillUnitRefs(st)
	if err != nil {
		fmt.Fprintln(os.Stderr, "dandori compliance notice: skip (list mandated skills):", err)
		return ""
	}
	if len(units) == 0 {
		return ""
	}
	repoRoot, err := repoRootFrom(cwd)
	if err != nil {
		fmt.Fprintln(os.Stderr, "dandori compliance notice: skip (no repo root):", err)
		return ""
	}

	var missing, stale []string
	for _, u := range units {
		path, err := skillreg.LocalPath(repoRoot, u.Name)
		if err != nil {
			continue // fail-open per-unit: skip this one, keep checking others
		}
		localHash, err := skillreg.LocalHash(path)
		if err != nil {
			continue
		}
		if localHash == "" {
			missing = append(missing, u.Name)
			continue
		}
		if localHash == u.ContentHash {
			continue // matches the row hash — only consult the audit trail when it doesn't
		}
		approveHash, err := skillreg.ApproveHash(st, u.ID)
		if err != nil {
			continue
		}
		if localHash != approveHash {
			stale = append(stale, u.Name)
		}
	}
	if len(missing) == 0 && len(stale) == 0 {
		return ""
	}
	return "[dandori] Skill bắt buộc (mandate) chưa đồng bộ — chưa cài: " +
		joinOrNone(missing) + " · lệch phiên bản: " + joinOrNone(stale) +
		" — chạy `dandori skill pull <name>` để cập nhật."
}

// joinOrNone renders a name list for the notice line, "(không có)" when empty
// so the sentence always reads grammatically regardless of which half fired.
func joinOrNone(names []string) string {
	if len(names) == 0 {
		return "(không có)"
	}
	out := names[0]
	for _, n := range names[1:] {
		out += ", " + n
	}
	return out
}

// repoRootFrom mirrors findRepoRoot's ".git upward walk" but is seeded from
// the hook's own cwd (in.CWD) rather than os.Getwd() — the hook process's
// cwd is not guaranteed to be the hooked project's directory, but in.CWD is
// exactly the field capture.HookInput carries for this purpose.
func repoRootFrom(cwd string) (string, error) {
	if cwd == "" {
		return "", fmt.Errorf("empty cwd")
	}
	dir := cwd
	for {
		if _, err := os.Stat(dir + "/.git"); err == nil {
			return dir, nil
		}
		parent := parentDir(dir)
		if parent == dir {
			return "", fmt.Errorf("not inside a git repo (no .git found walking up from %s)", cwd)
		}
		dir = parent
	}
}

// parentDir is a tiny filepath.Dir wrapper kept local to avoid importing
// path/filepath into this file solely for one call.
func parentDir(dir string) string {
	i := len(dir) - 1
	for i > 0 && dir[i] == '/' {
		i--
	}
	for i > 0 && dir[i] != '/' {
		i--
	}
	if i == 0 {
		return "/"
	}
	return dir[:i]
}

// emitContext writes the injection JSON to stdout (nothing for empty text).
func emitContext(text string) {
	if b := contexthub.BuildInjection(text); b != nil {
		os.Stdout.Write(b)
	}
}

// recordContextProvenance writes one context_injected event so a run traces
// back to the context versions it ran under. Best-effort.
func recordContextProvenance(ing *capture.Ingestor, in capture.HookInput, text string, prov contexthub.Provenance) {
	if text == "" {
		return
	}
	runID, err := ing.EnsureRun(in.SessionID, in.CWD, in.TranscriptPath, "hook")
	if err != nil {
		return
	}
	_, _ = ing.AddEvent(runID, "context_injected", "", sql.NullInt64{}, contexthub.ProvPayload(prov))
}

// injectContextCentral is the central-mode twin: pull the effective context
// from the server (TTL cache, fail-open), emit it, and spool a provenance
// event. The event is EVENTUAL — FlushThrottled kicks it toward the server
// rather than leaving it stuck until the next post-tool/stop (M6).
func injectContextCentral(c *ingest.Client, in capture.HookInput, agentID string) {
	if !injectsOn(in.Source) {
		return
	}
	text, prov := c.Context(agentID)
	if text == "" {
		return
	}
	emitContext(text)
	agentName, project := capture.ResolveAttribution(in.CWD)
	_ = c.AppendEvent(ingest.Record{
		Type:      "event",
		SessionID: in.SessionID,
		AgentName: agentName,
		Project:   project,
		CWD:       in.CWD,
		ULID:      ingest.NewULID(),
		Kind:      "context_injected",
		Payload:   contexthub.ProvPayload(prov),
		ClientTS:  store.Now(),
	})
	c.FlushThrottled()
}
