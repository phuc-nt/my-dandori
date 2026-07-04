package gws

import (
	"context"
	"encoding/base64"
	"strings"
	"testing"
)

func TestBuildRFC2822Headers(t *testing.T) {
	msg := BuildRFC2822("alerts@org.com", "phuc@org.com", "Daily Digest", "Fleet summary...")
	if !strings.Contains(msg, "From: alerts@org.com\r\n") {
		t.Errorf("missing From header (CRLF): %q", msg)
	}
	if !strings.Contains(msg, "To: phuc@org.com\r\n") {
		t.Errorf("missing To header (CRLF): %q", msg)
	}
	if !strings.Contains(msg, "\r\n\r\nFleet summary...") {
		t.Errorf("missing CRLF-separated body: %q", msg)
	}
	if !strings.Contains(msg, `charset="UTF-8"`) {
		t.Errorf("missing UTF-8 content-type: %q", msg)
	}
}

func TestBuildRFC2822StripsHeaderInjection(t *testing.T) {
	// A CR/LF in From or To must not start a new header line. The injected
	// text may survive inline, but never after a CRLF (which is what makes
	// it a header). Assert no CRLF precedes the injected tokens.
	msg := BuildRFC2822("a@org.com\r\nBcc: evil@x.com", "b@org.com\nX-Injected: 1", "Subj", "body")
	if strings.Contains(msg, "\r\nBcc:") {
		t.Errorf("From CRLF injection created a real Bcc header: %q", msg)
	}
	if strings.Contains(msg, "\r\nX-Injected:") {
		t.Errorf("To newline injection created a real header: %q", msg)
	}
	// The header block must still be exactly the 5 intended header lines
	// (4 internal CRLF separators before the trailing CRLF that ends the
	// block); injection would push this higher.
	if got := strings.Count(msg[:strings.Index(msg, "\r\n\r\n")], "\r\n"); got != 4 {
		t.Errorf("expected 5 header lines (4 CRLF), got %d separators: %q", got, msg)
	}
}

func TestGmailSendRawGuardSkip(t *testing.T) {
	g := &fakeGate{allowed: false}
	r, argvOut := newTestRunner(t, g)
	err := r.GmailSendRaw(context.Background(), "a@org.com", "b@org.com", "Subject", "body")
	if err != nil {
		t.Fatal(err)
	}
	if lines := readArgvLines(t, argvOut); len(lines) != 0 {
		t.Errorf("guard=false must not exec: %v", lines)
	}
}

func TestGmailSendRawUsesRawURLEncodingNoPadding(t *testing.T) {
	g := &fakeGate{allowed: true}
	r, argvOut := newTestRunner(t, g)
	err := r.GmailSendRaw(context.Background(), "a@org.com", "b@org.com", "Subject", "body")
	if err != nil {
		t.Fatal(err)
	}
	lines := readArgvLines(t, argvOut)
	if len(lines) != 1 {
		t.Fatalf("argv: %v", lines)
	}
	var body gmailSendBody
	unmarshalFlagJSON(t, lines[0], "--json", &body)
	if body.Raw == "" {
		t.Fatal("raw must not be empty")
	}
	if strings.Contains(body.Raw, "=") {
		t.Errorf("raw must have NO padding (RawURLEncoding), got: %s", body.Raw)
	}
	decoded, err := base64.RawURLEncoding.DecodeString(body.Raw)
	if err != nil {
		t.Fatalf("raw is not valid RawURLEncoding: %v", err)
	}
	if !strings.Contains(string(decoded), "From: a@org.com\r\n") {
		t.Errorf("decoded message missing From header: %q", decoded)
	}
}

func TestGmailSendRawUserIDIsMe(t *testing.T) {
	g := &fakeGate{allowed: true}
	r, argvOut := newTestRunner(t, g)
	if err := r.GmailSendRaw(context.Background(), "a@org.com", "b@org.com", "S", "b"); err != nil {
		t.Fatal(err)
	}
	lines := readArgvLines(t, argvOut)
	var params gmailSendParams
	unmarshalFlagJSON(t, lines[0], "--params", &params)
	if params.UserID != "me" {
		t.Errorf("userId: %s", params.UserID)
	}
}
