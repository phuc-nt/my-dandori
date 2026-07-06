// UC9 calendar-event request handling — split out of handlers_writeactions.go
// to keep each file under the project's 200-line cap.
package web

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/phuc-nt/dandori/internal/observer"
)

// maxCalendarAttendees caps invite-spam risk (M5) — a review calendar event
// has no legitimate reason to invite an unbounded list.
const maxCalendarAttendees = 20

// handleCalendarRequest proposes a review calendar event (UC9). Validates
// every attendee is an email and caps the count (M5); generates an
// idempotency key from run+title+start so a transient apply retry cannot
// double-book (H3).
func (s *Server) handleCalendarRequest(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	title := strings.TrimSpace(r.FormValue("title"))
	tz := strings.TrimSpace(r.FormValue("tz"))
	if tz == "" {
		tz = "UTC"
	}
	loc, err := time.LoadLocation(tz)
	if err != nil {
		http.Error(w, fmt.Sprintf("invalid timezone %q: %v", tz, err), http.StatusBadRequest)
		return
	}
	// <input type="datetime-local"> submits "2006-01-02T15:04" with no offset
	// — engine.go's apply-time re-validation requires RFC3339, so the offset
	// for the chosen IANA zone is attached here, once, at request time.
	start, err := parseLocalDateTime(r.FormValue("start"), loc)
	if err != nil {
		http.Error(w, "invalid start: "+err.Error(), http.StatusBadRequest)
		return
	}
	end, err := parseLocalDateTime(r.FormValue("end"), loc)
	if err != nil {
		http.Error(w, "invalid end: "+err.Error(), http.StatusBadRequest)
		return
	}
	if title == "" || start == "" || end == "" {
		http.Error(w, "title, start, and end are required", http.StatusBadRequest)
		return
	}
	var attendees []string
	for _, raw := range strings.Split(r.FormValue("attendees"), ",") {
		email := strings.TrimSpace(raw)
		if email == "" {
			continue
		}
		if !observer.ValidateEmail(email) {
			http.Error(w, fmt.Sprintf("invalid attendee email: %q", email), http.StatusBadRequest)
			return
		}
		attendees = append(attendees, email)
	}
	if len(attendees) > maxCalendarAttendees {
		http.Error(w, fmt.Sprintf("too many attendees (max %d)", maxCalendarAttendees), http.StatusBadRequest)
		return
	}
	idemKey := calendarIdemKey(id, title, start)
	summary := fmt.Sprintf("Tạo lịch review %q (chờ duyệt).", title)
	params := map[string]any{
		"title": title, "start": start, "end": end, "tz": tz,
		"attendees": attendees, "send_updates": "none", "idem_key": idemKey,
	}
	if _, err := observer.RequestAction(s.Store, "calendar-event", "run:"+id, summary, params, s.actor(r), "operator"); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	redirectBack(w, r, "/runs/"+id)
}

// parseLocalDateTime converts an <input type="datetime-local"> value
// ("2006-01-02T15:04", no offset) into RFC3339 using the given location, so
// the pinned evidence always carries an explicit, unambiguous offset.
func parseLocalDateTime(v string, loc *time.Location) (string, error) {
	v = strings.TrimSpace(v)
	if v == "" {
		return "", fmt.Errorf("empty")
	}
	t, err := time.ParseInLocation("2006-01-02T15:04", v, loc)
	if err != nil {
		return "", err
	}
	return t.Format(time.RFC3339), nil
}

// calendarIdemKey derives a stable request-time idempotency key so a
// transient apply retry with the same evidence never double-books (H3).
func calendarIdemKey(runID, title, start string) string {
	sum := sha256.Sum256([]byte(runID + "|" + title + "|" + start))
	return "cal_" + hex.EncodeToString(sum[:])[:16]
}
