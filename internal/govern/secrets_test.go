package govern

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
)

// toolCallWithContent builds a ToolCall through ExtractToolCall for Write,
// Edit, and NotebookEdit — the three content-bearing tools G1.5 must scan.
func toolCallWithContent(t testing.TB, runID, toolName, contentField, value string) ToolCall {
	t.Helper()
	input, err := json.Marshal(map[string]string{contentField: value, "file_path": "/work/proj/f.go"})
	if err != nil {
		t.Fatal(err)
	}
	return ExtractToolCall(runID, "a1", "proj", "/work/proj", toolName, input)
}

// TestSecretDenyAcrossToolSurfaces proves a strict-secret pattern denies
// whether it arrives in a Bash command, a Write's content, an Edit's
// new_string, or a NotebookEdit's new_source — and that the audit/deny
// message never echoes the raw secret value.
func TestSecretDenyAcrossToolSurfaces(t *testing.T) {
	e := testEngine(t)
	seedRun(t, e, "s1", 0)
	secret := "sk-liveTOKEN1234567890ABCDEFGH"

	cases := []struct {
		name string
		tc   ToolCall
	}{
		{"Bash command", bashCall("s1", "curl -H 'Authorization: Bearer "+secret+"'")},
		{"Write content", toolCallWithContent(t, "s1", "Write", "content", "API_KEY="+secret)},
		{"Edit new_string", toolCallWithContent(t, "s1", "Edit", "new_string", "const key = \""+secret+"\"")},
		{"NotebookEdit new_source", toolCallWithContent(t, "s1", "NotebookEdit", "new_source", "os.environ['X']='"+secret+"'")},
		{"AWS key", bashCall("s1", "export AWS_KEY=AKIAABCDEFGHIJKLMNOP")},
		{"private key", toolCallWithContent(t, "s1", "Write", "content", "-----BEGIN RSA PRIVATE KEY-----\nMII...")},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			d := e.Evaluate(context.Background(), c.tc)
			if d.Verdict != Deny {
				t.Fatalf("%s: verdict %s, want Deny", c.name, d.Verdict)
			}
			if strings.Contains(d.Reason, secret) || strings.Contains(d.Reason, "AKIAABCDEFGHIJKLMNOP") {
				t.Errorf("%s: deny reason echoes raw secret: %s", c.name, d.Reason)
			}
			var auditDetail string
			e.St.DB.QueryRow(`SELECT detail FROM audit_log ORDER BY id DESC LIMIT 1`).Scan(&auditDetail)
			if strings.Contains(auditDetail, secret) || strings.Contains(auditDetail, "AKIAABCDEFGHIJKLMNOP") {
				t.Errorf("%s: audit log echoes raw secret: %s", c.name, auditDetail)
			}
		})
	}
}

// TestSecretDenyFalsePositives proves the sanctioned env-var-ref Bearer
// pattern and generic kv-password usage are NOT denied by the strict set —
// these are exactly the shapes that would otherwise break legitimate
// dev-flow commands (the G1/G2 false-positive lesson this phase learned from).
func TestSecretDenyFalsePositives(t *testing.T) {
	e := testEngine(t)
	seedRun(t, e, "s2", 0)
	cases := []string{
		"curl -H 'Authorization: Bearer $OPENROUTER_API_KEY' https://api.x.com",
		"curl -H 'Authorization: Bearer ${TOKEN}' https://api.x.com",
		`password = os.environ["X"]`,
		"echo sk-abc", // short sk-, below strict length
	}
	for _, cmd := range cases {
		d := e.Evaluate(context.Background(), bashCall("s2", cmd))
		if d.Verdict == Deny {
			t.Errorf("%q must NOT be denied, got Deny: %s", cmd, d.Reason)
		}
	}
}

// TestPIIGateCard proves a Luhn-valid card number gates on approval, and
// that the stored approval action+reason never contain the raw card number
// — only a masked/summarized form.
func TestPIIGateCard(t *testing.T) {
	e := testEngine(t)
	seedRun(t, e, "s3", 0)
	card := "4111111111111111" // Luhn-valid test card
	tc := toolCallWithContent(t, "s3", "Write", "content", "customer card: "+card)

	d := e.Evaluate(context.Background(), tc)
	if d.Verdict != Deny { // GateWaitSeconds=0 in tests → immediate timeout-deny
		t.Fatalf("PII gate should deny-on-timeout in test harness, got %s", d.Verdict)
	}
	var action, reason string
	err := e.St.DB.QueryRow(`SELECT action, reason FROM approvals WHERE status='pending' ORDER BY id DESC LIMIT 1`).
		Scan(&action, &reason)
	if err != nil {
		t.Fatalf("approval row missing: %v", err)
	}
	if strings.Contains(action, card) || strings.Contains(reason, card) {
		t.Errorf("approval leaked raw card: action=%q reason=%q", action, reason)
	}
	if !strings.Contains(reason, "1 th") { // "1 thẻ"
		t.Errorf("reason should summarize card count: %q", reason)
	}
}

