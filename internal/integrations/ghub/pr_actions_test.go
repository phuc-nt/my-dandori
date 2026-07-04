package ghub

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// fakeGate is a controllable Gate for tests: Allowed reports what to
// return; Calls records every (action, detail) passed in.
type fakeGate struct {
	allowed bool
	calls   []string
}

func (g *fakeGate) Allow(action, detail string) bool {
	g.calls = append(g.calls, action+"|"+detail)
	return g.allowed
}

// withFakeGh prepends testdata (containing the fake-gh script renamed to
// "gh" in a temp dir symlink) onto PATH so exec.Command("gh", ...) resolves
// to the fake. Returns the path to the argv-capture file.
func withFakeGh(t *testing.T) string {
	t.Helper()
	repoRoot, err := filepath.Abs("../testdata")
	if err != nil {
		t.Fatal(err)
	}
	binDir := t.TempDir()
	link := filepath.Join(binDir, "gh")
	if err := os.Symlink(filepath.Join(repoRoot, "fake-gh"), link); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", binDir+string(os.PathListSeparator)+os.Getenv("PATH"))
	argvOut := filepath.Join(t.TempDir(), "argv.log")
	t.Setenv("FAKE_ARGV_OUT", argvOut)
	return argvOut
}

// readArgv returns the last captured invocation's args, split on the
// \x1f unit separator the fake CLI uses to join argv exactly (safe for
// values containing spaces or JSON braces).
func readArgv(t *testing.T, path string) []string {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	lines := strings.Split(strings.TrimRight(string(b), "\n"), "\n")
	if len(lines) == 0 || lines[len(lines)-1] == "" {
		t.Fatalf("no argv captured in %s", path)
	}
	return strings.Split(lines[len(lines)-1], "\x1f")
}

func TestPRCommentGuardSkip(t *testing.T) {
	withFakeGh(t)
	g := &fakeGate{allowed: false}
	if err := PRComment(context.Background(), g, "phuc-nt/dandori", 42, "hello"); err != nil {
		t.Fatalf("guard=false must return nil, got %v", err)
	}
	if len(g.calls) != 1 || g.calls[0] != "github.pr_comment|phuc-nt/dandori#42" {
		t.Errorf("guard calls: %v", g.calls)
	}
}

func TestPRCommentArgv(t *testing.T) {
	argvOut := withFakeGh(t)
	g := &fakeGate{allowed: true}
	hostile := `hello "world"`
	if err := PRComment(context.Background(), g, "phuc-nt/dandori", 42, hostile); err != nil {
		t.Fatal(err)
	}
	got := readArgv(t, argvOut)
	want := []string{"pr", "comment", "42", "--repo", "phuc-nt/dandori", "--body", hostile}
	if !equalArgv(got, want) {
		t.Errorf("argv: %v, want %v", got, want)
	}
}

func TestPRReviewDecisions(t *testing.T) {
	cases := []struct {
		decision string
		wantFlag string
	}{
		{"approve", "--approve"},
		{"request-changes", "--request-changes"},
		{"comment", "--comment"},
	}
	for _, c := range cases {
		t.Run(c.decision, func(t *testing.T) {
			argvOut := withFakeGh(t)
			g := &fakeGate{allowed: true}
			if err := PRReview(context.Background(), g, "phuc-nt/dandori", 7, c.decision, "note"); err != nil {
				t.Fatal(err)
			}
			got := readArgv(t, argvOut)
			if !containsArg(got, c.wantFlag) {
				t.Errorf("argv %v missing flag %q", got, c.wantFlag)
			}
			if !containsArg(got, "--body") || !containsArg(got, "note") {
				t.Errorf("argv %v missing --body note", got)
			}
		})
	}
}

func equalArgv(got, want []string) bool {
	if len(got) != len(want) {
		return false
	}
	for i := range got {
		if got[i] != want[i] {
			return false
		}
	}
	return true
}

func containsArg(argv []string, want string) bool {
	for _, a := range argv {
		if a == want {
			return true
		}
	}
	return false
}

func TestPRReviewUnknownDecision(t *testing.T) {
	withFakeGh(t)
	g := &fakeGate{allowed: true}
	err := PRReview(context.Background(), g, "phuc-nt/dandori", 7, "bogus", "")
	if err == nil {
		t.Fatal("expected error for unknown decision")
	}
	if len(g.calls) != 0 {
		t.Errorf("Guard must not be consulted before validating decision, calls=%v", g.calls)
	}
}

func TestPRReviewSoftErrorOnExit1(t *testing.T) {
	withFakeGh(t)
	t.Setenv("FAKE_EXIT", "1")
	g := &fakeGate{allowed: true}
	err := PRReview(context.Background(), g, "phuc-nt/dandori", 7, "approve", "")
	if err == nil {
		t.Fatal("expected soft error")
	}
	if !errors.Is(err, ErrPRReview) {
		t.Errorf("error must wrap ErrPRReview, got %v", err)
	}
}

func TestPRReviewHardErrorOnExit2(t *testing.T) {
	withFakeGh(t)
	t.Setenv("FAKE_EXIT", "2")
	g := &fakeGate{allowed: true}
	err := PRReview(context.Background(), g, "phuc-nt/dandori", 7, "approve", "")
	if err == nil {
		t.Fatal("expected hard error")
	}
	if errors.Is(err, ErrPRReview) {
		t.Errorf("exit 2 must NOT be classified as soft ErrPRReview: %v", err)
	}
}
