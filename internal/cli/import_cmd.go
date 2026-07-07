package cli

import (
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"

	"github.com/spf13/cobra"

	"github.com/phuc-nt/dandori/internal/contexthub"
	"github.com/phuc-nt/dandori/internal/learn"
	"github.com/phuc-nt/dandori/internal/store"
)

// dandori knowledge import lifts memory/journal .md files into kind=context
// knowledge units (v13 P2). Import is human-explicit per-file: the person
// running the command sees the FULL body and confirms — that confirmation IS
// the opt-in (design §4.4). This command never auto-watches a directory; it
// only runs when an operator explicitly invokes it.
const (
	originImportMemory  = "import-memory"
	originImportJournal = "import-journal"
)

var (
	flagImportMemory   bool
	flagImportJournals bool
	flagImportProject  string
	flagImportFile     string
	flagImportAll      bool
	flagImportYes      bool
)

var importCmd = &cobra.Command{
	Use:   "import",
	Short: "Import memory/journal .md files into kind=context knowledge units (per-file preview+confirm)",
	Long: `Scans ~/.claude/projects/<project>/memory/*.md (--memory) and/or
docs/journals/*.md in the current repo (--journals), previews each candidate
file's FULL body, and — after confirmation — nominates it as a kind=context
knowledge unit with origin=import-memory|import-journal. This is a snapshot,
not a sync: re-running after editing the source file creates a new draft
version through the normal (kind,name) supersede lineage; an unchanged file
is skipped ("không đổi").`,
	RunE: runKnowledgeImport,
}

func init() {
	importCmd.Flags().BoolVar(&flagImportMemory, "memory", false, "scan ~/.claude/projects/<project>/memory/*.md")
	importCmd.Flags().BoolVar(&flagImportJournals, "journals", false, "scan <repo>/docs/journals/*.md")
	importCmd.Flags().StringVar(&flagImportProject, "project", "", "project slug under ~/.claude/projects (required with --memory)")
	importCmd.Flags().StringVar(&flagImportFile, "file", "", "import exactly one file (any source, bypasses scan)")
	importCmd.Flags().BoolVar(&flagImportAll, "all", false, "import every scanned candidate without an interactive picker")
	importCmd.Flags().BoolVar(&flagImportYes, "yes", false, "skip the per-file confirmation prompt")
	knowledgeCmd.AddCommand(importCmd)
}

// importCandidate is one .md file scanned for import, before frontmatter
// parsing — kept separate from the parsed result so the scan step never
// itself reads bytes (a picker can list candidates cheaply).
type importCandidate struct {
	path   string
	origin string // originImportMemory | originImportJournal
}

func runKnowledgeImport(cmd *cobra.Command, args []string) error {
	if !flagImportMemory && !flagImportJournals && flagImportFile == "" {
		return fmt.Errorf("nothing to do — pass --memory, --journals, or --file <path>")
	}

	cfg, st, err := openStore()
	if err != nil {
		return err
	}
	defer st.Close()

	var candidates []importCandidate

	if flagImportFile != "" {
		// --file bypasses the scan entirely; origin is inferred from the path
		// (best-effort label) so a one-off import still carries a meaningful
		// badge instead of a blank one.
		origin := originImportMemory
		if strings.Contains(flagImportFile, string(filepath.Separator)+"journals"+string(filepath.Separator)) {
			origin = originImportJournal
		}
		candidates = append(candidates, importCandidate{path: flagImportFile, origin: origin})
	}

	if flagImportMemory {
		if flagImportProject == "" {
			return fmt.Errorf("--memory requires --project <slug>")
		}
		mem, err := scanMemoryFiles(cfg.ProjectsDir, flagImportProject)
		if err != nil {
			return err
		}
		candidates = append(candidates, mem...)
	}

	if flagImportJournals {
		repoRoot, err := findRepoRoot()
		if err != nil {
			return err
		}
		jr, err := scanJournalFiles(repoRoot)
		if err != nil {
			return err
		}
		candidates = append(candidates, jr...)
	}

	if len(candidates) == 0 {
		fmt.Fprintln(cmd.OutOrStdout(), "không tìm thấy file .md nào để import")
		return nil
	}

	selected := candidates
	if !flagImportAll && flagImportFile == "" && len(candidates) > 1 {
		selected, err = pickImportCandidates(cmd, candidates)
		if err != nil {
			return err
		}
	}

	imported, skipped, rejected := 0, 0, 0
	for _, c := range selected {
		outcome, err := importOneFile(cmd, st, cfg.UserName, c)
		if err != nil {
			return err
		}
		switch outcome {
		case importOutcomeImported:
			imported++
		case importOutcomeSkippedDup:
			skipped++
		case importOutcomeRejected:
			rejected++
		}
	}

	fmt.Fprintf(cmd.OutOrStdout(), "imported %d, skipped (không đổi) %d, rejected %d\n", imported, skipped, rejected)
	return nil
}

