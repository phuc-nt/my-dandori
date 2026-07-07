package cli

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path"
	"path/filepath"
	"sort"
	"strings"

	"github.com/spf13/cobra"

	"github.com/phuc-nt/dandori/internal/contexthub"
	"github.com/phuc-nt/dandori/internal/govern"
	"github.com/phuc-nt/dandori/internal/kitpolicy"
	"github.com/phuc-nt/dandori/internal/learn"
	"github.com/phuc-nt/dandori/internal/skillreg"
)

// kitCmd is the nominate-only distribution surface for kind=kit knowledge
// units (P4). Unlike skillCmd (pull-only, P5), kit nominate is the WRITE
// side: it scans this repo's git-tracked .claude/ tree and proposes a new
// kit for review. Pull (kit's own equivalent of skillCmd's pull) is P5's
// responsibility, built on the same internal/kitpolicy this command uses.
var kitCmd = &cobra.Command{
	Use:   "kit",
	Short: "Nominate a kit (bundle of git-tracked .claude/ files) for review",
}

var (
	flagKitTitle    string
	flagKitExclude  []string
	flagKitNominYes bool
)

var kitNominateCmd = &cobra.Command{
	Use:   "nominate <name>",
	Short: "Scan git-tracked .claude/ files, build a manifest, and nominate a kind=kit unit",
	Long: `Scans this repo's git-tracked .claude/ tree (git ls-files — untracked
files are never candidates), applies the kit deny-list first (hooks/,
scripts/, output-styles/, settings.json, settings.local.json — ABORTS the
whole nominate, naming the offending file: this wiring is never
distributable), then the whitelist (agents/, rules/, skills/, commands/).
A whitelisted non-.md file (e.g. skills/x/run.py) is WARNED about by name
and excluded — the kit stays instruction-only rather than aborting.

Every kept file is checked for size (<=64KB) and secret-shaped content;
either failure ABORTS the whole nominate naming the file (no silent skip).
The resulting manifest is printed for review, then confirmed before the
unit + per-file rows are inserted in one transaction.`,
	Args: cobra.ExactArgs(1),
	RunE: runKitNominate,
}

var flagKitPullYes bool

var kitPullCmd = &cobra.Command{
	Use:   "pull <name|unit-id>",
	Short: "Pull one published kit's whitelisted files into this repo's .claude/ tree (one confirm, all-or-nothing)",
	Long: `Verifies the kit manifest 3-way (body hash == row content_hash ==
audit-approved hash), then EVERY per-file body against its manifest entry —
any single mismatch hard-fails the WHOLE pull, nothing is written. Each
file's target path is re-validated through the deny-list/whitelist/depth/
symlink-safe walk (KitLocalPath) before any write; a single unsafe path also
hard-fails the whole pull, naming the offending file. Shows one diff summary
for the whole kit, asks ONE confirmation, then writes every file. A local
file the new kit no longer lists is left untouched (never deleted).`,
	Args: cobra.ExactArgs(1),
	RunE: runKitPull,
}

var kitListCmd = &cobra.Command{
	Use:   "list",
	Short: "List published kit units with approve-time hash and local install status per manifest file",
	RunE:  runKitList,
}

func init() {
	kitNominateCmd.Flags().StringVar(&flagKitTitle, "title", "", "human-readable title (defaults to <name>)")
	kitNominateCmd.Flags().StringArrayVar(&flagKitExclude, "exclude", nil, "glob (matched against the repo-relative path) to exclude; may repeat")
	kitNominateCmd.Flags().BoolVar(&flagKitNominYes, "yes", false, "skip the confirmation prompt")
	kitPullCmd.Flags().BoolVar(&flagKitPullYes, "yes", false, "skip the confirmation prompt")
	kitCmd.AddCommand(kitNominateCmd, kitPullCmd, kitListCmd)
	rootCmd.AddCommand(kitCmd)
}