// TestPIIGateManyEmails proves ≥5 emails in one payload gates, while a
// single email (common: a support address in a comment) does not.
func TestPIIGateManyEmails(t *testing.T) {
	e := testEngine(t)
	seedRun(t, e, "s4", 0)

	oneEmail := toolCallWithContent(t, "s4", "Write", "content", "contact: support@example.com")
	if d := e.Evaluate(context.Background(), oneEmail); d.Verdict == Deny && strings.Contains(d.Reason, "G1.5") {
		t.Errorf("single email must not trigger PII gate, got: %s", d.Reason)
	}

	emails := strings.Join([]string{
		"a@example.com", "b@example.com", "c@example.com", "d@example.com", "e@example.com",
	}, ", ")
	manyEmails := toolCallWithContent(t, "s4", "Write", "content", "recipients: "+emails)
	d := e.Evaluate(context.Background(), manyEmails)
	if d.Verdict != Deny || !strings.Contains(d.Reason, "G1.5") {
		t.Fatalf("5 emails should trigger PII gate (deny-on-timeout), got %s: %s", d.Verdict, d.Reason)
	}
	var reason string
	e.St.DB.QueryRow(`SELECT reason FROM approvals WHERE status='pending' ORDER BY id DESC LIMIT 1`).Scan(&reason)
	if strings.Contains(reason, "a@example.com") {
		t.Errorf("approval reason leaked raw email: %q", reason)
	}
}

// TestPIIGateBypassesBand proves a trusted-band agent does NOT skip the PII
// gate the way it skips non-critical rule-based gates — PII risk does not
// shrink because the agent is trusted on cost/edit history.
func TestPIIGateBypassesBand(t *testing.T) {
	e := testEngine(t)
	seedRun(t, e, "s5", 0)
	if err := SetBand(e.St, "a1", BandTrusted, "tester", "trusted for test"); err != nil {
		t.Fatal(err)
	}
	card := "4111111111111111"
	tc := toolCallWithContent(t, "s5", "Write", "content", "card "+card)
	d := e.Evaluate(context.Background(), tc)
	if d.Verdict != Deny {
		t.Fatalf("trusted band must still be gated for PII, got %s", d.Verdict)
	}
}

// TestSecretsGuardDisabled proves the config flag fully disables the check
// (both secret-Deny and PII-gate halves).
func TestSecretsGuardDisabled(t *testing.T) {
	e := testEngine(t)
	seedRun(t, e, "s6", 0)
	disabled := false
	e.Cfg.SecretsGuardEnabled = &disabled

	secretCall := bashCall("s6", "export KEY=AKIAABCDEFGHIJKLMNOP")
	if d := e.Evaluate(context.Background(), secretCall); d.Verdict != Allow {
		t.Errorf("secret check disabled but still denied: %s", d.Reason)
	}
	piiCall := toolCallWithContent(t, "s6", "Write", "content", "card 4111111111111111")
	if d := e.Evaluate(context.Background(), piiCall); d.Verdict != Allow {
		t.Errorf("PII gate disabled but still gated: %s", d.Reason)
	}
}

// TestLuhnValid locks in the checksum helper against known-good/bad numbers.
func TestLuhnValid(t *testing.T) {
	cases := []struct {
		span string
		want bool
	}{
		{"4111111111111111", true},  // Visa test number
		{"4111111111111112", false}, // last digit corrupted
		{"1234567890123", false},
		{"", false},
		{"abc", false},
	}
	for _, c := range cases {
		if got := LuhnValid(c.span); got != c.want {
			t.Errorf("LuhnValid(%q) = %v, want %v", c.span, got, c.want)
		}
	}
}

// TestExtractToolCallContent proves ExtractToolCall populates Content for
// all three content-bearing tools and leaves it empty for others.
func TestExtractToolCallContent(t *testing.T) {
	cases := []struct {
		tool  string
		field string
	}{
		{"Write", "content"},
		{"Edit", "new_string"},
		{"NotebookEdit", "new_source"},
	}
	for _, c := range cases {
		input, _ := json.Marshal(map[string]string{c.field: "hello world"})
		tc := ExtractToolCall("r", "a", "p", "/cwd", c.tool, input)
		if tc.Content != "hello world" {
			t.Errorf("%s: Content = %q, want %q", c.tool, tc.Content, "hello world")
		}
	}
	input, _ := json.Marshal(map[string]string{"command": "ls"})
	tc := ExtractToolCall("r", "a", "p", "/cwd", "Bash", input)
	if tc.Content != "" {
		t.Errorf("Bash: Content should stay empty, got %q", tc.Content)
	}
}