// scanMemoryFiles globs ~/.claude/projects/<project>/memory/*.md, excluding
// MEMORY.md (the auto-generated index, not a candidate — per spec).
func scanMemoryFiles(projectsDir, project string) ([]importCandidate, error) {
	pattern := filepath.Join(projectsDir, project, "memory", "*.md")
	matches, err := filepath.Glob(pattern)
	if err != nil {
		return nil, fmt.Errorf("scan memory: %w", err)
	}
	var out []importCandidate
	for _, m := range matches {
		if filepath.Base(m) == "MEMORY.md" {
			continue
		}
		out = append(out, importCandidate{path: m, origin: originImportMemory})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].path < out[j].path })
	return out, nil
}

// scanJournalFiles globs <repoRoot>/docs/journals/*.md.
func scanJournalFiles(repoRoot string) ([]importCandidate, error) {
	pattern := filepath.Join(repoRoot, "docs", "journals", "*.md")
	matches, err := filepath.Glob(pattern)
	if err != nil {
		return nil, fmt.Errorf("scan journals: %w", err)
	}
	var out []importCandidate
	for _, m := range matches {
		out = append(out, importCandidate{path: m, origin: originImportJournal})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].path < out[j].path })
	return out, nil
}

// pickImportCandidates prints a numbered list and reads a comma/space
// separated selection from stdin ("1,3" or "all"). Kept simple (KISS) —
// no TUI dependency for a low-frequency CLI operator flow.
func pickImportCandidates(cmd *cobra.Command, candidates []importCandidate) ([]importCandidate, error) {
	fmt.Fprintln(cmd.OutOrStdout(), "Tìm thấy các file sau:")
	for i, c := range candidates {
		fmt.Fprintf(cmd.OutOrStdout(), "  [%d] %s (%s)\n", i+1, c.path, c.origin)
	}
	fmt.Fprint(cmd.OutOrStdout(), "Chọn số (vd: 1,3) hoặc 'all': ")
	line := readLine()
	line = strings.TrimSpace(line)
	if line == "" || strings.EqualFold(line, "all") {
		return candidates, nil
	}
	var out []importCandidate
	for _, tok := range strings.FieldsFunc(line, func(r rune) bool { return r == ',' || r == ' ' }) {
		var idx int
		if _, err := fmt.Sscanf(tok, "%d", &idx); err != nil || idx < 1 || idx > len(candidates) {
			continue
		}
		out = append(out, candidates[idx-1])
	}
	return out, nil
}

// readLine is a var so tests can stub stdin — mirrors confirmPrompt's
// pattern in skill_cmd.go (test-injectable, production reads os.Stdin).
var readLine = func() string {
	var line string
	fmt.Scanln(&line)
	return line
}

type importOutcome int

const (
	importOutcomeImported importOutcome = iota
	importOutcomeSkippedDup
	importOutcomeRejected
)

var frontmatterRe = regexp.MustCompile(`(?s)^---\s*\n(.*?\n)---\s*\n(.*)$`)

// parsedImportFile is the result of parsing one candidate's frontmatter.
type parsedImportFile struct {
	name        string
	description string
	body        string // full body (frontmatter block stripped when present)
}

// parseFrontmatter does a minimal --- delimited YAML-ish key:value split
// (no existing frontmatter parser found in this codebase — grepped for
// "frontmatter", none). Tolerates missing/malformed frontmatter: falls back
// to filename-derived name and the whole file as body.
func parseFrontmatter(fileBase, raw string) parsedImportFile {
	m := frontmatterRe.FindStringSubmatch(raw)
	if m == nil {
		return parsedImportFile{name: fileBase, body: raw}
	}
	fields := map[string]string{}
	for _, line := range strings.Split(m[1], "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		k, v, ok := strings.Cut(line, ":")
		if !ok {
			continue
		}
		k = strings.TrimSpace(k)
		v = strings.Trim(strings.TrimSpace(v), `"'`)
		fields[k] = v
	}
	name := fields["name"]
	if name == "" {
		name = fileBase
	}
	return parsedImportFile{
		name:        name,
		description: fields["description"],
		body:        m[2],
	}
}

// slugify turns an arbitrary name/filename into a ValidSlug candidate:
// lowercase, non [a-z0-9-] runs collapsed to '-', trimmed of leading/
// trailing '-', capped to learn.MaxSlugLen (M6).
func slugify(s string) string {
	s = strings.ToLower(s)
	var b strings.Builder
	lastDash := false
	for _, r := range s {
		switch {
		case r >= 'a' && r <= 'z' || r >= '0' && r <= '9':
			b.WriteRune(r)
			lastDash = false
		default:
			if !lastDash && b.Len() > 0 {
				b.WriteByte('-')
				lastDash = true
			}
		}
	}
	out := strings.Trim(b.String(), "-")
	if len(out) > learn.MaxSlugLen {
		out = strings.Trim(out[:learn.MaxSlugLen], "-")
	}
	return out
}

