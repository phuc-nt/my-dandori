package cli

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/phuc-nt/dandori/internal/store"
)

// writeConfigYAML writes a minimal config.yaml pointing projects_dir at a
// temp dir, so --memory scans a fixture directory instead of the real
// ~/.claude/projects on the machine running `go test`.
func writeConfigYAML(t *testing.T, projectsDir string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "config.yaml")
	content := "projects_dir: " + projectsDir + "\nuser_name: tester\n"
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestImportMemoryFixtureSetsOrigin(t *testing.T) {
	db := tempDB(t)
	projectsDir := t.TempDir()
	memDir := filepath.Join(projectsDir, "my-proj", "memory")
	if err := os.MkdirAll(memDir, 0o755); err != nil {
		t.Fatal(err)
	}
	fixture := "---\nname: honest-zero-vs-capture-gap\ndescription: Before fixing a zero metric\n---\nBody content about honest zero handling.\n"
	if err := os.WriteFile(filepath.Join(memDir, "honest-zero.md"), []byte(fixture), 0o644); err != nil {
		t.Fatal(err)
	}
	// MEMORY.md must be excluded from the scan.
	if err := os.WriteFile(filepath.Join(memDir, "MEMORY.md"), []byte("# index"), 0o644); err != nil {
		t.Fatal(err)
	}

	cfgPath := writeConfigYAML(t, projectsDir)
	out, err := runImportCLI(t, cfgPath, db, "knowledge", "import", "--memory", "--project", "my-proj", "--all", "--yes")
	if err != nil {
		t.Fatalf("import: %v\noutput:\n%s", err, out)
	}
	if !strings.Contains(out, "imported 1") {
		t.Errorf("expected 1 imported, got:\n%s", out)
	}

	st, err := store.Open(db)
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()

	var origin, name, note string
	if err := st.DB.QueryRow(`SELECT origin, name FROM knowledge_units WHERE kind = 'context'`).
		Scan(&origin, &name); err != nil {
		t.Fatalf("read unit: %v", err)
	}
	if origin != "import-memory" {
		t.Errorf("origin = %q, want import-memory", origin)
	}
	if name != "honest-zero-vs-capture-gap" {
		t.Errorf("name = %q, want frontmatter-derived slug", name)
	}
	if err := st.DB.QueryRow(
		`SELECT note FROM knowledge_transitions WHERE unit_id = (SELECT id FROM knowledge_units WHERE name = ?) ORDER BY id LIMIT 1`,
		name,
	).Scan(&note); err != nil {
		t.Fatalf("read transition note: %v", err)
	}
	if !strings.Contains(note, "honest-zero.md") {
		t.Errorf("transition note = %q, want source path mentioned", note)
	}
}

func TestImportMemoryIdenticalReimportSkipped(t *testing.T) {
	db := tempDB(t)
	projectsDir := t.TempDir()
	memDir := filepath.Join(projectsDir, "my-proj", "memory")
	if err := os.MkdirAll(memDir, 0o755); err != nil {
		t.Fatal(err)
	}
	fixture := "---\nname: repeat-note\n---\nsame body every time\n"
	if err := os.WriteFile(filepath.Join(memDir, "repeat-note.md"), []byte(fixture), 0o644); err != nil {
		t.Fatal(err)
	}
	cfgPath := writeConfigYAML(t, projectsDir)

	out1, err := runImportCLI(t, cfgPath, db, "knowledge", "import", "--memory", "--project", "my-proj", "--all", "--yes")
	if err != nil {
		t.Fatalf("first import: %v\n%s", err, out1)
	}
	if !strings.Contains(out1, "imported 1") {
		t.Fatalf("expected first import to land 1 row, got:\n%s", out1)
	}

	out2, err := runImportCLI(t, cfgPath, db, "knowledge", "import", "--memory", "--project", "my-proj", "--all", "--yes")
	if err != nil {
		t.Fatalf("second import: %v\n%s", err, out2)
	}
	if !strings.Contains(out2, "không đổi") {
		t.Errorf("expected 'không đổi' skip message on identical re-import, got:\n%s", out2)
	}

	st, err := store.Open(db)
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	var n int
	st.DB.QueryRow(`SELECT count(*) FROM knowledge_units WHERE name = 'repeat-note'`).Scan(&n)
	if n != 1 {
		t.Errorf("row count after identical re-import = %d, want 1 (no duplicate)", n)
	}
}

