package contexthub

import (
	"path/filepath"
	"sync"
	"testing"

	"github.com/phuc-nt/dandori/internal/store"
)

func testHub(t *testing.T) *Hub {
	t.Helper()
	st, err := store.Open(filepath.Join(t.TempDir(), "t.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	return New(st)
}

func TestSaveAndHead(t *testing.T) {
	h := testHub(t)
	n, err := h.SaveContext(LayerCompany, "*", "chính sách v1", "phuc", "khởi tạo")
	if err != nil || n != 1 {
		t.Fatalf("first save: n=%d err=%v", n, err)
	}
	n2, err := h.SaveContext(LayerCompany, "*", "chính sách v2", "phuc", "sửa")
	if err != nil || n2 != 2 {
		t.Fatalf("second save: n=%d err=%v", n2, err)
	}
	head, err := h.Head(LayerCompany, "*")
	if err != nil || head == nil {
		t.Fatalf("head: %v %v", head, err)
	}
	if head.VersionN != 2 || head.Content != "chính sách v2" {
		t.Errorf("head = v%d %q, want v2", head.VersionN, head.Content)
	}
}

func TestHeadMissingIsNilNil(t *testing.T) {
	h := testHub(t)
	d, err := h.Head(LayerAgent, "ghost")
	if d != nil || err != nil {
		t.Errorf("missing head: %v %v, want nil,nil", d, err)
	}
}

func TestRollbackCopiesOldContent(t *testing.T) {
	h := testHub(t)
	h.SaveContext(LayerTeam, "7", "đội v1", "phuc", "")
	h.SaveContext(LayerTeam, "7", "đội v2 (sai)", "phuc", "")
	n, err := h.Rollback(LayerTeam, "7", 1, "phuc")
	if err != nil || n != 3 {
		t.Fatalf("rollback: n=%d err=%v (want v3)", n, err)
	}
	head, _ := h.Head(LayerTeam, "7")
	if head.Content != "đội v1" {
		t.Errorf("rolled-back head content = %q, want 'đội v1'", head.Content)
	}
	// History intact: v1 and v2 untouched.
	vs, _ := h.Versions(LayerTeam, "7")
	if len(vs) != 3 {
		t.Errorf("versions = %d, want 3 (history never deleted)", len(vs))
	}
	if v1, _ := h.Version(LayerTeam, "7", 1); v1.Content != "đội v1" {
		t.Errorf("v1 mutated: %q", v1.Content)
	}
}

func TestUnknownLayerRejected(t *testing.T) {
	h := testHub(t)
	if _, err := h.SaveContext("planet", "x", "hi", "a", ""); err == nil {
		t.Error("unknown layer must error")
	}
}

func TestSecretRejectedNotWritten(t *testing.T) {
	h := testHub(t)
	cases := []string{
		"deploy với sk-abc12345678 nhé",
		"token: xyzsecretvalue",
		"password: hunter2longenough",
	}
	for _, c := range cases {
		if _, err := h.SaveContext(LayerCompany, "*", c, "phuc", ""); err != ErrSecretInContent {
			t.Errorf("content %q: err=%v, want ErrSecretInContent", c, err)
		}
	}
	// Nothing written.
	if head, _ := h.Head(LayerCompany, "*"); head != nil {
		t.Errorf("secret content wrote a version: %+v", head)
	}
}

// Legit prose that trips the redact regex is rejected too — documented
// false-positive cost (M1). The author reword to ship.
func TestLegitProseWithSecretWordRejected(t *testing.T) {
	h := testHub(t)
	_, err := h.SaveContext(LayerCompany, "*", "Luôn xoay Bearer token hàng tháng.", "phuc", "")
	if err != ErrSecretInContent {
		t.Errorf("prose with 'Bearer token' err=%v, want ErrSecretInContent (accepted false positive)", err)
	}
	if frag := SecretFragment("Luôn xoay Bearer token hàng tháng."); frag == "" {
		t.Error("SecretFragment should name the offending substring")
	}
}

func TestConcurrentSaveDistinctVersions(t *testing.T) {
	h := testHub(t)
	// Seed the doc so both goroutines contend on version_n, not creation.
	h.SaveContext(LayerAgent, "a1", "v1", "phuc", "")
	var wg sync.WaitGroup
	errs := make([]error, 4)
	for i := 0; i < 4; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			_, errs[i] = h.SaveContext(LayerAgent, "a1", "concurrent", "phuc", "")
		}(i)
	}
	wg.Wait()
	for i, e := range errs {
		if e != nil {
			t.Errorf("goroutine %d: %v", i, e)
		}
	}
	vs, _ := h.Versions(LayerAgent, "a1")
	seen := map[int]bool{}
	for _, v := range vs {
		if seen[v.VersionN] {
			t.Fatalf("duplicate version_n %d", v.VersionN)
		}
		seen[v.VersionN] = true
	}
	if len(vs) != 5 { // v1 seed + 4 concurrent
		t.Errorf("versions = %d, want 5", len(vs))
	}
}

func TestListDocsOneRowPerDocAtHead(t *testing.T) {
	h := testHub(t)
	h.SaveContext(LayerCompany, "*", "c1", "phuc", "")
	h.SaveContext(LayerCompany, "*", "c2", "phuc", "")
	h.SaveContext(LayerTeam, "7", "t1", "phuc", "")
	docs, err := h.ListDocs()
	if err != nil {
		t.Fatal(err)
	}
	if len(docs) != 2 {
		t.Fatalf("docs = %d, want 2 (company + team, at head)", len(docs))
	}
	for _, d := range docs {
		if d.Layer == LayerCompany && (d.VersionN != 2 || d.Content != "c2") {
			t.Errorf("company doc not at head: %+v", d)
		}
	}
}
