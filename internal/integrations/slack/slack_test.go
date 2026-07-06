package slack

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	"github.com/phuc-nt/dandori/internal/config"
	"github.com/phuc-nt/dandori/internal/integrations"
	"github.com/phuc-nt/dandori/internal/store"
	"github.com/phuc-nt/dandori/internal/store/testseed"
)

type fakeSlack struct {
	posts     []string
	reactions []Reaction
}

func newFake(t *testing.T) (*Client, *fakeSlack) {
	t.Helper()
	f := &fakeSlack{}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		r.ParseForm()
		switch r.URL.Path {
		case "/chat.postMessage":
			if r.Header.Get("Authorization") != "Bearer xoxc-test" || r.Header.Get("Cookie") != "d=xoxd-test%2B" {
				w.Write([]byte(`{"ok":false,"error":"not_authed"}`))
				return
			}
			f.posts = append(f.posts, r.Form.Get("text"))
			w.Write([]byte(`{"ok":true,"ts":"111.222"}`))
		case "/reactions.get":
			resp := map[string]any{"ok": true, "message": map[string]any{"reactions": f.reactions}}
			json.NewEncoder(w).Encode(resp)
		case "/users.info":
			w.Write([]byte(`{"ok":true,"user":{"name":"phuc","profile":{"display_name":"Phuc N."}}}`))
		default:
			w.Write([]byte(`{"ok":true}`))
		}
	}))
	t.Cleanup(srv.Close)
	c := New("xoxc-test", "xoxd-test%2B")
	c.BaseURL = srv.URL
	return c, f
}

