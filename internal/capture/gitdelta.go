package capture

import (
	"os/exec"
	"strconv"
	"strings"
)

// GitState is a snapshot of a working tree: HEAD plus total line delta of
// the uncommitted diff. Used before/after a run to attribute code volume.
type GitState struct {
	Head    string
	Added   int
	Deleted int
	IsRepo  bool
}

// SnapshotGit captures HEAD and the current uncommitted numstat totals.
// Non-repo directories return IsRepo=false — callers skip silently.
func SnapshotGit(dir string) GitState {
	head, err := gitOut(dir, "rev-parse", "HEAD")
	if err != nil {
		return GitState{}
	}
	added, deleted := numstatTotals(dir, "diff", "--numstat")
	return GitState{Head: strings.TrimSpace(head), Added: added, Deleted: deleted, IsRepo: true}
}

// SessionDelta computes lines added/deleted during a session given the
// before/after snapshots: committed range delta plus change in dirty state.
func SessionDelta(dir string, before, after GitState) (added, deleted int) {
	if !before.IsRepo || !after.IsRepo {
		return 0, 0
	}
	if before.Head != after.Head {
		a, d := numstatTotals(dir, "diff", "--numstat", before.Head, after.Head)
		added += a
		deleted += d
	}
	// Dirty-tree growth during the session (uncommitted work counts too).
	if da := after.Added - before.Added; da > 0 {
		added += da
	}
	if dd := after.Deleted - before.Deleted; dd > 0 {
		deleted += dd
	}
	return added, deleted
}

// numstatTotals sums the added/deleted columns of a git numstat output.
func numstatTotals(dir string, args ...string) (added, deleted int) {
	out, err := gitOut(dir, args...)
	if err != nil {
		return 0, 0
	}
	for _, line := range strings.Split(out, "\n") {
		fields := strings.Fields(line)
		if len(fields) < 3 {
			continue
		}
		if a, err := strconv.Atoi(fields[0]); err == nil {
			added += a
		}
		if d, err := strconv.Atoi(fields[1]); err == nil {
			deleted += d
		}
	}
	return added, deleted
}

func gitOut(dir string, args ...string) (string, error) {
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	out, err := cmd.Output()
	return string(out), err
}
