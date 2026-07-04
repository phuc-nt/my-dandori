package web

import (
	"net/http"
	"net/url"
	"strings"
	"testing"
)

func TestGateThresholdsGetShowsDefaults(t *testing.T) {
	s := testServer(t)
	s.registerPhase05Routes()
	rec := get(t, s, "/gate-thresholds")
	if rec.Code != http.StatusOK {
		t.Fatalf("GET /gate-thresholds → %d", rec.Code)
	}
	body := rec.Body.String()
	if !strings.Contains(body, `value="C"`) || !strings.Contains(body, `value="80"`) {
		t.Errorf("defaults not rendered: %s", body)
	}
}

func TestGateThresholdsSetPersistsAndAudits(t *testing.T) {
	s := testServer(t)
	s.registerPhase05Routes()
	rec := postForm(t, s, "/gate-thresholds", url.Values{"gate_min_grade": {"B"}, "gate_min_pass_pct": {"90"}})
	if rec.Code != http.StatusSeeOther {
		t.Fatalf("POST /gate-thresholds → %d body=%s", rec.Code, rec.Body)
	}
	if got := s.Store.Setting("gate_min_grade"); got != "B" {
		t.Errorf("gate_min_grade = %q, want B", got)
	}
	if got := s.Store.Setting("gate_min_pass_pct"); got != "90" {
		t.Errorf("gate_min_pass_pct = %q, want 90", got)
	}
	var audits int
	s.Store.DB.QueryRow(`SELECT count(*) FROM audit_log WHERE action='gate_thresholds_set'`).Scan(&audits)
	if audits != 1 {
		t.Errorf("gate_thresholds_set audit rows = %d, want 1", audits)
	}
}

func TestGateThresholdsSetRejectsInvalidGrade(t *testing.T) {
	s := testServer(t)
	s.registerPhase05Routes()
	rec := postForm(t, s, "/gate-thresholds", url.Values{"gate_min_grade": {"Z"}, "gate_min_pass_pct": {"80"}})
	if rec.Code != http.StatusBadRequest {
		t.Errorf("invalid grade → %d, want 400", rec.Code)
	}
	if s.Store.Setting("gate_min_grade") != "" {
		t.Error("invalid grade must not be persisted")
	}
}

func TestGateThresholdsSetRejectsInvalidPassPct(t *testing.T) {
	s := testServer(t)
	s.registerPhase05Routes()
	for _, pct := range []string{"-1", "101", "abc"} {
		rec := postForm(t, s, "/gate-thresholds", url.Values{"gate_min_grade": {"C"}, "gate_min_pass_pct": {pct}})
		if rec.Code != http.StatusBadRequest {
			t.Errorf("pct=%q → %d, want 400", pct, rec.Code)
		}
	}
	if s.Store.Setting("gate_min_pass_pct") != "" {
		t.Error("invalid pct must not be persisted")
	}
}
