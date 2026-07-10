package govern

import (
	"path/filepath"
	"sync"
	"testing"

	"github.com/phuc-nt/dandori/internal/store"
)

// Hook commands run as separate processes from the serve loop, so two writers
// can append audit entries at the same time. The chain stays intact only if
// tip-read + insert is atomic across connections (_txlock=immediate).
func TestAuditChainConcurrentWriters(t *testing.T) {
	path := filepath.Join(t.TempDir(), "c.db")
	s1, err := store.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer s1.Close()
	s2, err := store.Open(path) // second connection = second "process"
	if err != nil {
		t.Fatal(err)
	}
	defer s2.Close()

	const perWriter = 20
	var wg sync.WaitGroup
	for _, st := range []*store.Store{s1, s2} {
		wg.Add(1)
		go func(st *store.Store) {
			defer wg.Done()
			a := &Audit{St: st, Actor: "writer"}
			for i := 0; i < perWriter; i++ {
				if _, err := a.Append("act", "subj", "detail"); err != nil {
					t.Errorf("append: %v", err)
					return
				}
			}
		}(st)
	}
	wg.Wait()

	var count int
	s1.DB.QueryRow(`SELECT count(*) FROM audit_log`).Scan(&count)
	if count != 2*perWriter {
		t.Fatalf("entries: %d, want %d", count, 2*perWriter)
	}
	if broken, reason, err := Verify(s1); err != nil || reason != "" {
		t.Errorf("chain broken at %d reason=%q (err %v) under concurrent writers", broken, reason, err)
	}
}
