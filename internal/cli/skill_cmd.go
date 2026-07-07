package cli

import (
	"bufio"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/hexops/gotextdiff"
	"github.com/hexops/gotextdiff/myers"
	"github.com/hexops/gotextdiff/span"
	"github.com/spf13/cobra"

	"github.com/phuc-nt/dandori/internal/govern"
	"github.com/phuc-nt/dandori/internal/learn"
	"github.com/phuc-nt/dandori/internal/skillreg"
)

// skillCmd is the P5 pull-only distribution surface for skill-kind
// knowledge units. LOCAL-MODE-ONLY (F3): it reads the same store any other
// dandori subcommand uses (openStore) — a fleet with multiple engineer
// machines, where a skill was published on a DIFFERENT machine's local DB,
// is NOT reachable yet (central mode = [Sau], tracked in plan.md). Both
// subcommands treat "not found here" identically to "never published"
// rather than guessing, since this package cannot tell the two apart without
// a central server.
var skillCmd = &cobra.Command{
	Use:   "skill",
	Short: "Pull reviewed, hash-pinned skills into this repo's .claude/skills/ (pull-only, local-mode)",
}

var flagSkillPullYes bool

var skillListCmd = &cobra.Command{
	Use:   "list",
	Short: "List published skill units with approve-time hash and local install status",
	RunE: func(cmd *cobra.Command, args []string) error {
		_, st, err := openStore()
		if err != nil {
			return err
		}
		defer st.Close()

		skills, err := skillreg.Published(st)
		if err != nil {
			return err
		}
		if len(skills) == 0 {
			fmt.Fprintln(cmd.OutOrStdout(), "no published skill units in this store")
			return nil
		}
		repoRoot, rootErr := findRepoRoot()
		for _, s := range skills {
			auditHash, haErr := skillreg.ApproveHash(st, s.UnitID)
			// compareHash is the hash actually shown for THIS row (audit hash
			// when available, else the row's own content_hash) — L1 compares
			// local installs against this, never s.Hash unconditionally. A
			// tampered row (H1's version-downgrade scenario) can disagree with
			// the audit trail while still equalling its OWN (also-tampered)
			// content_hash; comparing against s.Hash alone would print a false
			// "match" for exactly that case.
			compareHash := s.Hash
			hashDisp := s.Hash
			if haErr == nil {
				compareHash = auditHash
				hashDisp = auditHash
			} else {
				hashDisp = s.Hash + " (audit hash unavailable: " + haErr.Error() + ")"
			}
			status := "missing"
			if rootErr == nil {
				if path, pErr := skillreg.LocalPath(repoRoot, s.Name); pErr == nil {
					if localHash, lErr := skillreg.LocalHash(path); lErr == nil && localHash != "" {
						if localHash == compareHash {
							status = "match"
						} else {
							status = "stale"
						}
					}
				}
			}
			req := ""
			if s.Required {
				req = " [required]"
			}
			fmt.Fprintf(cmd.OutOrStdout(), "• %s%s\n    unit #%d · hash=%s · local=%s\n",
				s.Name, req, s.UnitID, hashDisp, status)
		}
		return nil
	},
}

