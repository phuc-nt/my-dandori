package gws

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// fakeGate is a controllable Gate for tests.
type fakeGate struct {
	allowed bool
	calls   []string
}

func (g *fakeGate) Allow(action, detail string) bool {
	g.calls = append(g.calls, action+"|"+detail)
	return g.allowed
}

// newTestRunner points Bin directly at the fake-gws script (Runner.Bin
// accepts a full path, so no PATH manipulation is needed) and wires an
// argv-capture file for assertions.
func newTestRunner(t *testing.T, guard Gate) (r *Runner, argvOut string) {
	t.Helper()
	bin, err := filepath.Abs("../testdata/fake-gws")
	if err != nil {
		t.Fatal(err)
	}
	argvOut = filepath.Join(t.TempDir(), "argv.log")
	t.Setenv("FAKE_ARGV_OUT", argvOut)
	return &Runner{Bin: bin, Guard: guard}, argvOut
}

// readArgvLines returns one []string per captured invocation, in order.
// Each invocation's args are split on the \x1f unit separator the fake
// CLI uses to join argv exactly (safe for values containing spaces or
// JSON braces).
func readArgvLines(t *testing.T, path string) [][]string {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		t.Fatal(err)
	}
	s := strings.TrimRight(string(b), "\n")
	if s == "" {
		return nil
	}
	lines := strings.Split(s, "\n")
	out := make([][]string, len(lines))
	for i, l := range lines {
		out[i] = strings.Split(l, "\x1f")
	}
	return out
}

func TestStripBanner(t *testing.T) {
	cases := []struct {
		name, in, want string
	}{
		{"object", "Using keyring backend: keyring\n{\"a\":1}", `{"a":1}`},
		{"array", "Using keyring backend: keyring\n[1,2]", "[1,2]"},
		{"no banner", `{"a":1}`, `{"a":1}`},
		{"no json", "plain text, no json here", "plain text, no json here"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := string(stripBanner([]byte(c.in)))
			if got != c.want {
				t.Errorf("stripBanner(%q) = %q, want %q", c.in, got, c.want)
			}
		})
	}
}

func TestRunnerMissingBinary(t *testing.T) {
	r := &Runner{Bin: filepath.Join(t.TempDir(), "does-not-exist-gws"), Guard: &fakeGate{allowed: true}}
	_, err := r.run(context.Background(), "sheets", "spreadsheets", "create", "--json", "{}")
	if err == nil {
		t.Fatal("expected error for missing binary")
	}
	if err != ErrGwsMissing {
		t.Errorf("error: %v, want ErrGwsMissing", err)
	}
}

func TestRunnerExit127MapsToErrGwsMissing(t *testing.T) {
	r, _ := newTestRunner(t, &fakeGate{allowed: true})
	t.Setenv("FAKE_EXIT", "127")
	_, err := r.run(context.Background(), "sheets", "spreadsheets", "create", "--json", "{}")
	if err != ErrGwsMissing {
		t.Errorf("error: %v, want ErrGwsMissing", err)
	}
}