func TestImportOversizedFileRejected(t *testing.T) {
	db := tempDB(t)
	projectsDir := t.TempDir()
	memDir := filepath.Join(projectsDir, "my-proj", "memory")
	if err := os.MkdirAll(memDir, 0o755); err != nil {
		t.Fatal(err)
	}
	big := strings.Repeat("x", 70*1024) // 70KB > 64KB cap
	if err := os.WriteFile(filepath.Join(memDir, "huge.md"), []byte(big), 0o644); err != nil {
		t.Fatal(err)
	}
	cfgPath := writeConfigYAML(t, projectsDir)

	out, err := runImportCLI(t, cfgPath, db, "knowledge", "import", "--memory", "--project", "my-proj", "--all", "--yes")
	if err != nil {
		t.Fatalf("import: %v\n%s", err, out)
	}
	if !strings.Contains(out, "vượt") {
		t.Errorf("expected oversize rejection guidance, got:\n%s", out)
	}
	if !strings.Contains(out, "rejected 1") {
		t.Errorf("expected 1 rejected, got:\n%s", out)
	}

	st, err := store.Open(db)
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	var n int
	st.DB.QueryRow(`SELECT count(*) FROM knowledge_units`).Scan(&n)
	if n != 0 {
		t.Errorf("expected no rows for oversized file, got %d", n)
	}
}

func TestImportSecretShapedFileRejected(t *testing.T) {
	db := tempDB(t)
	projectsDir := t.TempDir()
	memDir := filepath.Join(projectsDir, "my-proj", "memory")
	if err := os.MkdirAll(memDir, 0o755); err != nil {
		t.Fatal(err)
	}
	// A secret-shaped fragment matching internal/redact's detector
	// (key/secret/token/password: value — see redact.go's secretRe).
	secretBody := "config notes\napi_key: sk-abcdefghijklmnop\n"
	if err := os.WriteFile(filepath.Join(memDir, "leaky.md"), []byte(secretBody), 0o644); err != nil {
		t.Fatal(err)
	}
	cfgPath := writeConfigYAML(t, projectsDir)

	out, err := runImportCLI(t, cfgPath, db, "knowledge", "import", "--memory", "--project", "my-proj", "--all", "--yes")
	if err != nil {
		t.Fatalf("import: %v\n%s", err, out)
	}
	if !strings.Contains(out, "leaky.md") || !strings.Contains(out, "secret") {
		t.Errorf("expected secret-shaped rejection naming the file, got:\n%s", out)
	}

	st, err := store.Open(db)
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	var n int
	st.DB.QueryRow(`SELECT count(*) FROM knowledge_units`).Scan(&n)
	if n != 0 {
		t.Errorf("expected no rows for secret-shaped file, got %d", n)
	}
}

func TestSlugify(t *testing.T) {
	cases := map[string]string{
		"Honest Zero vs Capture Gap": "honest-zero-vs-capture-gap",
		"already-kebab-case":         "already-kebab-case",
		"  leading/trailing  ":       "leading-trailing",
		"Multiple___Underscores":     "multiple-underscores",
	}
	for in, want := range cases {
		if got := slugify(in); got != want {
			t.Errorf("slugify(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestParseFrontmatterFallsBackToFilename(t *testing.T) {
	p := parseFrontmatter("my-file", "no frontmatter here, just body text")
	if p.name != "my-file" {
		t.Errorf("name = %q, want fallback my-file", p.name)
	}
	if p.body != "no frontmatter here, just body text" {
		t.Errorf("body mismatch: %q", p.body)
	}
}

func TestParseFrontmatterExtractsFields(t *testing.T) {
	raw := "---\nname: my-name\ndescription: my desc\ntype: note\n---\nthe body\n"
	p := parseFrontmatter("fallback", raw)
	if p.name != "my-name" {
		t.Errorf("name = %q, want my-name", p.name)
	}
	if p.description != "my desc" {
		t.Errorf("description = %q, want my desc", p.description)
	}
	if p.body != "the body\n" {
		t.Errorf("body = %q, want %q", p.body, "the body\n")
	}
}

// runImportCLI runs rootCmd with --config first (must precede --db per
// cobra persistent-flag parsing used elsewhere in this package).
func runImportCLI(t *testing.T, cfgPath, db string, args ...string) (string, error) {
	t.Helper()
	return execCLI(t, db, append([]string{"--config", cfgPath}, args...)...)
}