var skillPullCmd = &cobra.Command{
	Use:   "pull <name|unit-id>",
	Short: "Pull one published skill into this repo's .claude/skills/<name>/SKILL.md",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		cfg, st, err := openStore()
		if err != nil {
			return err
		}
		defer st.Close()

		s, err := skillreg.Get(st, args[0])
		if err != nil {
			if err == skillreg.ErrNotFound {
				// F3 fail-open: clear message, exit cleanly, no crash.
				fmt.Fprintf(cmd.OutOrStdout(), "unit chưa published ở store này (central mode = [Sau]): %q\n", args[0])
				return nil
			}
			return err
		}

		// F7: verify against the audit hash-chain — an INDEPENDENT source
		// from the knowledge_units row skillreg.Get already read.
		auditHash, err := skillreg.ApproveHash(st, s.UnitID)
		if err != nil {
			return fmt.Errorf("cannot read audit approve-hash for unit %d: %w", s.UnitID, err)
		}
		if err := skillreg.Verify(*s, auditHash); err != nil {
			return err
		}

		repoRoot, err := findRepoRoot()
		if err != nil {
			return err
		}
		target, err := skillreg.LocalPath(repoRoot, s.Name)
		if err != nil {
			return err
		}

		existing, _ := os.ReadFile(target) // best-effort: "" if absent
		diffText := textDiff(string(existing), s.Body, target+" (hiện tại)", s.Name+" (pull)")
		fmt.Fprintln(cmd.OutOrStdout(), diffText)

		if !flagSkillPullYes {
			if !confirmPrompt(cmd, fmt.Sprintf("Ghi %s? [y/N] ", target)) {
				fmt.Fprintln(cmd.OutOrStdout(), "huỷ — không ghi gì")
				return nil
			}
		}

		if err := skillreg.Write(target, s.Body); err != nil {
			return fmt.Errorf("write %s: %w", target, err)
		}

		operator := cfg.UserName
		if _, err := learn.RecordUnitAdoption(st, s.UnitID, operator, "", true, cfg.LearnWindowDays); err != nil {
			return fmt.Errorf("write succeeded but RecordUnitAdoption failed: %w", err)
		}
		a := &govern.Audit{St: st, Actor: operator}
		detail := fmt.Sprintf("unit_id=%d name=%s hash=%s actor=%s", s.UnitID, s.Name, s.Hash, operator)
		if _, err := a.Append("skill_pulled", "skill:"+s.Name, detail); err != nil {
			return fmt.Errorf("write succeeded but audit append failed: %w", err)
		}

		fmt.Fprintf(cmd.OutOrStdout(), "pulled %q → %s\n", s.Name, target)
		// v13 P2 anti-Goodhart badge: surface origin on pull output too, not
		// just the web UI, so a CLI-only operator sees whether they just
		// pulled human-authored, imported, ai-drafted, or detector content.
		// Best-effort — skillreg.Skill is deliberately narrower than
		// learn.KnowledgeUnit (doesn't carry Origin), so re-read the unit row
		// directly rather than widening that package's public type for one
		// CLI-only line; a lookup failure here must never fail the pull
		// itself (the write+audit above already succeeded).
		if u, uErr := learn.GetUnit(st, s.UnitID); uErr == nil && u != nil {
			origin := u.Origin
			if origin == "" {
				origin = "human"
			}
			if origin == "ai-draft" && u.OriginModel != "" {
				fmt.Fprintf(cmd.OutOrStdout(), "origin: %s · %s (human-edited & approved)\n", origin, u.OriginModel)
			} else {
				fmt.Fprintf(cmd.OutOrStdout(), "origin: %s\n", origin)
			}
		}
		return nil
	},
}

// textDiff renders a PLAIN TEXT unified diff (F15 — deliberately NOT
// contexthub.DiffHTML, which returns template.HTML meant for web-escaped
// browser rendering, not a terminal). Reuses the same gotextdiff engine
// DiffHTML does, just without HTML wrapping/escaping/coloring.
func textDiff(before, after, fromLabel, toLabel string) string {
	edits := myers.ComputeEdits(span.URIFromPath("a"), before, after)
	unified := gotextdiff.ToUnified(fromLabel, toLabel, before, edits)
	var b strings.Builder
	fmt.Fprintf(&b, "--- %s\n+++ %s\n", fromLabel, toLabel)
	if len(unified.Hunks) == 0 {
		b.WriteString("(không có thay đổi)\n")
		return b.String()
	}
	for _, hunk := range unified.Hunks {
		for _, l := range hunk.Lines {
			text := strings.TrimRight(l.Content, "\n")
			switch l.Kind {
			case gotextdiff.Insert:
				b.WriteString("+ " + text + "\n")
			case gotextdiff.Delete:
				b.WriteString("- " + text + "\n")
			default:
				b.WriteString("  " + text + "\n")
			}
		}
	}
	return b.String()
}

// confirmPrompt is a var so tests can stub stdin reading; production reads
// one line from os.Stdin and accepts y/Y/yes as confirmation.
var confirmPrompt = func(cmd *cobra.Command, prompt string) bool {
	fmt.Fprint(cmd.OutOrStdout(), prompt)
	reader := bufio.NewReader(os.Stdin)
	line, _ := reader.ReadString('\n')
	line = strings.ToLower(strings.TrimSpace(line))
	return line == "y" || line == "yes"
}

// findRepoRoot walks up from the current working directory looking for a
// .git entry (file or directory — a worktree's .git is a file) to locate the
// repo root that owns .claude/skills/. This is deliberately independent of
// `dandori init`'s --project flag (which targets an arbitrary hooked
// project dir): skill pull always targets the repo the operator is
// currently standing in, matching how `git` itself resolves its root.
//
// A var (not a plain func) so tests can stub it to a throwaway temp repo —
// otherwise `go test` (cwd = internal/cli) would walk up to and write real
// files into THIS repo's own .claude/skills/.
var findRepoRoot = func() (string, error) {
	dir, err := os.Getwd()
	if err != nil {
		return "", err
	}
	dir, err = filepath.Abs(dir)
	if err != nil {
		return "", err
	}
	for {
		if _, err := os.Stat(filepath.Join(dir, ".git")); err == nil {
			return dir, nil
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return "", fmt.Errorf("not inside a git repo (no .git found walking up from cwd) — skill pull writes to repo-local .claude/skills/")
		}
		dir = parent
	}
}

func init() {
	skillPullCmd.Flags().BoolVar(&flagSkillPullYes, "yes", false, "skip confirmation prompt")
	skillCmd.AddCommand(skillListCmd, skillPullCmd)
	rootCmd.AddCommand(skillCmd)
}