// gitLsFiles is a var (not a plain func) so tests can stub it to avoid
// depending on a real git worktree containing the exact fixture layout a
// test needs — mirrors findRepoRoot/confirmPrompt's test-injectable pattern.
var gitLsFiles = func(repoRoot, pathspec string) ([]string, error) {
	cmd := exec.Command("git", "ls-files", "--", pathspec)
	cmd.Dir = repoRoot
	out, err := cmd.Output()
	if err != nil {
		return nil, fmt.Errorf("git ls-files: %w", err)
	}
	var files []string
	for _, line := range strings.Split(string(out), "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		files = append(files, filepath.ToSlash(line))
	}
	return files, nil
}

// stripClaudePrefix drops a leading ".claude/" from each path — real `git
// ls-files -- .claude` reports paths repo-root-relative (e.g.
// ".claude/agents/x.md"), but kitpolicy.ValidateKitPath and every stored
// knowledge_kit_files.path are .claude-relative (e.g. "agents/x.md"), the
// same convention skillreg.KitLocalPath's pull-side uses. A path lacking the
// prefix is passed through unchanged rather than dropped: gitLsFiles is a
// test-injectable var (mirrors findRepoRoot/confirmPrompt), and some fakes
// return already-.claude-relative paths directly — passthrough keeps this
// helper a no-op for that shape instead of silently discarding valid input.
func stripClaudePrefix(paths []string) []string {
	const prefix = ".claude/"
	out := make([]string, 0, len(paths))
	for _, p := range paths {
		out = append(out, strings.TrimPrefix(p, prefix))
	}
	return out
}

func runKitNominate(cmd *cobra.Command, args []string) error {
	name := args[0]
	if !learn.ValidSlug(name) {
		return fmt.Errorf("invalid name %q — must match ^[a-z0-9][a-z0-9-]*$", name)
	}
	title := flagKitTitle
	if title == "" {
		title = name
	}

	cfg, st, err := openStore()
	if err != nil {
		return err
	}
	defer st.Close()

	repoRoot, err := findRepoRoot()
	if err != nil {
		return err
	}

	tracked, err := gitLsFiles(repoRoot, ".claude")
	if err != nil {
		return err
	}
	if len(tracked) == 0 {
		fmt.Fprintln(cmd.OutOrStdout(), "không tìm thấy file .claude/ nào được git track")
		return nil
	}
	// `git ls-files -- .claude` reports paths repo-root-relative, i.e.
	// PREFIXED with "'.claude/" (e.g. ".claude/agents/x.md") — kitpolicy's
	// whitelist/deny-list segments (agents/, rules/, hooks/, ...) are defined
	// relative to .claude/ itself, so that prefix must be stripped before
	// validation or every real file would fail ErrNotWhitelisted (top segment
	// ".claude" is not a whitelisted segment) and nominate would silently
	// scan nothing against a real git worktree.
	tracked = stripClaudePrefix(tracked)
	sort.Strings(tracked)

	var kept []string
	var warnings []string
	for _, rel := range tracked {
		err := kitpolicy.ValidateKitPath(rel)
		switch {
		case err == nil:
			kept = append(kept, rel)
		case errorsIsDenied(err):
			return fmt.Errorf("huỷ toàn bộ nominate — %q nằm trong deny-list (hooks/scripts/output-styles/settings.json không bao giờ được phân phối): %w", rel, err)
		case errorsIsNotWhitelisted(err):
			// not a kit candidate at all — silent skip.
			continue
		case errorsIsNonMarkdownInSkill(err):
			warnings = append(warnings, rel)
			continue
		default:
			// ErrInvalidSegment / ErrTooDeep under a whitelisted dir — hard
			// reject naming the file, same abort-and-name behavior as deny.
			return fmt.Errorf("huỷ toàn bộ nominate — đường dẫn không hợp lệ %q: %w", rel, err)
		}
	}

	kept = applyExcludeGlobs(kept, flagKitExclude)

	if len(kept) == 0 {
		fmt.Fprintln(cmd.OutOrStdout(), "không còn file nào sau khi lọc deny-list/whitelist/--exclude")
		return nil
	}

	var files []learn.KitFileInput
	for _, rel := range kept {
		// rel is .claude-relative (prefix stripped above) — same convention
		// skillreg.KitLocalPath uses on the pull side, so the on-disk read
		// here re-adds the ".claude" segment repoRoot itself does not include.
		full := filepath.Join(repoRoot, ".claude", filepath.FromSlash(rel))
		raw, err := os.ReadFile(full)
		if err != nil {
			return fmt.Errorf("read %s: %w", rel, err)
		}
		body := string(raw)
		if len(body) > learn.MaxUnitBodySize {
			return fmt.Errorf("huỷ toàn bộ nominate — %q vượt %d byte", rel, learn.MaxUnitBodySize)
		}
		if frag := contexthub.SecretFragment(body); frag != "" {
			return fmt.Errorf("huỷ toàn bộ nominate — %q chứa chuỗi giống secret (%s)", rel, frag)
		}
		files = append(files, learn.KitFileInput{Path: rel, Body: body})
	}

	if len(files) > learn.MaxKitFiles {
		return fmt.Errorf("kit có %d file, vượt giới hạn %d", len(files), learn.MaxKitFiles)
	}
	manifest, total := learn.BuildKitManifest(files)
	if total > learn.MaxKitTotalBytes {
		return fmt.Errorf("kit tổng %d byte, vượt giới hạn %d", total, learn.MaxKitTotalBytes)
	}

	printKitManifestPreview(cmd, name, manifest, total, warnings)

	if !flagKitNominYes {
		if !confirmPrompt(cmd, fmt.Sprintf("Đề cử kit %q với %d file? [y/N] ", name, len(manifest.Files))) {
			fmt.Fprintln(cmd.OutOrStdout(), "huỷ — không đề cử gì")
			return nil
		}
	}

	unitID, err := learn.NominateUnitTx(st, learn.KitNominateParams{
		Name: name, Title: title, Files: files, NominatedBy: cfg.UserName, Origin: "human",
	})
	if err != nil {
		if err == learn.ErrDuplicateDraft {
			return fmt.Errorf("đã có bản nháp kit %q đang chờ duyệt", name)
		}
		return fmt.Errorf("nominate kit %q: %w", name, err)
	}
	fmt.Fprintf(cmd.OutOrStdout(), "đã đề cử kit %q (unit #%d) — %d file, %d byte\n", name, unitID, len(manifest.Files), total)
	return nil
}

