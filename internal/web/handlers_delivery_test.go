package web

import (
	"net/url"
	"path/filepath"
	"strings"
	"testing"

	"github.com/phuc-nt/dandori/internal/store/testseed"
)

// fakeGwsBinPath resolves the shared offline fake-gws fixture so delivery
// tests never touch the network.
func fakeGwsBinPath(t *testing.T) string {
	t.Helper()
	bin, err := filepath.Abs("../integrations/testdata/fake-gws")
	if err != nil {
		t.Fatal(err)
	}
	return bin
}

// deliveryTestServer builds a test server with phase-03 routes wired (server.go
// doesn't call registerPhase03Routes itself — that wiring happens once both
// concurrent phases land) and points gws at the offline fake binary.
func deliveryTestServer(t *testing.T) *Server {
	t.Helper()
	bin := fakeGwsBinPath(t)
	t.Setenv("DANDORI_GWS_BIN", bin)
	s := testServer(t)
	s.registerPhase03Routes()
	testseed.Agent(t, s.Store, "a1")
	testseed.Run(t, s.Store, "r1", "a1", "done", 1, 2.0)
	return s
}

func TestHandleExportSheetsDryRunByDefault(t *testing.T) {
	s := deliveryTestServer(t) // config.Load default DryRun=true
	rec := postForm(t, s, "/dash/export-sheets", url.Values{})
	if rec.Code != 200 {
		t.Fatalf("status: %d body: %s", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), "dry-run") {
		t.Errorf("expected dry-run in fragment, got: %s", rec.Body.String())
	}
}

func TestHandleExportSheetsIgnoresRequestDestination(t *testing.T) {
	s := deliveryTestServer(t)
	s.Cfg.DryRun = false
	s.Cfg.ExportSpreadsheetID = "config-pinned-id"
	// A request body carrying a destination-like field must have no effect —
	// the handler doesn't read any such field at all.
	rec := postForm(t, s, "/dash/export-sheets", url.Values{"spreadsheet_id": {"attacker-sheet"}})
	if rec.Code != 200 {
		t.Fatalf("status: %d body: %s", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), "config-pinned-id") {
		t.Errorf("export must use the config-pinned id, got: %s", rec.Body.String())
	}
	if strings.Contains(rec.Body.String(), "attacker-sheet") {
		t.Errorf("request-supplied destination must never appear in the result: %s", rec.Body.String())
	}
}

func TestHandleSendDigestNoRecipientsConfigured(t *testing.T) {
	s := deliveryTestServer(t)
	s.Cfg.DryRun = false // recipients gate fires before the guard would
	rec := postForm(t, s, "/dash/send-digest", url.Values{})
	if rec.Code != 200 {
		t.Fatalf("status: %d body: %s", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), "no recipients configured") {
		t.Errorf("expected no-recipients message, got: %s", rec.Body.String())
	}
}

func TestHandleSendDigestIgnoresRequestDestination(t *testing.T) {
	s := deliveryTestServer(t)
	s.Cfg.DigestRecipients = []string{"ceo@example.com"}
	// Guard stays default (DryRun=true from config.Load) so no live send is
	// attempted; the point is the handler has no field to read a request
	// destination from in the first place.
	rec := postForm(t, s, "/dash/send-digest", url.Values{"to": {"attacker@evil.com"}})
	if rec.Code != 200 {
		t.Fatalf("status: %d body: %s", rec.Code, rec.Body.String())
	}
	if strings.Contains(rec.Body.String(), "attacker@evil.com") {
		t.Errorf("request-supplied destination must never surface: %s", rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), "dry-run") {
		t.Errorf("expected dry-run legs, got: %s", rec.Body.String())
	}
}
