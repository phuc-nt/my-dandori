package gws

import (
	"encoding/json"
	"testing"
)

// equalArgvPrefix reports whether argv starts with exactly the given
// leading tokens.
func equalArgvPrefix(argv, prefix []string) bool {
	if len(argv) < len(prefix) {
		return false
	}
	for i, p := range prefix {
		if argv[i] != p {
			return false
		}
	}
	return true
}

// unmarshalFlagJSON finds `flag` in argv and unmarshals the following
// argument (the exact value passed to exec.Command, no shell re-parsing)
// into dst.
func unmarshalFlagJSON(t *testing.T, argv []string, flag string, dst any) {
	t.Helper()
	for i, a := range argv {
		if a == flag && i+1 < len(argv) {
			if err := json.Unmarshal([]byte(argv[i+1]), dst); err != nil {
				t.Fatalf("unmarshal %s value %q: %v", flag, argv[i+1], err)
			}
			return
		}
	}
	t.Fatalf("flag %s not found in argv %v", flag, argv)
}
