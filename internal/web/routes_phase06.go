package web

// registerPhase06Routes wires UG3 saved views and the UG5 live fleet
// wallboard. Deliberately NOT called from routes() — server.go's route table
// is owned by a concurrent phase this round; the caller of Server.New /
// routes() wiring is responsible for invoking this once after this phase
// lands (e.g. `s.registerPhase06Routes()` right after `s.routes()` in
// server.go, or from routes() itself once merged).
func (s *Server) registerPhase06Routes() {
	s.mux.Post("/views/save", s.handleSavedViewSave)
	s.mux.Get("/views/{id}/apply", s.handleSavedViewApply)
	s.mux.Post("/views/{id}/delete", s.handleSavedViewDelete)

	s.mux.Get("/wallboard", s.handleWallboard)
	s.mux.Get("/wallboard/fragment", s.handleWallboardFragment)
}
