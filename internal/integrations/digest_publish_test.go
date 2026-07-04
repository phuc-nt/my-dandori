package integrations

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"github.com/phuc-nt/dandori/internal/config"
	"github.com/phuc-nt/dandori/internal/learn"
	"github.com/phuc-nt/dandori/internal/store"
	"github.com/phuc-nt/dandori/internal/store/testseed"
)

type fakeGmail struct {
	sent []string // "to:subject" per call
}

func (f *fakeGmail) GmailSendRaw(ctx context.Context, from, to, subject, body string) error {
	f.sent = append(f.sent, to+":"+subject)
	return nil
}

func digestFixture(t *testing.T, dryRun bool, recipients []string) (*DigestPublisher, *fakeSlack, *fakeGmail, *store.Store, *config.Config) {
	t.Helper()
	st, err := store.Open(filepath.Join(t.TempDir(), "d.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	testseed.Agent(t, st, "a1")
	testseed.Run(t, st, "r1", "a1", "done", 1, 3.0)

	cfg := &config.Config{DryRun: dryRun, DigestRecipients: recipients}
	cfg.Integrations.SlackChannel = "C-digest"
	sl := &fakeSlack{}
	gm := &fakeGmail{}
	pub := &DigestPublisher{
		St: st, Guard: &Guard{Cfg: cfg, St: st}, Cfg: cfg,
		Slack: sl, GWS: gm, From: "me",
	}
	return pub, sl, gm, st, cfg
}

func TestDigestSendConfigOnlyRecipientsIgnoresAnyOtherDestination(t *testing.T) {
	pub, sl, gm, _, _ := digestFixture(t, false, []string{"ceo@example.com", "coo@example.com"})
	data, err := learn.BuildDigestData(pub.St, 30)
	if err != nil {
		t.Fatal(err)
	}
	// Simulate a request that tried to carry a destination — DigestPublisher
	// has no parameter for it, so there is no way to pass "attacker@evil.com"
	// into Send. We assert the ONLY address ever reaching Gmail is the
	// config-configured set.
	slackRes, gmailRes, err := pub.Send(context.Background(), data)
	if err != nil {
		t.Fatal(err)
	}
	if slackRes != "sent" || gmailRes != "sent" {
		t.Fatalf("legs: slack=%s gmail=%s", slackRes, gmailRes)
	}
	if len(sl.posts) != 1 {
		t.Fatalf("slack posts: %d", len(sl.posts))
	}
	if len(gm.sent) != 1 {
		t.Fatalf("gmail sends: %d", len(gm.sent))
	}
	if gm.sent[0] != "ceo@example.com,coo@example.com:Dandori fleet digest "+gmailDate() {
		t.Errorf("gmail destination not from config: %q", gm.sent[0])
	}
}

func TestDigestSendEmptyRecipientsNoOp(t *testing.T) {
	pub, sl, gm, _, _ := digestFixture(t, false, nil)
	data, err := learn.BuildDigestData(pub.St, 30)
	if err != nil {
		t.Fatal(err)
	}
	slackRes, gmailRes, err := pub.Send(context.Background(), data)
	if err != nil {
		t.Fatal(err)
	}
	if slackRes != "no recipients configured" || gmailRes != "no recipients configured" {
		t.Errorf("legs: slack=%s gmail=%s", slackRes, gmailRes)
	}
	if len(sl.posts) != 0 || len(gm.sent) != 0 {
		t.Error("no recipients must mean no send on either leg")
	}
}

func TestDigestSendDryRunNoSend(t *testing.T) {
	pub, sl, gm, st, _ := digestFixture(t, true, []string{"ceo@example.com"})
	data, err := learn.BuildDigestData(pub.St, 30)
	if err != nil {
		t.Fatal(err)
	}
	slackRes, gmailRes, err := pub.Send(context.Background(), data)
	if err != nil {
		t.Fatal(err)
	}
	if slackRes != "dry-run" || gmailRes != "dry-run" {
		t.Errorf("legs: slack=%s gmail=%s", slackRes, gmailRes)
	}
	if len(sl.posts) != 0 || len(gm.sent) != 0 {
		t.Error("dry-run must not touch clients")
	}
	var n int
	st.DB.QueryRow(`SELECT count(*) FROM notifications WHERE kind='digest'`).Scan(&n)
	if n != 0 {
		t.Errorf("dry-run must not record notifications, got %d", n)
	}
}

func TestDigestSendDedupSameDaySameRecipients(t *testing.T) {
	pub, sl, gm, _, _ := digestFixture(t, false, []string{"ceo@example.com"})
	data, err := learn.BuildDigestData(pub.St, 30)
	if err != nil {
		t.Fatal(err)
	}
	if s, g, err := pub.Send(context.Background(), data); err != nil || s != "sent" || g != "sent" {
		t.Fatalf("first send: slack=%s gmail=%s err=%v", s, g, err)
	}
	s2, g2, err := pub.Send(context.Background(), data)
	if err != nil {
		t.Fatal(err)
	}
	if s2 != "deduped" || g2 != "deduped" {
		t.Errorf("second same-day send: slack=%s gmail=%s, want deduped", s2, g2)
	}
	if len(sl.posts) != 1 || len(gm.sent) != 1 {
		t.Errorf("client calls should still be 1 each: slack=%d gmail=%d", len(sl.posts), len(gm.sent))
	}
}

// TestDigestSendRecipientChangeNotDedupedSameDay is M4: the dedup key
// hashes the recipient set, so correcting digest_recipients mid-day and
// resending must NOT be swallowed by the previous (wrong) send's dedup row.
func TestDigestSendRecipientChangeNotDedupedSameDay(t *testing.T) {
	pub, sl, gm, _, cfg := digestFixture(t, false, []string{"wrong@example.com"})
	data, err := learn.BuildDigestData(pub.St, 30)
	if err != nil {
		t.Fatal(err)
	}
	if _, g, err := pub.Send(context.Background(), data); err != nil || g != "sent" {
		t.Fatalf("first send: gmail=%s err=%v", g, err)
	}
	// Operator fixes the config mid-day.
	cfg.DigestRecipients = []string{"correct@example.com"}
	_, g2, err := pub.Send(context.Background(), data)
	if err != nil {
		t.Fatal(err)
	}
	if g2 != "sent" {
		t.Errorf("resend after recipients change: gmail=%s, want sent (new dest hash, not deduped)", g2)
	}
	if len(gm.sent) != 2 {
		t.Fatalf("expected 2 gmail sends (different recipient sets), got %d: %v", len(gm.sent), gm.sent)
	}
	if gm.sent[0] == gm.sent[1] {
		t.Errorf("destinations should differ between sends: %v", gm.sent)
	}
	// Slack leg is channel-keyed (unaffected by recipients), so it dedupes normally.
	if len(sl.posts) != 1 {
		t.Errorf("slack leg should dedupe (channel unchanged), got %d posts", len(sl.posts))
	}
}

func TestDigestSendEmptyFleetNoPanic(t *testing.T) {
	st, err := store.Open(filepath.Join(t.TempDir(), "empty.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	cfg := &config.Config{DryRun: false, DigestRecipients: []string{"ceo@example.com"}}
	sl, gm := &fakeSlack{}, &fakeGmail{}
	pub := &DigestPublisher{St: st, Guard: &Guard{Cfg: cfg, St: st}, Cfg: cfg, Slack: sl, GWS: gm, From: "me"}
	data, err := learn.BuildDigestData(st, 30)
	if err != nil {
		t.Fatal(err)
	}
	slackRes, gmailRes, err := pub.Send(context.Background(), data)
	if err != nil {
		t.Fatal(err)
	}
	if slackRes != "sent" || gmailRes != "sent" {
		t.Errorf("legs on empty fleet: slack=%s gmail=%s", slackRes, gmailRes)
	}
}

func gmailDate() string {
	return time.Now().UTC().Format("2006-01-02")
}
