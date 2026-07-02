package integrations

import (
	"path/filepath"
	"strings"
	"testing"

	"github.com/phuc-nt/dandori/internal/config"
	"github.com/phuc-nt/dandori/internal/store"
)

type fakeSlack struct{ posts []string }

func (f *fakeSlack) PostMessage(channel, text string) (string, error) {
	f.posts = append(f.posts, text)
	return "ts", nil
}

type fakeConf struct{ pages []string }

func (f *fakeConf) CreatePage(spaceID, parentID, title, body string) (string, error) {
	f.pages = append(f.pages, title+" "+body)
	return "id", nil
}

func flywheelFixture(t *testing.T, dryRun bool) (*FlywheelPublisher, *fakeSlack, *fakeConf, *store.Store) {
	t.Helper()
	st, err := store.Open(filepath.Join(t.TempDir(), "t.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	st.DB.Exec(`INSERT INTO agents(id, name, created_at) VALUES('a','Agent A', 'now')`)
	st.DB.Exec(`INSERT INTO runs(id, session_id, agent_id, started_at) VALUES('r1','r1','a','now')`)
	st.DB.Exec(`INSERT INTO playbooks(id, name, run_id, agent_id, task_key, prompt, model, cost_usd, top_files, notes, created_at, created_by)
		VALUES(1, 'Pattern: Agent A · SCRUM-9', 'r1', 'a', 'SCRUM-9', '', 'sonnet', 1.5, '[]', 'Run sạch — mẫu đáng nhân bản.', 'now', 'phucnt')`)
	cfg := &config.Config{DryRun: dryRun}
	sl, cf := &fakeSlack{}, &fakeConf{}
	pub := &FlywheelPublisher{St: st, Guard: &Guard{Cfg: cfg, St: st},
		Slack: sl, SlackChannel: "C123", Confluence: cf, SpaceID: "S1"}
	return pub, sl, cf, st
}

func TestPublishDryRunTouchesNothingExternal(t *testing.T) {
	pub, sl, cf, st := flywheelFixture(t, true)
	s, c, err := pub.Publish(1, "phucnt")
	if err != nil {
		t.Fatal(err)
	}
	if s != "dry-run" || c != "dry-run" {
		t.Errorf("legs: %s/%s", s, c)
	}
	if len(sl.posts)+len(cf.pages) != 0 {
		t.Error("dry-run reached a client")
	}
	var skips int
	st.DB.QueryRow(`SELECT count(*) FROM events WHERE kind='write_skipped'`).Scan(&skips)
	if skips != 2 {
		t.Errorf("write_skipped events: %d", skips)
	}
}

func TestPublishSendsOncePerDay(t *testing.T) {
	pub, sl, cf, _ := flywheelFixture(t, false)
	if s, c, err := pub.Publish(1, "phucnt"); err != nil || s != "sent" || c != "sent" {
		t.Fatalf("first publish: %s/%s %v", s, c, err)
	}
	if s, c, _ := pub.Publish(1, "phucnt"); s != "deduped" || c != "deduped" {
		t.Errorf("second publish: %s/%s, want dedup", s, c)
	}
	if len(sl.posts) != 1 || len(cf.pages) != 1 {
		t.Errorf("client calls: slack=%d conf=%d", len(sl.posts), len(cf.pages))
	}
}

// The card content is pattern-only: agent attribution is fine, operator
// identities and ranking words are not.
func TestPublishedCardHasNoHumanRanking(t *testing.T) {
	pub, sl, cf, _ := flywheelFixture(t, false)
	if _, _, err := pub.Publish(1, "phucnt"); err != nil {
		t.Fatal(err)
	}
	all := strings.Join(append(sl.posts, cf.pages...), " ")
	for _, banned := range []string{"@", "xếp hạng", "leaderboard", "top operator", "giỏi nhất"} {
		if strings.Contains(strings.ToLower(all), banned) {
			t.Errorf("published card contains %q: %s", banned, all)
		}
	}
	if !strings.Contains(all, "Agent A") || !strings.Contains(all, "mẫu đáng nhân bản") {
		t.Errorf("card missing pattern content: %s", all)
	}
}
