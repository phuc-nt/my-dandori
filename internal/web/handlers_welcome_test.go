package web

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/phuc-nt/dandori/internal/config"
	"github.com/phuc-nt/dandori/internal/store"
)

func TestWizardStepsDeriveFromHealth(t *testing.T) {
	s := testServer(t)
	s.Cfg.Integrations = config.Integrations{}
	s.Cfg.OpenRouterKey = ""

	// Nothing done yet.
	if v := s.wizard(); v.HookProject.Done || v.Connect.Done || v.FirstRun.Done || v.AllDone {
		t.Fatalf("fresh install should have no steps done: %+v", v)
	}

	// Hook a project → step 1.
	s.Store.SetSetting("hooked:/p", store.Now())
	if v := s.wizard(); !v.HookProject.Done {
		t.Error("step 1 not done after hooking a project")
	}

	// Connect an integration (configured + ok cached test) → step 2.
	s.Cfg.OpenRouterKey = "sk-x"
	cache, _ := json.Marshal(TestInfo{OK: true, At: store.Now()})
	s.Store.SetSetting("inttest:openrouter", string(cache))
	if v := s.wizard(); !v.Connect.Done {
		t.Error("step 2 not done after connecting an integration")
	}

	// Record a run → step 3 + all done.
	s.Store.DB.Exec(`INSERT INTO runs(id, started_at) VALUES('r1',?)`, store.Now())
	v := s.wizard()
	if !v.FirstRun.Done || !v.AllDone {
		t.Errorf("step 3/all-done wrong: %+v", v)
	}
}

func TestWelcomeFragmentStopsPollingWhenDone(t *testing.T) {
	s := testServer(t)
	// Make all three steps done.
	s.Store.SetSetting("hooked:/p", store.Now())
	s.Cfg.OpenRouterKey = "sk-x"
	cache, _ := json.Marshal(TestInfo{OK: true, At: store.Now()})
	s.Store.SetSetting("inttest:openrouter", string(cache))
	s.Store.DB.Exec(`INSERT INTO runs(id, started_at) VALUES('r1',?)`, store.Now())

	rec := get(t, s, "/welcome/fragment")
	if rec.Code != 286 {
		t.Errorf("fragment code = %d, want 286 (stop polling when done)", rec.Code)
	}
}

func TestExecHomeShowsWizardBannerUntilFirstRun(t *testing.T) {
	s := testServer(t)
	rec := get(t, s, "/")
	if !strings.Contains(rec.Body.String(), "Bắt đầu thiết lập") {
		t.Error("wizard banner missing on fresh install")
	}
	s.Store.DB.Exec(`INSERT INTO runs(id, started_at) VALUES('r1',?)`, store.Now())
	rec = get(t, s, "/")
	if strings.Contains(rec.Body.String(), "Bắt đầu thiết lập") {
		t.Error("wizard banner still shown after a run was captured")
	}
}