// applyExcludeGlobs drops any kept path matching one of the --exclude
// patterns. Matching is against the full repo-relative slashed path (not
// just the basename) via path.Match per-segment-joined, so a pattern like
// "skills/legacy-*/**" style exclusion still works for the common case of
// "skills/legacy-*" (path.Match doesn't support "**"; documented limitation,
// KISS — a repo needing deeper glob semantics can pass --exclude multiple
// times for each subtree).
func applyExcludeGlobs(paths []string, globs []string) []string {
	if len(globs) == 0 {
		return paths
	}
	var out []string
	for _, p := range paths {
		excluded := false
		for _, g := range globs {
			if ok, _ := path.Match(g, p); ok {
				excluded = true
				break
			}
			// Also try matching against the basename, so a simple pattern
			// like "*.py" (no slash) behaves the way an operator expects
			// even though every kept path here already ends in .md.
			if ok, _ := path.Match(g, path.Base(p)); ok {
				excluded = true
				break
			}
		}
		if !excluded {
			out = append(out, p)
		}
	}
	return out
}

func printKitManifestPreview(cmd *cobra.Command, name string, m learn.KitManifest, total int, warnings []string) {
	fmt.Fprintf(cmd.OutOrStdout(), "\nKit %q — %d file, %d byte:\n", name, len(m.Files), total)
	for _, f := range m.Files {
		fmt.Fprintf(cmd.OutOrStdout(), "  %s (%d byte, %s)\n", f.Path, f.Size, f.ContentHash[:12])
	}
	if len(warnings) > 0 {
		fmt.Fprintln(cmd.OutOrStdout(), "\nCẢNH BÁO — các file sau bị loại (không phải .md trong thư mục kit, H4):")
		for _, w := range warnings {
			fmt.Fprintf(cmd.OutOrStdout(), "  ! %s — kit có thể không chạy độc lập nếu đây là file cần thiết\n", w)
		}
	}
	fmt.Fprintln(cmd.OutOrStdout())
}

