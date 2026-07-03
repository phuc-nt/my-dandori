package cli

import (
	"database/sql"
	"fmt"
	"os"

	"github.com/phuc-nt/dandori/internal/capture"
	"github.com/phuc-nt/dandori/internal/config"
	"github.com/phuc-nt/dandori/internal/contexthub"
	"github.com/phuc-nt/dandori/internal/ingest"
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
	emitContext(text)
	recordContextProvenance(ing, in, text, prov)
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
