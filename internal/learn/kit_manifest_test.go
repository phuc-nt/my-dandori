package learn

import (
	"testing"
)

// TestBuildKitManifestSortedByPath proves canonical ordering — the input
// order must never leak into the manifest (map/scan iteration order is not
// guaranteed elsewhere in the codebase, so this is the single load-bearing
// sort point for hash determinism).
func TestBuildKitManifestSortedByPath(t *testing.T) {
	files := []KitFileInput{
		{Path: "rules/z.md", Body: "z"},
		{Path: "agents/a.md", Body: "a"},
		{Path: "rules/m.md", Body: "m"},
	}
	m, total := BuildKitManifest(files)
	if total != 3 {
		t.Fatalf("total=%d, want 3", total)
	}
	want := []string{"agents/a.md", "rules/m.md", "rules/z.md"}
	if len(m.Files) != len(want) {
		t.Fatalf("got %d files, want %d", len(m.Files), len(want))
	}
	for i, w := range want {
		if m.Files[i].Path != w {
			t.Errorf("Files[%d].Path = %q, want %q", i, m.Files[i].Path, w)
		}
	}
}

// TestKitManifestHashDeterminism proves the manifest content_hash is stable
// regardless of the INPUT order the caller supplies files in — this is the
// risk-table item "Manifest hash unstable (map ordering)" made concrete: two
// callers building the same file set in different orders must produce the
// identical manifest hash.
func TestKitManifestHashDeterminism(t *testing.T) {
	a := []KitFileInput{
		{Path: "rules/z.md", Body: "z-content"},
		{Path: "agents/a.md", Body: "a-content"},
	}
	b := []KitFileInput{
		{Path: "agents/a.md", Body: "a-content"},
		{Path: "rules/z.md", Body: "z-content"},
	}
	mA, _ := BuildKitManifest(a)
	mB, _ := BuildKitManifest(b)
	hA, err := mA.ContentHash()
	if err != nil {
		t.Fatal(err)
	}
	hB, err := mB.ContentHash()
	if err != nil {
		t.Fatal(err)
	}
	if hA != hB {
		t.Errorf("manifest hash differs by input order: %s vs %s", hA, hB)
	}

	// Also prove the hash actually depends on content — changing one file's
	// body must change the hash (sanity, not just "always equal").
	c := []KitFileInput{
		{Path: "agents/a.md", Body: "a-content"},
		{Path: "rules/z.md", Body: "DIFFERENT"},
	}
	mC, _ := BuildKitManifest(c)
	hC, err := mC.ContentHash()
	if err != nil {
		t.Fatal(err)
	}
	if hC == hA {
		t.Error("manifest hash did not change when file content changed")
	}
}

// TestKitManifestRoundTrip proves MarshalCanonical → ParseKitManifest
// recovers the exact same file entries (path/hash/size).
func TestKitManifestRoundTrip(t *testing.T) {
	files := []KitFileInput{
		{Path: "skills/x/SKILL.md", Body: "skill body"},
		{Path: "rules/r.md", Body: "rule body"},
	}
	m, _ := BuildKitManifest(files)
	body, err := m.MarshalCanonical()
	if err != nil {
		t.Fatal(err)
	}
	parsed, err := ParseKitManifest(body)
	if err != nil {
		t.Fatal(err)
	}
	if len(parsed.Files) != len(m.Files) {
		t.Fatalf("round-trip file count = %d, want %d", len(parsed.Files), len(m.Files))
	}
	for i := range m.Files {
		if parsed.Files[i] != m.Files[i] {
			t.Errorf("round-trip[%d] = %+v, want %+v", i, parsed.Files[i], m.Files[i])
		}
	}

	f, ok := parsed.FileByPath("rules/r.md")
	if !ok {
		t.Fatal("FileByPath: rules/r.md not found after round-trip")
	}
	if f.Size != len("rule body") {
		t.Errorf("FileByPath size = %d, want %d", f.Size, len("rule body"))
	}
	if _, ok := parsed.FileByPath("does/not/exist.md"); ok {
		t.Error("FileByPath: expected ok=false for missing path")
	}
}