func errorsIsDenied(err error) bool         { return errors.Is(err, kitpolicy.ErrDenied) }
func errorsIsNotWhitelisted(err error) bool { return errors.Is(err, kitpolicy.ErrNotWhitelisted) }
func errorsIsNonMarkdownInSkill(err error) bool {
	return errors.Is(err, kitpolicy.ErrNonMarkdownInSkill)
}

// verifyKitManifest performs the manifest-level 3-way hash check — the kit
// counterpart to skillreg.Verify, which is typed specifically to Skill and
// deliberately left untouched (review-audit rule: don't widen Verify's
// signature for a second caller shape). sha256(k.Body) == k.Hash ==
// auditHash; any mismatch hard-fails the whole pull before any per-file
// check even runs.
func verifyKitManifest(k skillreg.Kit, auditHash string) error {
	sum := sha256.Sum256([]byte(k.Body))
	bodyHash := hex.EncodeToString(sum[:])
	if bodyHash != k.Hash {
		return fmt.Errorf("kit manifest hash mismatch: body hash %s != row content_hash %s", bodyHash, k.Hash)
	}
	if k.Hash != auditHash {
		return fmt.Errorf("kit manifest hash mismatch: row content_hash %s != audit-approved hash %s", k.Hash, auditHash)
	}
	return nil
}

// kitPullPlan is one manifest file's resolved pull-time state — computed for
// EVERY file before any write happens, so a single unsafe path or hash
// mismatch anywhere in the kit can hard-fail the whole pull with nothing
// written yet (no partial pull).
type kitPullPlan struct {
	path      string
	target    string // KitLocalPath-resolved absolute path
	body      string // verified new body to write
	localHash string // "" if the file does not exist locally yet
	status    string // "new" | "changed" | "match"
}

