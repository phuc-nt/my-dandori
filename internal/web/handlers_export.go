package web

import (
	"net/http"

	"github.com/phuc-nt/dandori/internal/govern"
)

// handleConfluenceReport publishes today's fleet report (UC3).
func (s *Server) handleConfluenceReport(w http.ResponseWriter, r *http.Request) {
	if s.ReportSink == nil {
		http.Error(w, "confluence not configured", http.StatusServiceUnavailable)
		return
	}
	pageID, err := s.ReportSink()
	if err != nil {
		http.Error(w, err.Error(), 500)
		return
	}
	s.audit("confluence_report", "org", pageID)
	redirectBack(w, r, "/dash/org")
}

// handleComplianceExport streams the compliance bundle for download.
func (s *Server) handleComplianceExport(w http.ResponseWriter, r *http.Request) {
	bundle, err := govern.BuildComplianceBundle(s.Store, s.Cfg.UserName)
	if err != nil {
		http.Error(w, err.Error(), 500)
		return
	}
	if r.URL.Query().Get("format") == "csv" {
		w.Header().Set("Content-Type", "text/csv")
		w.Header().Set("Content-Disposition", `attachment; filename="dandori-audit.csv"`)
		if err := bundle.WriteCSV(w); err != nil {
			http.Error(w, err.Error(), 500)
		}
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Content-Disposition", `attachment; filename="dandori-compliance.json"`)
	if err := bundle.WriteJSON(w); err != nil {
		http.Error(w, err.Error(), 500)
	}
}
