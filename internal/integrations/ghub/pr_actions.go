package ghub

import (
	"context"
	"errors"
	"fmt"
	"os/exec"
	"strconv"
	"time"
)

// prWriteTimeout bounds a single gh write. A hung gh (network stall,
// credential prompt, TTY read) must not wedge the approval-apply loop that
// calls these methods.
const prWriteTimeout = 30 * time.Second

// Gate is the narrow slice of integrations.Guard this package needs. Kept
// local (structural typing) so ghub does not import the integrations
// package for a single method — Guard satisfies this interface as-is.
type Gate interface {
	Allow(action, detail string) bool
}

// ErrPRReview is a soft failure: gh exited 1, which can mean self-approve,
// missing permissions, or a network blip. Callers should log and continue
// rather than treat it as a hard failure.
var ErrPRReview = errors.New("gh pr review: soft failure (self-approve, permissions, or network)")

// PRComment posts a comment on a PR via the gh CLI (keyring auth, no shell).
// Guard runs first; when it returns false the write is treated as done.
func PRComment(ctx context.Context, g Gate, repo string, num int, body string) error {
	detail := fmt.Sprintf("%s#%d", repo, num)
	if !g.Allow("github.pr_comment", detail) {
		return nil
	}
	ctx, cancel := context.WithTimeout(ctx, prWriteTimeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, "gh", "pr", "comment", strconv.Itoa(num), "--repo", repo, "--body", body)
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("gh pr comment %s: %w", detail, err)
	}
	return nil
}

// PRReview approves, requests changes on, or comments on a PR via the gh
// CLI. decision must be one of "approve", "request-changes", "comment".
// gh cannot self-approve its own PR — that surfaces as exit 1, mapped to
// the soft ErrPRReview so callers can log-and-continue.
func PRReview(ctx context.Context, g Gate, repo string, num int, decision, body string) error {
	flag, err := reviewFlag(decision)
	if err != nil {
		return err
	}
	detail := fmt.Sprintf("%s#%d:%s", repo, num, decision)
	if !g.Allow("github.pr_review", detail) {
		return nil
	}
	args := []string{"pr", "review", strconv.Itoa(num), "--repo", repo, flag}
	if body != "" {
		args = append(args, "--body", body)
	}
	ctx, cancel := context.WithTimeout(ctx, prWriteTimeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, "gh", args...)
	if runErr := cmd.Run(); runErr != nil {
		var exitErr *exec.ExitError
		if errors.As(runErr, &exitErr) && exitErr.ExitCode() == 1 {
			return fmt.Errorf("%w: %s", ErrPRReview, detail)
		}
		return fmt.Errorf("gh pr review %s: %w", detail, runErr)
	}
	return nil
}

func reviewFlag(decision string) (string, error) {
	switch decision {
	case "approve":
		return "--approve", nil
	case "request-changes":
		return "--request-changes", nil
	case "comment":
		return "--comment", nil
	default:
		return "", fmt.Errorf("gh pr review: unknown decision %q", decision)
	}
}