// runKitPull implements `dandori kit pull <name|unit-id>` per the P5 spec:
// get published kit -> ApproveHash -> 3-way manifest verify -> per-file body
// verify -> per-file KitLocalPath -> classify -> summary -> ONE confirm ->
// write all -> adoption -> audit. Any failure before the confirm prompt
// aborts with nothing written; any failure of KitLocalPath itself also
// aborts before the confirm prompt is even shown.
func runKitPull(cmd *cobra.Command, args []string) error {
	cfg, st, err := openStore()
	if err != nil {
		return err
	}
	defer st.Close()

	k, err := skillreg.GetKit(st, args[0])
	if err != nil {
		if err == skillreg.ErrNotFound {
			fmt.Fprintf(cmd.OutOrStdout(), "kit unit chưa published ở store này (central mode = [Sau]): %q\n", args[0])
			return nil
		}
		return err
	}

	// F7-equivalent for kits: verify against the audit hash-chain, an
	// INDEPENDENT source from the knowledge_units row GetKit already read.
	auditHash, err := skillreg.ApproveHash(st, k.UnitID)
	if err != nil {
		return fmt.Errorf("cannot read audit approve-hash for kit unit %d: %w", k.UnitID, err)
	}
	if err := verifyKitManifest(*k, auditHash); err != nil {
		return fmt.Errorf("huỷ toàn bộ pull — manifest thất bại xác thực 3 chiều: %w", err)
	}

	manifest, err := learn.ParseKitManifest(k.Body)
	if err != nil {
		return fmt.Errorf("huỷ toàn bộ pull — không parse được manifest: %w", err)
	}
	kitFiles, err := learn.KitFiles(st, k.UnitID)
	if err != nil {
		return err
	}
	if len(kitFiles) != len(manifest.Files) {
		return fmt.Errorf("huỷ toàn bộ pull — manifest có %d file nhưng knowledge_kit_files có %d file (không đồng bộ)",
			len(manifest.Files), len(kitFiles))
	}

	repoRoot, err := findRepoRoot()
	if err != nil {
		return err
	}

	// Per-file verify + per-file path safety, EVERY file, before any write —
	// any single failure hard-fails the WHOLE pull naming the offending file.
	plans := make([]kitPullPlan, 0, len(kitFiles))
	for _, f := range kitFiles {
		mf, ok := manifest.FileByPath(f.Path)
		if !ok {
			return fmt.Errorf("huỷ toàn bộ pull — file %q có trong knowledge_kit_files nhưng không có trong manifest", f.Path)
		}
		sum := sha256.Sum256([]byte(f.Body))
		bodyHash := hex.EncodeToString(sum[:])
		if bodyHash != f.ContentHash || f.ContentHash != mf.ContentHash {
			return fmt.Errorf("huỷ toàn bộ pull — %q lệch hash (body=%s, row=%s, manifest=%s)",
				f.Path, bodyHash, f.ContentHash, mf.ContentHash)
		}

		target, err := skillreg.KitLocalPath(repoRoot, f.Path)
		if err != nil {
			return fmt.Errorf("huỷ toàn bộ pull — đường dẫn không an toàn %q: %w", f.Path, err)
		}

		localHash, err := skillreg.LocalHash(target)
		if err != nil {
			return fmt.Errorf("huỷ toàn bộ pull — đọc file cục bộ %q: %w", target, err)
		}
		status := "match"
		switch {
		case localHash == "":
			status = "new"
		case localHash != f.ContentHash:
			status = "changed"
		}
		plans = append(plans, kitPullPlan{path: f.Path, target: target, body: f.Body, localHash: localHash, status: status})
	}

	printKitPullSummary(cmd, k.Name, plans)

	if !flagKitPullYes {
		if !confirmPrompt(cmd, fmt.Sprintf("Ghi %d file của kit %q vào %s? [y/N] ", len(plans), k.Name, repoRoot)) {
			fmt.Fprintln(cmd.OutOrStdout(), "huỷ — không ghi gì")
			return nil
		}
	}

	// Write all — verification above already guaranteed every target is
	// path-safe, so this loop is write-only (no further validation).
	for _, p := range plans {
		if err := skillreg.Write(p.target, p.body); err != nil {
			return fmt.Errorf("ghi %s thất bại (một phần kit có thể đã được ghi — chạy lại `kit pull` để đồng bộ nốt): %w", p.target, err)
		}
	}

	operator := cfg.UserName
	if _, err := learn.RecordUnitAdoption(st, k.UnitID, operator, "", true, cfg.LearnWindowDays); err != nil {
		return fmt.Errorf("write succeeded but RecordUnitAdoption failed: %w", err)
	}
	a := &govern.Audit{St: st, Actor: operator}
	detail := fmt.Sprintf("unit_id=%d name=%s hash=%s actor=%s files=%d", k.UnitID, k.Name, k.Hash, operator, len(plans))
	if _, err := a.Append("kit_pulled", "kit:"+k.Name, detail); err != nil {
		return fmt.Errorf("write succeeded but audit append failed: %w", err)
	}

	fmt.Fprintf(cmd.OutOrStdout(), "pulled kit %q — %d file ghi vào %s\n", k.Name, len(plans), repoRoot)
	return warnOrphanKitFiles(cmd, repoRoot, manifest)
}

// printKitPullSummary prints one confirm-worthy summary for the whole kit:
// counts by status, then an inline unified diff per changed file when the
// changed set is small enough to actually read (<=10), else just names them.
func printKitPullSummary(cmd *cobra.Command, name string, plans []kitPullPlan) {
	var newFiles, changed, match []kitPullPlan
	for _, p := range plans {
		switch p.status {
		case "new":
			newFiles = append(newFiles, p)
		case "changed":
			changed = append(changed, p)
		default:
			match = append(match, p)
		}
	}
	fmt.Fprintf(cmd.OutOrStdout(), "\nKit %q — %d file: %d mới, %d thay đổi, %d giống hệt\n",
		name, len(plans), len(newFiles), len(changed), len(match))
	for _, p := range newFiles {
		fmt.Fprintf(cmd.OutOrStdout(), "  + %s (mới)\n", p.path)
	}
	if len(changed) > 0 && len(changed) <= 10 {
		for _, p := range changed {
			existing, _ := os.ReadFile(p.target) // best-effort: "" if unreadable
			fmt.Fprintln(cmd.OutOrStdout(), textDiff(string(existing), p.body, p.target+" (hiện tại)", p.path+" (pull)"))
		}
	} else {
		for _, p := range changed {
			fmt.Fprintf(cmd.OutOrStdout(), "  ~ %s (thay đổi)\n", p.path)
		}
	}
	fmt.Fprintln(cmd.OutOrStdout())
}