// TestNominateUnitTxOneTxUnitAndFiles proves the unit row + ALL
// knowledge_kit_files rows land together, and are queryable as kind=kit.
func TestNominateUnitTxOneTxUnitAndFiles(t *testing.T) {
	st := testStore(t)
	files := []KitFileInput{
		{Path: "agents/reviewer.md", Body: "review body"},
		{Path: "rules/dev.md", Body: "dev rules body"},
	}
	id, err := NominateUnitTx(st, KitNominateParams{
		Name: "agent-pack", Title: "Agent Pack", Files: files, NominatedBy: "tester",
	})
	if err != nil {
		t.Fatalf("NominateUnitTx: %v", err)
	}

	u, err := GetUnit(st, id)
	if err != nil || u == nil {
		t.Fatalf("GetUnit: %+v err=%v", u, err)
	}
	if u.Kind != KindKit || u.State != StateNominated {
		t.Fatalf("unit kind/state = %s/%s, want kit/nominated", u.Kind, u.State)
	}
	if u.ContentHash == "" {
		t.Error("expected non-empty content_hash on kit unit")
	}

	var fileCount int
	if err := st.DB.QueryRow(`SELECT count(*) FROM knowledge_kit_files WHERE unit_id = ?`, id).Scan(&fileCount); err != nil {
		t.Fatal(err)
	}
	if fileCount != 2 {
		t.Errorf("knowledge_kit_files count = %d, want 2", fileCount)
	}

	// The unit body must parse as a manifest whose per-file hashes match the
	// knowledge_kit_files rows (Merkle-lite binding).
	m, err := ParseKitManifest(u.Body)
	if err != nil {
		t.Fatalf("parse manifest: %v", err)
	}
	if len(m.Files) != 2 {
		t.Fatalf("manifest file count = %d, want 2", len(m.Files))
	}
	rows, err := st.DB.Query(`SELECT path, content_hash FROM knowledge_kit_files WHERE unit_id = ?`, id)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	seen := 0
	for rows.Next() {
		var path, hash string
		if err := rows.Scan(&path, &hash); err != nil {
			t.Fatal(err)
		}
		mf, ok := m.FileByPath(path)
		if !ok {
			t.Errorf("manifest missing file row path %q", path)
			continue
		}
		if mf.ContentHash != hash {
			t.Errorf("manifest hash for %q = %s, row hash = %s — mismatch", path, mf.ContentHash, hash)
		}
		seen++
	}
	if seen != 2 {
		t.Errorf("verified %d file rows, want 2", seen)
	}
}

// TestNominateUnitTxRollbackOnCapViolation proves the pre-tx cap checks
// refuse before any row is written — MaxKitFiles/MaxKitTotalBytes must never
// let a partial kit land.
func TestNominateUnitTxRollbackOnCapViolation(t *testing.T) {
	st := testStore(t)
	files := make([]KitFileInput, MaxKitFiles+1)
	for i := range files {
		files[i] = KitFileInput{Path: "rules/f.md", Body: "x"}
	}
	if _, err := NominateUnitTx(st, KitNominateParams{
		Name: "too-many-files", Title: "t", Files: files, NominatedBy: "tester",
	}); err == nil {
		t.Fatal("expected error for exceeding MaxKitFiles")
	}
	var n int
	st.DB.QueryRow(`SELECT count(*) FROM knowledge_units WHERE kind = ? AND name = ?`, KindKit, "too-many-files").Scan(&n)
	if n != 0 {
		t.Errorf("expected no unit row written on cap violation, found %d", n)
	}
}

// TestNominateUnitTxDuplicateDraftRejected mirrors NominateUnit's own M1
// dedup guard for kit units.
func TestNominateUnitTxDuplicateDraftRejected(t *testing.T) {
	st := testStore(t)
	files := []KitFileInput{{Path: "rules/d.md", Body: "body"}}
	if _, err := NominateUnitTx(st, KitNominateParams{
		Name: "dup-kit", Title: "t", Files: files, NominatedBy: "tester",
	}); err != nil {
		t.Fatalf("first nominate: %v", err)
	}
	_, err := NominateUnitTx(st, KitNominateParams{
		Name: "dup-kit", Title: "t", Files: files, NominatedBy: "tester",
	})
	if err == nil {
		t.Fatal("expected ErrDuplicateDraft on second nominate for same name")
	}
}
