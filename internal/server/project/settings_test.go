package project

import (
	"strings"
	"testing"

	"github.com/getslowpoke/slowpoke/internal/server/tasks"
)

func TestDefaultsAreValid(t *testing.T) {
	s := Defaults()
	if err := s.Validate(); err != nil {
		t.Fatal(err)
	}
	var weight float64
	for _, c := range s.Score.Components {
		weight += c.Weight
	}
	if weight != 100 {
		t.Fatalf("default weights should sum to 100, got %v", weight)
	}
}

func TestParseOverlaysOnlyGivenKeys(t *testing.T) {
	s, err := ParseSettings(`{"missions":{"window_hours":1},"notifications":{"channels":[{"id":"dev","type":"slack","url":"https://hooks.slack.com/x"}]}}`)
	if err != nil {
		t.Fatal(err)
	}
	if s.Missions.WindowHours != 1 || s.Missions.GoodWindows != Defaults().Missions.GoodWindows {
		t.Fatalf("missions %+v", s.Missions)
	}
	if len(s.Notifications.Channels) != 1 || len(s.Score.Components) != len(Defaults().Score.Components) {
		t.Fatalf("settings %+v", s)
	}
}

func TestValidateRejectsNonsense(t *testing.T) {
	cases := map[string]func(*Settings){
		"good must be below bad": func(s *Settings) { s.Score.Components[0].Good = 5; s.Score.Components[0].Bad = 1 },
		"unknown metric":         func(s *Settings) { s.Score.Components[0].Metric = "vibes" },
		"negative weight":        func(s *Settings) { s.Score.Components[0].Weight = -1 },
		"no components":          func(s *Settings) { s.Score.Components = nil },
		"window too small":       func(s *Settings) { s.Missions.WindowHours = 0 },
		"webhook must be http(s)": func(s *Settings) {
			s.Notifications.Channels = []Channel{{ID: "x", Type: "discord", URL: "file:///etc/passwd"}}
		},
		"rule with unknown channel": func(s *Settings) { s.Notifications.Rules = []Rule{{Events: []string{"badge"}, Channel: "nope"}} },
		"rule with unknown event": func(s *Settings) {
			s.Notifications.Channels = []Channel{{ID: "x", Type: "webhook", URL: "https://example.com"}}
			s.Notifications.Rules = []Rule{{Events: []string{"vibes"}, Channel: "x"}}
		},
		"broken template":        func(s *Settings) { s.Notifications.Templates = map[string]string{"badge": "{{.Title"} },
		"telegram without token": func(s *Settings) { s.Notifications.Channels = []Channel{{ID: "t", Type: "telegram", ChatID: "1"}} },
		"bad quiet hours":        func(s *Settings) { s.Notifications.Quiet.Enabled = true; s.Notifications.Quiet.From = "25:00" },
		"levels need a zero":     func(s *Settings) { s.Score.Levels = []Level{{Min: 50, Name: "x"}} },
	}
	for name, mutate := range cases {
		s := Defaults()
		mutate(&s)
		if err := s.Validate(); err == nil {
			t.Errorf("%s: expected an error", name)
		}
	}
}

func TestLevelFor(t *testing.T) {
	s := Defaults()
	if l := s.LevelFor(96); l.Name != "Platinum" {
		t.Fatal(l)
	}
	if l := s.LevelFor(10); l.Name != "Climbing" {
		t.Fatal(l)
	}
	next := s.NextLevel(72)
	if next == nil || next.Name != "Gold" {
		t.Fatalf("%+v", next)
	}
	if s.NextLevel(99) != nil {
		t.Fatal("no level above platinum")
	}
}

func TestSlugify(t *testing.T) {
	if got := Slugify("  Il mio E-commerce!! "); got != "il-mio-e-commerce" {
		t.Fatal(got)
	}
	if !strings.HasPrefix(Slugify("???"), "project") {
		t.Fatal("empty slug fallback")
	}
}

func TestSecretsAreMaskedAndRestored(t *testing.T) {
	stored := Defaults()
	stored.Integrations.Tasks = []tasks.Integration{{ID: "gh", Provider: "github", Token: "ghp_real", Repo: "a/b"}}
	stored.Notifications.Channels = []Channel{{ID: "s", Type: "slack", URL: "https://hooks.slack.com/x", BotToken: "xoxb-real"}}

	masked := stored.Masked()
	if masked.Integrations.Tasks[0].Token != SecretMask || masked.Notifications.Channels[0].BotToken != SecretMask {
		t.Fatalf("%+v", masked)
	}
	if stored.Integrations.Tasks[0].Token != "ghp_real" {
		t.Fatal("masking must not change the stored settings")
	}
	masked.RestoreSecrets(stored)
	if masked.Integrations.Tasks[0].Token != "ghp_real" || masked.Notifications.Channels[0].BotToken != "xoxb-real" {
		t.Fatalf("%+v", masked)
	}
	bad := Defaults()
	bad.Integrations.Tasks = []tasks.Integration{{ID: "gh", Provider: "github", Token: "x", Repo: "nope"}}
	if bad.Validate() == nil {
		t.Fatal("invalid integration accepted")
	}
}
