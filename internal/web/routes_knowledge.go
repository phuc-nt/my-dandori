package web

// registerKnowledgeRoutes wires the /knowledge queue+detail routes. Called
// once from routes() (server.go) alongside the other registerPhaseNN
// helpers. Named without a phase number (F14): this surface outlives any one
// implementation round, so its route-registration function name should read
// as a feature, not a sprint label.
//
// Auth split (F9): nominate is any authenticated operator — viewer role is
// sufficient, since proposing a candidate has no external side effect
// (NominateUnit is an internal-only write). Every decide route (submit to
// review, publish-request, reject) is gated admin-only via requireAdmin —
// the same admin boundary already enforced on /reviews decide and /contexts
// company writes.
func (s *Server) registerKnowledgeRoutes() {
	s.mux.Get("/knowledge", s.handleKnowledgeQueue)
	s.mux.Get("/knowledge/unit/{id}", s.handleKnowledgeUnit)
	s.mux.Post("/knowledge/nominate", s.handleKnowledgeNominate) // viewer-ok (F9)

	// Mining Queue (v13 P1): read-only tab (viewer-ok) + one reading-list-only
	// dismiss write (M2 — no governance-suppression power, never audited).
	s.mux.Get("/knowledge/mining", s.handleKnowledgeMining)
	s.mux.Post("/knowledge/mining/{runID}/dismiss", s.handleKnowledgeMiningDismiss)

	// LLM-Draft Assistant (v13 P3): member+ (viewer blocked — spends
	// OpenRouter tokens on every call, unlike every other viewer-ok route
	// on this page).
	s.mux.Post("/knowledge/draft", s.requireNotViewer(s.handleKnowledgeDraft))

	// P4 suggest surface: read-only fragment (any authenticated operator) +
	// viewer-ok adopt-intent click (mirrors playbook-adopt's own viewer-ok
	// write — recording "I intend to use this" carries no external side
	// effect, same rationale as handlePlaybookAdopt).
	s.mux.Get("/agents/{agent}/knowledge-suggest", s.handleKnowledgeSuggestForAgent)
	s.mux.Post("/knowledge/unit/{id}/adopt-skill", s.handleKnowledgeAdoptSkill)

	s.mux.With(s.requireAdmin).Post("/knowledge/unit/{id}/submit", s.handleKnowledgeSubmit)
	s.mux.With(s.requireAdmin).Post("/knowledge/unit/{id}/reject", s.handleKnowledgeReject)
	s.mux.With(s.requireAdmin).Post("/knowledge/unit/{id}/publish-request", s.handleKnowledgePublishRequest)
	// F13: mandate/retire are the reachable-from-UI points where an admin
	// decides compliance-visibility on / off for a published unit — both
	// gated the same way publish-request is (RequestAction → /reviews).
	s.mux.With(s.requireAdmin).Post("/knowledge/unit/{id}/mandate-request", s.handleKnowledgeMandate)
	s.mux.With(s.requireAdmin).Post("/knowledge/unit/{id}/retire-request", s.handleKnowledgeRetire)
}
