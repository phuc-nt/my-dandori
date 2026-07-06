package web

import (
	"strings"
	"testing"
)

func TestRiskPageEmptyStates(t *testing.T) {
	s := testServer(t)
	rec := get(t, s, "/risk")
	if rec.Code != 200 {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	body := rec.Body.String()
	// A clean install shows the positive empty states, not a blank page.
	for _, want := range []string{"Không có gì chờ duyệt", "Không có agent nào ở hạng D/F", "Không có cảnh báo nào đang mở"} {
		if !strings.Contains(body, want) {
			t.Errorf("missing empty-state %q", want)
		}
	}
}

func TestRiskPageListsOpenFlagsWithAge(t *testing.T) {
	s := testServer(t)
	s.Store.DB.Exec(`INSERT INTO flags(run_id, reason, status, created_at)
		VALUES(NULL, 'low grade F: agent x', 'open', datetime('now','-5 days'))`)
	rec := get(t, s, "/risk")
	body := rec.Body.String()
	if !strings.Contains(body, "low grade F: agent x") {
		t.Error("open flag not shown on risk page")
	}
	if !strings.Contains(body, "5 ngày") {
		t.Errorf("flag age not rendered:\n%s", body)
	}
}

func TestRiskFragmentServes(t *testing.T) {
	s := testServer(t)
	rec := get(t, s, "/risk/fragment")
	if rec.Code != 200 {
		t.Fatalf("fragment status = %d", rec.Code)
	}
}
