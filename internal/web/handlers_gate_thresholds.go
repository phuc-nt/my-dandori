package web

import (
	"net/http"
	"regexp"
	"strconv"

	"github.com/phuc-nt/dandori/internal/govern"
)

// UE3 defaults, mirrored from internal/observer's defaultGateMinGrade /
// pass-pct fallback so the form shows the same value the detector uses when
// the operator has never set these settings.
const (
	defaultGateMinGrade   = "C"
	defaultGateMinPassPct = "80"
)

var gateMinGradeRe = regexp.MustCompile(`^[A-F]$`)

// handleGateThresholds renders the current global quality-gate thresholds
// (UE3): minimum playbook-candidate grade and minimum check pass percentage.
func (s *Server) handleGateThresholds(w http.ResponseWriter, r *http.Request) {
	data := map[string]any{
		"Page":           "gate_thresholds",
		"GateMinGrade":   settingOrDefault(s, "gate_min_grade", defaultGateMinGrade),
		"GateMinPassPct": settingOrDefault(s, "gate_min_pass_pct", defaultGateMinPassPct),
	}
	if isHTMX(r) {
		s.renderFragment(w, r, "gate_thresholds", "gate_thresholds_form", data)
		return
	}
	s.render(w, r, "gate_thresholds", data)
}

// handleGateThresholdsSet validates and persists both threshold settings in
// one submit; a rejected value leaves both settings untouched (all-or-nothing
// so the form never partially applies).
func (s *Server) handleGateThresholdsSet(w http.ResponseWriter, r *http.Request) {
	grade := r.FormValue("gate_min_grade")
	pctRaw := r.FormValue("gate_min_pass_pct")
	if !gateMinGradeRe.MatchString(grade) {
		http.Error(w, "invalid grade — must be one of A-F", http.StatusBadRequest)
		return
	}
	pct, err := strconv.Atoi(pctRaw)
	if err != nil || pct < 0 || pct > 100 {
		http.Error(w, "invalid pass percentage — must be an integer 0-100", http.StatusBadRequest)
		return
	}
	if err := s.Store.SetSetting("gate_min_grade", grade); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	if err := s.Store.SetSetting("gate_min_pass_pct", pctRaw); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	a := &govern.Audit{St: s.Store, Actor: s.actor(r)}
	_, _ = a.Append("gate_thresholds_set", "global", "grade="+grade+" pass_pct="+pctRaw)
	redirectBack(w, r, "/gate-thresholds")
}

// settingOrDefault reads a settings key, falling back when unset — the same
// "fallback to defaults" contract minGrade() uses on the observer side.
func settingOrDefault(s *Server, key, def string) string {
	if v := s.Store.Setting(key); v != "" {
		return v
	}
	return def
}
