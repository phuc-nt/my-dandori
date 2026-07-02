package ingest

import (
	"bufio"
	"encoding/json"
	"os"
	"path/filepath"
)

// Spool is the on-disk queue for records that could not (yet) be delivered.
// Records are redacted BEFORE they are written (red-team L1) — the spool must
// never hold a raw secret. Appends are O_APPEND single-line writes (atomic
// for our sizes); flush is serialized by a lock file in client.go.
const spoolMaxBytes = 10 << 20 // 10 MB — beyond this the oldest half is dropped

func spoolPath() string {
	home, _ := os.UserHomeDir()
	return filepath.Join(home, ".dandori", "spool.jsonl")
}

// spoolAppend writes one already-redacted record line.
func spoolAppend(rec Record) error {
	path := spoolPath()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	rotateIfHuge(path)
	b, err := json.Marshal(rec)
	if err != nil {
		return err
	}
	f, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o600)
	if err != nil {
		return err
	}
	defer f.Close()
	_, err = f.Write(append(b, '\n'))
	return err
}

// spoolDrain reads all spooled records and truncates the file. The caller
// holds the flush lock; if the subsequent POST fails the records are
// re-appended by the caller (they are still in memory).
func spoolDrain() ([]Record, error) {
	f, err := os.Open(spoolPath())
	if os.IsNotExist(err) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var recs []Record
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 0, 64*1024), 1<<20)
	for sc.Scan() {
		var r Record
		if json.Unmarshal(sc.Bytes(), &r) == nil && r.SessionID != "" {
			recs = append(recs, r)
		}
	}
	f.Close()
	if err := sc.Err(); err != nil {
		return recs, err
	}
	return recs, os.Truncate(spoolPath(), 0)
}

// rotateIfHuge drops the oldest half when the spool exceeds the size cap —
// bounded disk use beats unbounded growth during a long server outage.
func rotateIfHuge(path string) {
	fi, err := os.Stat(path)
	if err != nil || fi.Size() < spoolMaxBytes {
		return
	}
	b, err := os.ReadFile(path)
	if err != nil {
		return
	}
	half := b[len(b)/2:]
	// Cut at the first newline so we keep whole lines only.
	for i, c := range half {
		if c == '\n' {
			half = half[i+1:]
			break
		}
	}
	_ = os.WriteFile(path, half, 0o600)
}
