package web

// registerPhase02Routes wires the approval-gated write actions: UC2 Jira
// transition, UC4 PR review, UC9 calendar event, and the UB4 per-check
// quality-gate override. Split from server.go's routes() into its own file
// (same convention as registerPhase05Routes) so two phases touching the
// route table in the same window don't collide on one file's diff.
func (s *Server) registerPhase02Routes() {
	s.mux.Get("/runs/{id}/transitions", s.handleRunTransitions)
	s.mux.Get("/runs/{id}/gate-results", s.handleRunGateResults)
	// admin: external write against Jira/GitHub/Calendar or a gate override.
	s.mux.With(s.requireAdmin).Post("/runs/{id}/transition-request", s.handleTransitionRequest)
	s.mux.With(s.requireAdmin).Post("/runs/{id}/pr-review-request", s.handlePRReviewRequest)
	s.mux.With(s.requireAdmin).Post("/runs/{id}/calendar-request", s.handleCalendarRequest)
	s.mux.With(s.requireAdmin).Post("/runs/{id}/override-gate", s.handleOverrideGate)
}