// importOneFile runs the full per-file pipeline: read, size cap, secret
// scan, full preview, confirm, dedup, NominateUnit. Errors returned here are
// I/O/DB failures that should stop the whole import run; content rejections
// (too big, secret-shaped, empty slug) print a message and return
// importOutcomeRejected instead of erroring, so one bad file doesn't abort a
// batch import.
func importOneFile(cmd *cobra.Command, st *store.Store, actor string, c importCandidate) (importOutcome, error) {
	raw, err := os.ReadFile(c.path)
	if err != nil {
		return importOutcomeRejected, fmt.Errorf("read %s: %w", c.path, err)
	}
	if len(raw) > learn.MaxUnitBodySize {
		fmt.Fprintf(cmd.OutOrStdout(), "bỏ qua %s: vượt %d byte — dùng --file để trích đoạn nhỏ hơn thay vì import cả file\n",
			c.path, learn.MaxUnitBodySize)
		return importOutcomeRejected, nil
	}

	fileBase := strings.TrimSuffix(filepath.Base(c.path), filepath.Ext(c.path))
	parsed := parseFrontmatter(fileBase, string(raw))

	if frag := contexthub.SecretFragment(parsed.body); frag != "" {
		fmt.Fprintf(cmd.OutOrStdout(), "bỏ qua %s: nội dung chứa chuỗi giống secret (%s)\n", c.path, frag)
		return importOutcomeRejected, nil
	}

	slug := slugify(parsed.name)
	if !learn.ValidSlug(slug) {
		slug = slugify(fileBase)
	}
	if !learn.ValidSlug(slug) {
		fmt.Fprintf(cmd.OutOrStdout(), "bỏ qua %s: không suy ra được tên slug hợp lệ\n", c.path)
		return importOutcomeRejected, nil
	}

	title := parsed.description
	if title == "" {
		title = parsed.name
	}
	if title == "" {
		title = fileBase
	}

	fmt.Fprintf(cmd.OutOrStdout(), "\n--- %s (%s) ---\nslug: %s\ntitle: %s\n\n%s\n---\n", c.path, c.origin, slug, title, parsed.body)

	hash := sha256Hex(parsed.body)
	dup, err := contextUnitUnchanged(st, slug, hash)
	if err != nil {
		return importOutcomeRejected, err
	}
	if dup {
		fmt.Fprintf(cmd.OutOrStdout(), "không đổi — bỏ qua %s (nội dung trùng bản đang chờ/đã publish)\n", c.path)
		return importOutcomeSkippedDup, nil
	}

	if !flagImportYes {
		if !confirmPrompt(cmd, fmt.Sprintf("Đề cử %q từ %s? [y/N] ", slug, c.path)) {
			fmt.Fprintln(cmd.OutOrStdout(), "huỷ — bỏ qua file này")
			return importOutcomeRejected, nil
		}
	}

	_, err = learn.NominateUnit(st, learn.NominateParams{
		Kind: learn.KindContext, Name: slug, Title: title, Body: parsed.body,
		Origin:         c.origin,
		NominatedBy:    actor,
		TransitionNote: fmt.Sprintf("imported from %s", c.path),
	})
	if err != nil {
		if err == learn.ErrDuplicateDraft {
			fmt.Fprintf(cmd.OutOrStdout(), "đã có bản nháp đang chờ duyệt cho %q — bỏ qua %s\n", slug, c.path)
			return importOutcomeSkippedDup, nil
		}
		return importOutcomeRejected, fmt.Errorf("nominate %s: %w", c.path, err)
	}
	fmt.Fprintf(cmd.OutOrStdout(), "đã đề cử %q ← %s (nguồn: %s)\n", slug, c.path, c.origin)
	return importOutcomeImported, nil
}

// contextUnitUnchanged reports whether a live/draft context unit named slug
// already carries this exact content_hash — the per-spec dedup query
// (kind='context', name=slug, state IN the non-terminal/live set). A
// changed-content re-import is NOT a dup; NominateUnit's own supersede
// lineage handles that as a new draft version.
func contextUnitUnchanged(st *store.Store, slug, hash string) (bool, error) {
	var existing string
	err := st.Read().QueryRow(
		`SELECT content_hash FROM knowledge_units
		 WHERE kind = 'context' AND name = ?
		   AND state IN ('nominated','in_review','published','adopted','measured')
		 ORDER BY version_n DESC LIMIT 1`, slug,
	).Scan(&existing)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return false, nil
		}
		return false, err
	}
	return existing == hash, nil
}

func sha256Hex(s string) string {
	sum := sha256.Sum256([]byte(s))
	return hex.EncodeToString(sum[:])
}
