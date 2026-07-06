package web

import (
	"context"
	"net/http"

	"github.com/phuc-nt/dandori/internal/integrations"
	"github.com/phuc-nt/dandori/internal/integrations/gws"
	"github.com/phuc-nt/dandori/internal/integrations/slack"
	"github.com/phuc-nt/dandori/internal/learn"
)

// deliveryResult is the fragment view for both the Sheets export and the
// digest trigger. Guard = false shows as "dry-run"; a config gap ("no
// recipients configured") is surfaced as-is.
type deliveryResult struct {
	Page      string
	SheetsURL string
	SheetsRes string
	SlackRes  string
	GmailRes  string
	Err       string
}

// handleExportSheets triggers UC8 (POST /dash/export-sheets). The target is
// read from server config only — any destination in the request body is
// ignored, there is no field for it to occupy.
func (s *Server) handleExportSheets(w http.ResponseWriter, r *http.Request) {
	guard := &integrations.Guard{Cfg: s.Cfg, St: s.Store}
	exporter := &gws.SheetsExporter{
		Guard: guard, GWS: gws.NewRunner(guard), St: s.Store, Cfg: s.Cfg,
	}
	days := s.Cfg.LearnWindowDays
	url, res, err := exporter.Export(r.Context(), days)
	data := &deliveryResult{Page: "org", SheetsURL: url, SheetsRes: res}
	if err != nil {
		data.Err = err.Error()
	} else {
		s.audit(r, "sheets_export_triggered", "org", res)
	}
	s.renderFragment(w, r, "dash_org", "delivery_result", data)
}

// handleSendDigest triggers UG2b (POST /dash/send-digest). Recipients come
// ONLY from config; any destination field in the request body is ignored.
func (s *Server) handleSendDigest(w http.ResponseWriter, r *http.Request) {
	days := s.Cfg.LearnWindowDays
	digestData, err := learn.BuildDigestData(s.Store, days)
	if err != nil {
		s.renderFragment(w, r, "dash_org", "delivery_result", &deliveryResult{Page: "org", Err: err.Error()})
		return
	}
	guard := &integrations.Guard{Cfg: s.Cfg, St: s.Store}
	i := s.Cfg.Integrations
	pub := &integrations.DigestPublisher{
		St: s.Store, Guard: guard, Cfg: s.Cfg,
		Slack: slack.New(i.SlackXoxc, i.SlackXoxd),
		GWS:   gws.NewRunner(guard),
		From:  "me",
	}
	slackRes, gmailRes, err := pub.Send(context.Background(), digestData)
	data := &deliveryResult{Page: "org", SlackRes: slackRes, GmailRes: gmailRes}
	if err != nil {
		data.Err = err.Error()
	} else {
		s.audit(r, "digest_triggered", "org", "slack="+slackRes+" gmail="+gmailRes)
	}
	s.renderFragment(w, r, "dash_org", "delivery_result", data)
}
