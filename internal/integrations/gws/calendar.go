package gws

import (
	"context"
	"encoding/json"
	"fmt"
)

// CalendarEvent is the minimal event shape Dandori inserts. TimeZone
// defaults to "UTC" when empty so off-by-one-hour bugs across timezones
// don't slip in via an unset field.
type CalendarEvent struct {
	Summary     string             `json:"summary"`
	Description string             `json:"description,omitempty"`
	Start       CalendarDateTime   `json:"start"`
	End         CalendarDateTime   `json:"end"`
	Attendees   []CalendarAttendee `json:"attendees,omitempty"`
}

type CalendarDateTime struct {
	DateTime string `json:"dateTime"`
	TimeZone string `json:"timeZone"`
}

type CalendarAttendee struct {
	Email          string `json:"email"`
	DisplayName    string `json:"displayName,omitempty"`
	ResponseStatus string `json:"responseStatus,omitempty"`
}

type calendarInsertParams struct {
	CalendarID  string `json:"calendarId"`
	SendUpdates string `json:"sendUpdates"`
}

type calendarInsertResponse struct {
	ID       string `json:"id"`
	HTMLLink string `json:"htmlLink"`
}

// CalendarInsert creates an event on the primary calendar. sendUpdates
// defaults to "none" (internal review use case per research) when empty.
func (r *Runner) CalendarInsert(ctx context.Context, ev CalendarEvent, sendUpdates string) (eventID, htmlLink string, err error) {
	if sendUpdates == "" {
		sendUpdates = "none"
	}
	if ev.Start.TimeZone == "" {
		ev.Start.TimeZone = "UTC"
	}
	if ev.End.TimeZone == "" {
		ev.End.TimeZone = "UTC"
	}
	if !r.Guard.Allow("gws.calendar_insert", ev.Summary) {
		return "", "", nil
	}
	params, err := json.Marshal(calendarInsertParams{CalendarID: "primary", SendUpdates: sendUpdates})
	if err != nil {
		return "", "", err
	}
	body, err := json.Marshal(ev)
	if err != nil {
		return "", "", err
	}
	out, err := r.run(ctx, "calendar", "events", "insert", "--params", string(params), "--json", string(body))
	if err != nil {
		return "", "", fmt.Errorf("gws calendar insert %q: %w", ev.Summary, err)
	}
	var resp calendarInsertResponse
	if err := json.Unmarshal(out, &resp); err != nil {
		return "", "", fmt.Errorf("gws calendar insert: parse response: %w", err)
	}
	return resp.ID, resp.HTMLLink, nil
}
