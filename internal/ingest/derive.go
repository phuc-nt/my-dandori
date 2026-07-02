package ingest

import (
	"encoding/json"
	"os"
	"path/filepath"

	"github.com/phuc-nt/dandori/internal/capture"
	"github.com/phuc-nt/dandori/internal/config"
	"github.com/phuc-nt/dandori/internal/store"
)

// Client-side derivation (red-team H4): the transcript lives on THIS machine
// and never leaves it. We parse it locally and ship only numerics.

// sessionStatePath holds per-session client state (git snapshot at start).
func sessionStatePath(sessionID string) string {
	home, _ := os.UserHomeDir()
	return filepath.Join(home, ".dandori", "sessions", sessionID+".json")
}

// SaveSessionGit snapshots the repo state at session start so DeriveFinalize
// can compute the session's code delta at stop.
func SaveSessionGit(sessionID, cwd string) {
	gs := capture.SnapshotGit(cwd)
	if !gs.IsRepo {
		return
	}
	b, err := json.Marshal(gs)
	if err != nil {
		return
	}
	path := sessionStatePath(sessionID)
	_ = os.MkdirAll(filepath.Dir(path), 0o755)
	_ = os.WriteFile(path, b, 0o600)
}

// DeriveFinalize parses the local transcript and builds the numeric payload.
// Fail-open contract: on parse errors it returns whatever it could derive.
func DeriveFinalize(cfg *config.Config, sessionID, cwd, transcriptPath string) *RunFinalize {
	fin := &RunFinalize{Status: "done", EndedAt: store.Now()}
	if transcriptPath != "" {
		if u, err := capture.ParseTranscript(transcriptPath); err == nil {
			fin.Model = u.Model
			fin.InputTokens, fin.OutputTokens = u.Input, u.Output
			fin.CacheRead, fin.CacheWrite = u.CacheRead, u.CacheWrite
			fin.CostUSD = cfg.Cost(u.Model, u.Input, u.Output, u.CacheRead, u.CacheWrite)
			fin.TaskKey = capture.FindTaskKey(u.FirstUser)
			fin.MidRunMsgs = u.MidRunMsgs
			fin.PromptWords, fin.PromptSpec = capture.PromptProxy(u.FirstUser)
		}
	}
	after := capture.SnapshotGit(cwd)
	if after.IsRepo {
		fin.HeadAfter = after.Head
		if before := loadSessionGit(sessionID); before != nil {
			fin.HeadBefore = before.Head
			fin.LinesAdded, fin.LinesDeleted = capture.SessionDelta(cwd, *before, after)
		}
	}
	_ = os.Remove(sessionStatePath(sessionID))
	return fin
}

func loadSessionGit(sessionID string) *capture.GitState {
	b, err := os.ReadFile(sessionStatePath(sessionID))
	if err != nil {
		return nil
	}
	var gs capture.GitState
	if json.Unmarshal(b, &gs) != nil {
		return nil
	}
	return &gs
}
