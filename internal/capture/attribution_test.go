package capture

import "testing"

// acceptSCRUM validates only keys in the SCRUM project — mirrors the
// work_items-backed validator without needing a store.
func acceptSCRUM(k string) bool { return len(k) > 6 && k[:6] == "SCRUM-" }

func TestFindTaskKeyChain(t *testing.T) {
	tests := []struct {
		name       string
		branch     string
		commits    []string
		texts      []string
		wantKey    string
		wantSource TaskKeySource
	}{
		{
			name:       "branch wins over all",
			branch:     "feature/SCRUM-42-login",
			commits:    []string{"fix SCRUM-99"},
			texts:      []string{"work on SCRUM-7"},
			wantKey:    "SCRUM-42",
			wantSource: TaskKeyFromBranch,
		},
		{
			name:       "commit when branch has no key",
			branch:     "main",
			commits:    []string{"feat: implement SCRUM-15 flow"},
			texts:      []string{"SCRUM-7"},
			wantKey:    "SCRUM-15",
			wantSource: TaskKeyFromCommit,
		},
		{
			name:       "transcript when branch and commits empty",
			branch:     "main",
			commits:    nil,
			texts:      []string{"please handle SCRUM-7 today"},
			wantKey:    "SCRUM-7",
			wantSource: TaskKeyFromTranscript,
		},
		{
			name:    "unvalidated key dropped (no mislink)",
			branch:  "feature/ABC-1",
			commits: []string{"ref ABC-2"},
			texts:   []string{"ABC-3"},
			wantKey: "",
		},
		{
			name:    "no key anywhere",
			branch:  "main",
			texts:   []string{"just some prose"},
			wantKey: "",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			key, source := FindTaskKeyChain(tt.branch, tt.commits, tt.texts, acceptSCRUM)
			if key != tt.wantKey {
				t.Errorf("key = %q, want %q", key, tt.wantKey)
			}
			if tt.wantKey != "" && source != tt.wantSource {
				t.Errorf("source = %q, want %q", source, tt.wantSource)
			}
		})
	}
}

func TestFindTaskKeyChainNilValidateAcceptsAny(t *testing.T) {
	key, source := FindTaskKeyChain("feature/ABC-9", nil, nil, nil)
	if key != "ABC-9" || source != TaskKeyFromBranch {
		t.Errorf("nil validate should accept any key: %q %q", key, source)
	}
}
