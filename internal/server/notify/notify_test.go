package notify

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/getslowpoke/slowpoke/internal/server/game"
	"github.com/getslowpoke/slowpoke/internal/server/project"
	"github.com/getslowpoke/slowpoke/internal/server/store"
)

type captured struct {
	path, auth string
	body       map[string]any
}

type sink struct {
	mu   sync.Mutex
	got  []captured
	srv  *httptest.Server
	fail bool
}

func newSink(t *testing.T) *sink {
	s := &sink{}
	s.srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		raw, _ := io.ReadAll(r.Body)
		var body map[string]any
		_ = json.Unmarshal(raw, &body)
		s.mu.Lock()
		s.got = append(s.got, captured{r.URL.Path, r.Header.Get("Authorization"), body})
		fail := s.fail
		s.mu.Unlock()
		if fail {
			w.WriteHeader(500)
			return
		}
		_, _ = w.Write([]byte(`{"ok":true}`))
	}))
	t.Cleanup(s.srv.Close)
	return s
}

func (s *sink) byPath(prefix string) []captured {
	s.mu.Lock()
	defer s.mu.Unlock()
	var out []captured
	for _, c := range s.got {
		if strings.HasPrefix(c.path, prefix) {
			out = append(out, c)
		}
	}
	return out
}

type fixture struct {
	t        *testing.T
	ctx      context.Context
	db       *store.DB
	project  project.Project
	notifier *Notifier
	sink     *sink
	now      time.Time
}

func newFixture(t *testing.T, configure func(s *project.Settings, url string)) *fixture {
	db := store.OpenTest(t)
	ctx := context.Background()
	repo := project.Repo{DB: db}
	p, _ := repo.Create(ctx, "Shop", "Europe/Rome")
	sk := newSink(t)
	configure(&p.Settings, sk.srv.URL)
	if err := repo.Update(ctx, p); err != nil {
		t.Fatal(err)
	}
	f := &fixture{t: t, ctx: ctx, db: db, project: p, sink: sk, now: time.Date(2026, 9, 16, 12, 0, 0, 0, time.UTC)}
	f.notifier = New(db, game.New(db), "https://slowpoke.example.com")
	f.notifier.SlackAPI = sk.srv.URL + "/slack-api"
	f.notifier.TelegramAPI = sk.srv.URL + "/telegram"
	f.notifier.Now = func() time.Time { return f.now }
	f.notifier.Engine.Now = f.notifier.Now
	return f
}

func (f *fixture) activity(kind, icon, title string, data map[string]any) {
	raw, _ := json.Marshal(data)
	if _, err := f.db.Q().Exec(f.ctx, "INSERT INTO activities (project_id, type, icon, title, data, notified, created_at) VALUES (?, ?, ?, ?, ?, FALSE, ?)",
		f.project.ID, kind, icon, title, string(raw), f.now.Unix()); err != nil {
		f.t.Fatal(err)
	}
}

func (f *fixture) run() {
	p, _ := project.Repo{DB: f.db}.ByID(f.ctx, f.project.ID)
	if err := f.notifier.ProcessProject(f.ctx, p); err != nil {
		f.t.Fatal(err)
	}
}

func TestRulesRouteEventsToChannelsWithTemplatesAndMentions(t *testing.T) {
	f := newFixture(t, func(s *project.Settings, url string) {
		s.Notifications.Channels = []project.Channel{
			{ID: "alerts", Type: "slack", URL: url + "/slack-alerts"},
			{ID: "team", Type: "discord", URL: url + "/discord-team"},
			{ID: "ops", Type: "webhook", URL: url + "/hook"},
		}
		s.Notifications.Rules = []project.Rule{
			{Events: []string{"boss", "deploy_worse"}, Channel: "alerts"},
			{Events: []string{"mission_completed", "badge"}, Channel: "team"},
			{Events: []string{"*"}, Channel: "ops"},
		}
		s.Notifications.People = map[string]project.Person{"ada": {SlackUserID: "U123"}}
		s.Notifications.Templates = map[string]string{"mission_completed": "🎉 {{.Person}} earned {{.Points}} points: {{.Title}} ({{.Saved}})"}
	})
	f.activity("boss", "👾", "A regression appeared: GET /search", map[string]any{"person": "ada"})
	f.activity("mission_completed", "🧹", "Debt repaid: select * from orders", map[string]any{"person": "ada", "points": 120, "saved_seconds_per_day": 7260})
	f.activity("deploy", "✅", "Deploy v2 made the speed index better (+4.0)", map[string]any{"verdict": "better"})

	f.run()

	alerts := f.sink.byPath("/slack-alerts")
	if len(alerts) != 1 || !strings.Contains(alerts[0].body["text"].(string), "<@U123>") || !strings.Contains(alerts[0].body["text"].(string), "https://slowpoke.example.com/projects/shop/missions") {
		t.Fatalf("slack alerts %+v", alerts)
	}
	team := f.sink.byPath("/discord-team")
	if len(team) != 1 || team[0].body["content"] != "🎉 ada earned 120 points: Debt repaid: select * from orders (2h 1m per day)" {
		t.Fatalf("discord %+v", team)
	}
	hooks := f.sink.byPath("/hook")
	if len(hooks) != 3 || hooks[2].body["event"] != "deploy_better" || hooks[0].body["project"] != "Shop" {
		t.Fatalf("webhook %+v", hooks)
	}

	f.run()
	if len(f.sink.byPath("/")) != 5 {
		t.Fatal("activities must be sent once")
	}
}