func liveEnv(t *testing.T) (*store.Store, *integrations.Guard) {
	t.Helper()
	st, err := store.Open(filepath.Join(t.TempDir(), "s.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	cfg, _ := config.Load(filepath.Join(t.TempDir(), "no.yaml"))
	cfg.DryRun = false // tests exercise the live path against the fake server
	return st, &integrations.Guard{Cfg: cfg, St: st}
}

func TestAlertVietnameseFormatAndDeepLink(t *testing.T) {
	st, guard := liveEnv(t)
	client, fake := newFake(t)
	a := &Alerter{St: st, Client: client, Guard: guard, Channel: "C1", BaseURL: "https://dandori.test"}

	testseed.Agent(t, st, "a1")
	testseed.Run(t, st, "r1", "a1", "running", 0, 0)
	testseed.Event(t, st, "r1", "closed_loop", "a1", 0, "grade F: agent a1")

	if err := a.Dispatch(); err != nil {
		t.Fatal(err)
	}
	if len(fake.posts) != 1 {
		t.Fatalf("posts: %d, want 1", len(fake.posts))
	}
	msg := fake.posts[0]
	// Business-language, not the old ":rotating_light: Dandori closed_loop".
	if strings.Contains(msg, "closed_loop") || strings.Contains(msg, "rotating_light") {
		t.Errorf("message still technical: %q", msg)
	}
	if !strings.Contains(msg, "hạ quyền tự chủ") {
		t.Errorf("message missing Vietnamese lead: %q", msg)
	}
	if !strings.Contains(msg, "https://dandori.test/dash/org") {
		t.Errorf("message missing deep link: %q", msg)
	}
}

func TestAlertFlagStaleKindIsDispatched(t *testing.T) {
	st, guard := liveEnv(t)
	client, fake := newFake(t)
	a := &Alerter{St: st, Client: client, Guard: guard, Channel: "C1", BaseURL: "https://d.test"}

	testseed.Agent(t, st, "a1")
	testseed.Run(t, st, "r1", "a1", "running", 0, 0)
	testseed.Event(t, st, "r1", "flag_stale", "a1", 0, "cảnh báo #7 a1 mở quá 3 ngày chưa xử lý")
	if err := a.Dispatch(); err != nil {
		t.Fatal(err)
	}
	if len(fake.posts) != 1 {
		t.Fatalf("flag_stale not dispatched: %d posts", len(fake.posts))
	}
	if !strings.Contains(fake.posts[0], "chưa xử lý") {
		t.Errorf("flag_stale payload missing: %q", fake.posts[0])
	}
}

func TestAlertDedup(t *testing.T) {
	st, guard := liveEnv(t)
	client, fake := newFake(t)
	a := &Alerter{St: st, Client: client, Guard: guard, Channel: "C1"}

	testseed.Agent(t, st, "a1")
	testseed.Run(t, st, "r1", "a1", "running", 0, 0)
	// Two identical budget warns (same payload, same day) → one Slack post.
	testseed.Event(t, st, "r1", "budget_warn", "", 0, "budget global at 75%")
	testseed.Event(t, st, "r1", "budget_warn", "", 0, "budget global at 75%")

	if err := a.Dispatch(); err != nil {
		t.Fatal(err)
	}
	if len(fake.posts) != 1 {
		t.Errorf("posts: %d, want 1 (dedup)", len(fake.posts))
	}
	// Second dispatch: nothing new.
	a.Dispatch()
	if len(fake.posts) != 1 {
		t.Errorf("posts after re-dispatch: %d", len(fake.posts))
	}
}

func TestApprovalReactionApproves(t *testing.T) {
	st, guard := liveEnv(t)
	client, fake := newFake(t)
	b := &ApprovalBridge{St: st, Client: client, Guard: guard, Channel: "C1", ConsoleURL: "http://x"}

	testseed.Agent(t, st, "a1")
	testseed.Run(t, st, "r1", "a1", "running", 0, 0)
	st.DB.Exec(`INSERT INTO approvals(run_id, action, reason, requested_at) VALUES('r1','git push','gate', ?)`, store.Now())

	b.Tick() // posts the approval message
	if len(fake.posts) != 1 {
		t.Fatalf("approval not posted: %d", len(fake.posts))
	}
	var ts string
	st.DB.QueryRow(`SELECT slack_ts FROM approvals WHERE id=1`).Scan(&ts)
	if ts != "111.222" {
		t.Fatalf("slack_ts: %q", ts)
	}

	fake.reactions = []Reaction{{Name: "white_check_mark", Users: []string{"U123"}}}
	b.Tick() // polls and approves
	var status, by, note string
	st.DB.QueryRow(`SELECT status, decided_by, decision_note FROM approvals WHERE id=1`).Scan(&status, &by, &note)
	if status != "approved" || by != "Phuc N. (U123)" || note != "via slack reaction" {
		t.Errorf("approval: %s by %q note %q", status, by, note)
	}
	var audits int
	st.DB.QueryRow(`SELECT count(*) FROM audit_log WHERE action='approval_approved'`).Scan(&audits)
	if audits != 1 {
		t.Errorf("audit: %d", audits)
	}
}

// Reactions from users outside the approvers whitelist must be ignored.
func TestApproverWhitelist(t *testing.T) {
	st, guard := liveEnv(t)
	client, fake := newFake(t)
	b := &ApprovalBridge{St: st, Client: client, Guard: guard, Channel: "C1",
		ConsoleURL: "http://x", Approvers: []string{"U-BOSS"}}
	testseed.Agent(t, st, "a1")
	testseed.Run(t, st, "r1", "a1", "running", 0, 0)
	st.DB.Exec(`INSERT INTO approvals(run_id, action, reason, requested_at) VALUES('r1','git push','gate', ?)`, store.Now())

	b.Tick() // post
	fake.reactions = []Reaction{{Name: "white_check_mark", Users: []string{"U-RANDO"}}}
	b.Tick() // poll — U-RANDO not whitelisted
	var status string
	st.DB.QueryRow(`SELECT status FROM approvals WHERE id=1`).Scan(&status)
	if status != "pending" {
		t.Errorf("non-approver reaction must not decide: %s", status)
	}

	// Griefing guard: a non-approver reacting FIRST must not block a later
	// whitelisted reaction on the same message.
	fake.reactions = []Reaction{{Name: "white_check_mark", Users: []string{"U-RANDO", "U-BOSS"}}}
	b.Tick()
	st.DB.QueryRow(`SELECT status FROM approvals WHERE id=1`).Scan(&status)
	if status != "approved" {
		t.Errorf("whitelisted approver must decide despite earlier non-approver reaction: %s", status)
	}
	var by string
	st.DB.QueryRow(`SELECT decided_by FROM approvals WHERE id=1`).Scan(&by)
	if !strings.Contains(by, "U-BOSS") {
		t.Errorf("decided_by must record the whitelisted user id: %q", by)
	}
}

func TestDryRunPostsNothing(t *testing.T) {
	st, guard := liveEnv(t)
	guard.Cfg.DryRun = true
	client, fake := newFake(t)
	b := &ApprovalBridge{St: st, Client: client, Guard: guard, Channel: "C1", ConsoleURL: "http://x"}
	testseed.Agent(t, st, "a1")
	testseed.Run(t, st, "r1", "a1", "running", 0, 0)
	st.DB.Exec(`INSERT INTO approvals(run_id, action, reason, requested_at) VALUES('r1','deploy','gate', ?)`, store.Now())
	b.Tick()
	if len(fake.posts) != 0 {
		t.Errorf("dry run must not post: %d", len(fake.posts))
	}
	var skipped int
	st.DB.QueryRow(`SELECT count(*) FROM events WHERE kind='write_skipped'`).Scan(&skipped)
	if skipped != 1 {
		t.Errorf("write_skipped: %d", skipped)
	}
}
