package gws

import (
	"context"
	"testing"
)

func TestCalendarInsertGuardSkip(t *testing.T) {
	g := &fakeGate{allowed: false}
	r, argvOut := newTestRunner(t, g)
	ev := CalendarEvent{Summary: "Review"}
	id, link, err := r.CalendarInsert(context.Background(), ev, "")
	if err != nil {
		t.Fatal(err)
	}
	if id != "" || link != "" {
		t.Errorf("guard=false must return empty, got id=%q link=%q", id, link)
	}
	if lines := readArgvLines(t, argvOut); len(lines) != 0 {
		t.Errorf("guard=false must not exec: %v", lines)
	}
}

func TestCalendarInsertDefaultsTimeZoneAndSendUpdates(t *testing.T) {
	g := &fakeGate{allowed: true}
	r, argvOut := newTestRunner(t, g)
	ev := CalendarEvent{
		Summary: "Review",
		Start:   CalendarDateTime{DateTime: "2026-07-10T10:00:00"},
		End:     CalendarDateTime{DateTime: "2026-07-10T11:00:00"},
	}
	id, link, err := r.CalendarInsert(context.Background(), ev, "")
	if err != nil {
		t.Fatal(err)
	}
	if id != "fake-event-id" || link == "" {
		t.Errorf("id/link: %s %s", id, link)
	}
	lines := readArgvLines(t, argvOut)
	if len(lines) != 1 {
		t.Fatalf("argv: %v", lines)
	}
	argv := lines[0]
	var params calendarInsertParams
	unmarshalFlagJSON(t, argv, "--params", &params)
	if params.SendUpdates != "none" {
		t.Errorf("default sendUpdates must be none: %s", params.SendUpdates)
	}
	var body CalendarEvent
	unmarshalFlagJSON(t, argv, "--json", &body)
	if body.Start.TimeZone != "UTC" || body.End.TimeZone != "UTC" {
		t.Errorf("default timeZone must be UTC: start=%s end=%s", body.Start.TimeZone, body.End.TimeZone)
	}
}

func TestCalendarInsertHostileTitleRoundTrips(t *testing.T) {
	g := &fakeGate{allowed: true}
	r, argvOut := newTestRunner(t, g)
	hostile := `Review "Q3" \ Sprint`
	ev := CalendarEvent{
		Summary: hostile,
		Start:   CalendarDateTime{DateTime: "2026-07-10T10:00:00", TimeZone: "UTC"},
		End:     CalendarDateTime{DateTime: "2026-07-10T11:00:00", TimeZone: "UTC"},
	}
	if _, _, err := r.CalendarInsert(context.Background(), ev, "all"); err != nil {
		t.Fatal(err)
	}
	lines := readArgvLines(t, argvOut)
	if len(lines) != 1 {
		t.Fatalf("argv: %v", lines)
	}
	var body CalendarEvent
	unmarshalFlagJSON(t, lines[0], "--json", &body)
	if body.Summary != hostile {
		t.Errorf("hostile summary broke JSON round-trip: got %q, want %q", body.Summary, hostile)
	}
}
