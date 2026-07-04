package gws

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"mime"
	"strings"
	"time"
)

// stripCRLF removes carriage returns and newlines from an address/header
// value so it cannot inject additional headers (RFC 2822 header injection).
// Subject is Q-encoded separately; From/To are not, so they need this.
func stripCRLF(s string) string {
	return strings.NewReplacer("\r", "", "\n", "").Replace(s)
}

// BuildRFC2822 assembles a minimal plain-text RFC 2822 message with CRLF
// line endings and a UTF-8 word-encoded subject (safe for non-ASCII). From
// and To are stripped of CR/LF to block header injection.
func BuildRFC2822(from, to, subject, body string) string {
	encodedSubject := mime.QEncoding.Encode("UTF-8", subject)
	headers := "" +
		"From: " + stripCRLF(from) + "\r\n" +
		"To: " + stripCRLF(to) + "\r\n" +
		"Subject: " + encodedSubject + "\r\n" +
		"Date: " + time.Now().Format(time.RFC1123Z) + "\r\n" +
		"Content-Type: text/plain; charset=\"UTF-8\"\r\n" +
		"\r\n"
	return headers + body
}

type gmailSendParams struct {
	UserID string `json:"userId"`
}

type gmailSendBody struct {
	Raw string `json:"raw"`
}

// GmailSendRaw builds an RFC 2822 message and sends it via gws. The raw
// field MUST be base64url encoded WITHOUT padding (base64.RawURLEncoding) —
// padded output breaks Gmail's raw-message parsing.
func (r *Runner) GmailSendRaw(ctx context.Context, from, to, subject, body string) error {
	detail := to + ":" + subject
	if !r.Guard.Allow("gws.gmail_send", detail) {
		return nil
	}
	msg := BuildRFC2822(from, to, subject, body)
	raw := base64.RawURLEncoding.EncodeToString([]byte(msg))

	params, err := json.Marshal(gmailSendParams{UserID: "me"})
	if err != nil {
		return err
	}
	payload, err := json.Marshal(gmailSendBody{Raw: raw})
	if err != nil {
		return err
	}
	_, err = r.run(ctx, "gmail", "users", "messages", "send", "--params", string(params), "--json", string(payload))
	if err != nil {
		return fmt.Errorf("gws gmail send %s: %w", detail, err)
	}
	return nil
}
