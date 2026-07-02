package web

import (
	"fmt"
	"html/template"
	"net/http"

	"github.com/go-chi/chi/v5"

	"github.com/phuc-nt/dandori/internal/learn"
)

// handleAgentAIReview lazy-loads the AI-generated performance blurb (L1's
// "vài câu nhận xét tiếng người"). Empty result (no key / no data / upstream
// error) renders a quiet placeholder — never a broken page.
func (s *Server) handleAgentAIReview(w http.ResponseWriter, r *http.Request) {
	agent := chi.URLParam(r, "agent")
	reviewer := learn.NewAIReviewer(s.Store, s.Cfg.OpenRouterKey, s.Cfg.OpenRouterModel)
	text := reviewer.Review(agent, s.Cfg.LearnWindowDays)
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	if text == "" {
		fmt.Fprint(w, `<span class="text-gray-400 text-sm">No AI review available (needs OPENROUTER_API_KEY and ≥1 run).</span>`)
		return
	}
	fmt.Fprintf(w, `<p class="text-sm">%s</p>
<p class="text-xs text-gray-400 mt-1">AI-generated weekly review — every number is verifiable in <a class="underline" href="/provenance?agent=%s&metric=acceptance">provenance</a>.</p>`,
		template.HTMLEscapeString(text), template.URLQueryEscaper(agent))
}