// warnOrphanKitFiles reports (never deletes, per spec) any file already
// present in this manifest's whitelisted .claude/ subtrees that the CURRENT
// manifest no longer lists — e.g. a file removed in a newer kit version.
// Detection is scoped to the top-level directories the manifest's own files
// live under, so this never wanders into unrelated .claude/ content the kit
// never touched.
func warnOrphanKitFiles(cmd *cobra.Command, repoRoot string, manifest learn.KitManifest) error {
	inManifest := make(map[string]bool, len(manifest.Files))
	topDirs := make(map[string]bool)
	for _, f := range manifest.Files {
		inManifest[f.Path] = true
		if idx := strings.Index(f.Path, "/"); idx > 0 {
			topDirs[f.Path[:idx]] = true
		}
	}
	for top := range topDirs {
		base := filepath.Join(repoRoot, ".claude", top)
		_ = filepath.Walk(base, func(p string, info os.FileInfo, err error) error {
			if err != nil || info.IsDir() {
				return nil
			}
			rel, relErr := filepath.Rel(filepath.Join(repoRoot, ".claude"), p)
			if relErr != nil {
				return nil
			}
			rel = filepath.ToSlash(rel)
			if !inManifest[rel] {
				fmt.Fprintf(cmd.OutOrStdout(), "! %s không còn trong kit — giữ nguyên trên đĩa (không tự xoá)\n", rel)
			}
			return nil
		})
	}
	return nil
}

// runKitList implements `dandori kit list`: published kit units, their
// audit-approved hash, and per-manifest-file local install status
// (match/stale/missing) — the kit counterpart to `skill list`.
func runKitList(cmd *cobra.Command, args []string) error {
	_, st, err := openStore()
	if err != nil {
		return err
	}
	defer st.Close()

	kits, err := skillreg.KitPublished(st)
	if err != nil {
		return err
	}
	if len(kits) == 0 {
		fmt.Fprintln(cmd.OutOrStdout(), "no published kit units in this store")
		return nil
	}
	repoRoot, rootErr := findRepoRoot()
	for _, k := range kits {
		auditHash, haErr := skillreg.ApproveHash(st, k.UnitID)
		hashDisp := k.Hash
		if haErr != nil {
			hashDisp = k.Hash + " (audit hash unavailable: " + haErr.Error() + ")"
		} else if auditHash != k.Hash {
			hashDisp = k.Hash + " (audit mismatch: " + auditHash + ")"
		}
		fmt.Fprintf(cmd.OutOrStdout(), "• %s\n    unit #%d · hash=%s\n", k.Name, k.UnitID, hashDisp)

		manifest, parseErr := learn.ParseKitManifest(k.Body)
		if parseErr != nil {
			fmt.Fprintf(cmd.OutOrStdout(), "    (manifest parse error: %v)\n", parseErr)
			continue
		}
		for _, f := range manifest.Files {
			status := "missing"
			if rootErr == nil {
				if target, pErr := skillreg.KitLocalPath(repoRoot, f.Path); pErr == nil {
					if localHash, lErr := skillreg.LocalHash(target); lErr == nil && localHash != "" {
						if localHash == f.ContentHash {
							status = "match"
						} else {
							status = "stale"
						}
					}
				} else {
					status = "path-unsafe"
				}
			}
			fmt.Fprintf(cmd.OutOrStdout(), "    %s · local=%s\n", f.Path, status)
		}
	}
	return nil
}
