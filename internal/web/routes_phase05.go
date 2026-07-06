package web

// registerPhase05Routes wires the UE3 quality-gate threshold form and the L7
// agent-assignment suggest endpoint. Deliberately NOT called from routes() —
// server.go's route table is owned by a concurrent phase this round; the
// caller of Server.New / routes() wiring is responsible for invoking this
// once after both phases land (e.g. `s.registerPhase05Routes()` right after
// `s.routes()` in server.go, or from routes() itself once merged).
func (s *Server) registerPhase05Routes() {
	s.mux.Get("/gate-thresholds", s.handleGateThresholds)
	// admin (C4): GOVERN policy write.
	s.mux.With(s.requireAdmin).Post("/gate-thresholds", s.handleGateThresholdsSet)
	s.mux.Get("/assign/suggest", s.handleAssignmentSuggest)
}
