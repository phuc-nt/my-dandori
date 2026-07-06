package web

// registerPhase03Routes wires the UC8 Sheets export + UG2b digest trigger
// endpoints. Deliberately NOT called from routes() — server.go's route
// table is owned by a concurrent phase this round; the caller of
// Server.New / routes() wiring is responsible for invoking this once after
// both phases land (e.g. `s.registerPhase03Routes()` right after `s.routes()`
// in server.go, or from routes() itself once merged).
func (s *Server) registerPhase03Routes() {
	// admin (C4): both exfiltrate fleet data to an external service (Sheets/Slack).
	s.mux.With(s.requireAdmin).Post("/dash/export-sheets", s.handleExportSheets)
	s.mux.With(s.requireAdmin).Post("/dash/send-digest", s.handleSendDigest)
}
