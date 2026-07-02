package web

import (
	"html/template"
	"net/http"
	"strings"

	"github.com/phuc-nt/dandori/internal/chat"
)

// chatPrincipal is the single-principal MVP identity (see plan Trust model).
func (s *Server) chatPrincipal() string { return s.Cfg.UserName + "@console" }

// handleChatPage renders the assistant with today's history. When the model
// cannot call tools the page shows a Vietnamese notice instead of a silently
// hallucinating chatbot.
func (s *Server) handleChatPage(w http.ResponseWriter, r *http.Request) {
	c := chat.NewClient(s.Cfg, s.Store, s.chatPrincipal())
	var notice string
	if err := c.SupportsTools(); err != nil {
		notice = "Trợ lý tạm không khả dụng: " + err.Error()
	}
	sessionID, _ := chat.Session(s.Store, s.chatPrincipal())
	history, _ := chat.History(s.Store, sessionID, 50)
	s.render(w, r, "chat", map[string]any{
		"Page": "chat", "History": history, "Notice": notice,
		"Mode":   modeFrom(r), // chat is CEO-facing: keep exec nav in exec mode
		"KillOn": s.Store.Setting("kill_switch_global") == "1",
	})
}

// handleChatMessage runs one full tool-loop turn and returns the exchange
// as a single fragment (user bubble + assistant bubble).
func (s *Server) handleChatMessage(w http.ResponseWriter, r *http.Request) {
	text := strings.TrimSpace(r.FormValue("message"))
	if text == "" || len(text) > 4000 {
		http.Error(w, "empty or oversized message", http.StatusBadRequest)
		return
	}
	c := chat.NewClient(s.Cfg, s.Store, s.chatPrincipal())
	answer, err := c.Ask(text)
	if err != nil {
		answer = "Có lỗi khi gọi trợ lý: " + err.Error()
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	frag := `<div class="flex justify-end"><div class="bg-blue-600 text-white rounded-2xl rounded-br-sm px-4 py-2 my-1 max-w-[80%] whitespace-pre-wrap">` +
		template.HTMLEscapeString(text) + `</div></div>` +
		`<div class="flex"><div class="bg-gray-100 rounded-2xl rounded-bl-sm px-4 py-2 my-1 max-w-[80%] whitespace-pre-wrap">` +
		template.HTMLEscapeString(answer) + `</div></div>`
	w.Write([]byte(frag))
}