func TestDirectMessagesToThePerson(t *testing.T) {
	f := newFixture(t, func(s *project.Settings, url string) {
		s.Notifications.Channels = []project.Channel{
			{ID: "slack", Type: "slack", URL: url + "/slack-channel", BotToken: "xoxb-test"},
			{ID: "tg", Type: "telegram", BotToken: "42:abc", ChatID: "-100"},
		}
		s.Notifications.Rules = []project.Rule{
			{Events: []string{"mission_completed"}, Channel: "slack", DirectMessage: true},
			{Events: []string{"mission_completed"}, Channel: "tg", DirectMessage: true},
		}
		s.Notifications.People = map[string]project.Person{"ada": {SlackUserID: "U123", TelegramChatID: "777"}}
	})
	f.activity("mission_completed", "🧹", "Debt repaid", map[string]any{"person": "ada", "points": 50})
	f.run()

	dm := f.sink.byPath("/slack-api/chat.postMessage")
	if len(dm) != 1 || dm[0].auth != "Bearer xoxb-test" || dm[0].body["channel"] != "U123" {
		t.Fatalf("slack dm %+v", dm)
	}
	tg := f.sink.byPath("/telegram/bot42:abc/sendMessage")
	chats := map[string]bool{}
	for _, c := range tg {
		chats[c.body["chat_id"].(string)] = true
	}
	if len(tg) != 2 || !chats["-100"] || !chats["777"] {
		t.Fatalf("telegram %+v", tg)
	}
}

func TestQuietHoursHoldMessages(t *testing.T) {
	f := newFixture(t, func(s *project.Settings, url string) {
		s.Notifications.Channels = []project.Channel{{ID: "hook", Type: "webhook", URL: url + "/hook"}}
		s.Notifications.Rules = []project.Rule{{Events: []string{"*"}, Channel: "hook"}}
		s.Notifications.Quiet.Enabled = true
		s.Notifications.Quiet.From, s.Notifications.Quiet.To = "22:00", "08:00"
	})
	f.now = time.Date(2026, 9, 16, 23, 30, 0, 0, time.FixedZone("CEST", 2*3600)) // 23:30 in Rome
	f.activity("badge", "🛡️", "Badge unlocked: Zero 500", nil)
	f.run()
	if len(f.sink.byPath("/hook")) != 0 {
		t.Fatal("sent during quiet hours")
	}
	f.now = time.Date(2026, 9, 17, 8, 5, 0, 0, time.FixedZone("CEST", 2*3600))
	f.run()
	if len(f.sink.byPath("/hook")) != 1 {
		t.Fatal("held message must be sent after quiet hours")
	}
}

func TestFailedDeliveryIsRetriedLater(t *testing.T) {
	f := newFixture(t, func(s *project.Settings, url string) {
		s.Notifications.Channels = []project.Channel{{ID: "hook", Type: "webhook", URL: url + "/hook"}}
		s.Notifications.Rules = []project.Rule{{Events: []string{"*"}, Channel: "hook"}}
	})
	f.sink.fail = true
	f.activity("badge", "🛡️", "Badge unlocked", nil)
	f.run()
	f.sink.fail = false
	f.now = f.now.Add(2 * time.Minute)
	f.run()
	f.run()
	if n := len(f.sink.byPath("/hook")); n != 2 {
		t.Fatalf("want one failed and one successful attempt, got %d", n)
	}
}

func TestWeeklyDigestIsSentOncePerWeek(t *testing.T) {
	f := newFixture(t, func(s *project.Settings, url string) {
		s.Notifications.Channels = []project.Channel{{ID: "team", Type: "slack", URL: url + "/slack-team"}}
		s.Notifications.Weekly.Enabled = true
		s.Notifications.Weekly.Channel = "team"
		s.Notifications.Weekly.Weekday = 1
		s.Notifications.Weekly.Hour = 9
	})
	f.db.Q().Exec(f.ctx, "INSERT INTO score_days (project_id, day, score, partial, closed, hours, updated_at) VALUES (?, '2026-09-20', 82.5, FALSE, TRUE, 24, 0)", f.project.ID)
	monday := time.Date(2026, 9, 21, 9, 10, 0, 0, f.project.Location())
	f.now = monday
	f.run()
	f.now = monday.Add(20 * time.Minute)
	f.run()
	msgs := f.sink.byPath("/slack-team")
	if len(msgs) != 1 || !strings.Contains(msgs[0].body["text"].(string), "82.5") {
		t.Fatalf("weekly %+v", msgs)
	}
}

func TestSendTest(t *testing.T) {
	f := newFixture(t, func(s *project.Settings, url string) {
		s.Notifications.Channels = []project.Channel{{ID: "team", Type: "teams", URL: url + "/teams"}}
	})
	p, _ := project.Repo{DB: f.db}.ByID(f.ctx, f.project.ID)
	if err := f.notifier.SendTest(f.ctx, p, "team"); err != nil {
		t.Fatal(err)
	}
	if err := f.notifier.SendTest(f.ctx, p, "nope"); err == nil {
		t.Fatal("unknown channel")
	}
	if got := f.sink.byPath("/teams"); len(got) != 1 || got[0].body["text"] == nil {
		t.Fatalf("%+v", got)
	}
}

func TestHumanDuration(t *testing.T) {
	cases := map[float64]string{12: "12s", 90: "1m 30s", 7260: "2h 1m", 0: "0s"}
	for in, want := range cases {
		if got := HumanDuration(in); got != want {
			t.Errorf("%v: %q", in, got)
		}
	}
}
