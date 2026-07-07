package kitpolicy

import (
	"errors"
	"testing"
)

func TestValidateKitPath(t *testing.T) {
	tests := []struct {
		name    string
		path    string
		wantErr error // nil means "keep" (no error)
	}{
		// --- deny-list (H3), checked before whitelist ---
		{"deny hooks dir", "hooks/pre-tool.md", ErrDenied},
		{"deny scripts dir", "scripts/run.md", ErrDenied},
		{"deny output-styles dir", "output-styles/theme.md", ErrDenied},
		{"deny settings.json anywhere", "settings.json", ErrDenied},
		{"deny settings.local.json anywhere", "settings.local.json", ErrDenied},
		{"deny settings.json nested", "rules/settings.json", ErrDenied},
		{"deny settings.local.json nested", "agents/settings.local.json", ErrDenied},
		{"deny beats whitelist even if nested", "hooks/agents/x.md", ErrDenied},

		// --- whitelist ---
		{"whitelist agents md", "agents/reviewer.md", nil},
		{"whitelist rules md", "rules/development-rules.md", nil},
		{"whitelist commands md", "commands/deploy.md", nil},
		{"whitelist skills md", "skills/my-skill/SKILL.md", nil},
		{"non-whitelisted top segment skipped", "docs/readme.md", ErrNotWhitelisted},
		{"unknown top segment skipped", "random/file.md", ErrNotWhitelisted},

		// --- per-segment regex / traversal ---
		{"traversal dotdot segment", "rules/../../../etc/passwd.md", ErrInvalidSegment},
		{"dot segment rejected raw (no silent clean)", "rules/./x.md", ErrInvalidSegment},
		{"invalid char in segment", "rules/bad name!.md", ErrInvalidSegment},
		{"empty segment via double slash", "rules//x.md", ErrInvalidSegment},

		// --- depth cap ---
		{"depth ok at 3", "skills/foo/SKILL.md", nil},
		{"depth ok at 4", "skills/foo/bar/SKILL.md", nil},
		{"depth exceeds cap at 5", "skills/foo/bar/baz/SKILL.md", ErrTooDeep},

		// --- .md extension / H4 non-md-in-skill-dir sentinel ---
		{"non-md in skill dir warns (H4)", "skills/foo/run.py", ErrNonMarkdownInSkill},
		{"non-md in agents dir warns (H4)", "agents/reviewer.png", ErrNonMarkdownInSkill},
		{"no extension warns (H4)", "rules/README", ErrNonMarkdownInSkill},

		// --- hidden (dotfile) basename rejected even under a whitelisted dir ---
		{"hidden md file rejected", "agents/.hidden.md", ErrInvalidSegment},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := ValidateKitPath(tt.path)
			if tt.wantErr == nil {
				if err != nil {
					t.Errorf("ValidateKitPath(%q) = %v, want nil (keep)", tt.path, err)
				}
				return
			}
			if !errors.Is(err, tt.wantErr) {
				t.Errorf("ValidateKitPath(%q) = %v, want errors.Is(_, %v)", tt.path, err, tt.wantErr)
			}
		})
	}
}

// TestValidateKitPathDenyFirstOrder proves deny-first ordering (H3 doc
// comment): a path that would ALSO fail whitelist/depth/regex on its own
// merits must still surface as ErrDenied, never any other error, since the
// deny check runs before every other branch.
func TestValidateKitPathDenyFirstOrder(t *testing.T) {
	err := ValidateKitPath("hooks/../../etc/passwd")
	if !errors.Is(err, ErrDenied) {
		t.Errorf("deny-first: got %v, want ErrDenied even though path is also malformed", err)
	}
}

// TestValidateKitPathEmpty guards the degenerate empty-path input.
func TestValidateKitPathEmpty(t *testing.T) {
	err := ValidateKitPath("")
	if !errors.Is(err, ErrInvalidSegment) {
		t.Errorf("empty path: got %v, want ErrInvalidSegment", err)
	}
}
