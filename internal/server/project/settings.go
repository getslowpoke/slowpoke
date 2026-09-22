package project

import (
	"github.com/getslowpoke/slowpoke/internal/server/tasks"

	"encoding/json"
	"errors"
	"fmt"
	"math"
	"net/url"
	"regexp"
	"sort"
	"strings"
	"text/template"
)

type Component struct {
	Key    string  `json:"key"`
	Label  string  `json:"label"`
	Metric string  `json:"metric"`
	Source string  `json:"source"`         // optional prefix of HTTP sources, e.g. "nginx:api"
	Name   string  `json:"name,omitempty"` // custom metric name, for custom_* metrics
	Weight float64 `json:"weight"`
	Good   float64 `json:"good"`
	Bad    float64 `json:"bad"`
}

type Level struct {
	Min   float64 `json:"min"`
	Name  string  `json:"name"`
	Icon  string  `json:"icon"`
	Color string  `json:"color"`
}

type Settings struct {
	Score struct {
		Components         []Component `json:"components"`
		Levels             []Level     `json:"levels"`
		MinHoursForFullDay float64     `json:"min_hours_for_full_day"`
	} `json:"score"`
	HTTP struct {
		SlowSeconds float64 `json:"slow_seconds"`
	} `json:"http"`
	DB struct {
		ExplainMinSecondsPerDay float64 `json:"explain_min_seconds_per_day"`
	} `json:"db"`
	Missions struct {
		MaxOpen     int     `json:"max_open"`
		WindowHours float64 `json:"window_hours"`
		GoodWindows int     `json:"good_windows"`
		TargetRatio float64 `json:"target_ratio"`
		MinSeconds  float64 `json:"min_seconds"`
		ExpireDays  int     `json:"expire_days"`
	} `json:"missions"`
	Bosses struct {
		Growth        float64 `json:"growth"`
		MinSeconds    float64 `json:"min_seconds"`
		DeadlineHours float64 `json:"deadline_hours"`
	} `json:"bosses"`
	Deploys struct {
		CompareMinutes int `json:"compare_minutes"`
	} `json:"deploys"`
	// Traffic says the hours the project is judged on (missions, bosses, deploys). Empty days:
	// learnt from the traffic. Days are 0 (Sunday) to 6, hours 0 to 24, from included, to not.
	Traffic struct {
		BusyDays []int `json:"busy_days"`
		BusyFrom int   `json:"busy_from"`
		BusyTo   int   `json:"busy_to"`
	} `json:"traffic"`
	Notifications Notifications `json:"notifications"`
	People        struct {
		Leaderboard bool `json:"leaderboard"`
	} `json:"people"`
	Integrations struct {
		Tasks []tasks.Integration `json:"tasks"`
	} `json:"integrations"`
	// Debt is the repayment budget: a standing allocation beats occasional refactoring sprints.
	Debt struct {
		WeeklyGoalSeconds float64 `json:"weekly_goal_seconds"` // waiting time per day to repay each week
		CapacityPercent   int     `json:"capacity_percent"`    // share of each sprint reserved for debt
	} `json:"debt"`
}

// Metrics understood by the speed index.
var Metrics = map[string]string{
	"http_slow_ratio":  "share of requests over 2 s",
	"http_p95":         "p95 response time (seconds)",
	"http_error_rate":  "5xx every 10,000 requests",
	"php_stalls":       "PHP slowlog entries every 1,000 requests",
	"db_slow_per_hour": "seconds of slow queries per hour",
	"db_worst":         "slowest query (seconds)",
	"custom_p95":       "p95 of a custom timer (seconds)",
	"custom_avg":       "average of a custom timer or gauge",
	"custom_max":       "maximum of a custom gauge or timer",
	"custom_per_hour":  "custom counter total per hour",
}

// NotificationEvents are the activity types a rule can route (deploys are split by verdict).
var NotificationEvents = []string{"level_up", "badge", "mission_completed", "boss", "boss_defeated", "boss_escaped", "deploy_better", "deploy_worse", "deploy_neutral", "command_overlap", "debt_goal", "mission_claimed", "pull_request", "mission_shipped", "mission_confirmed", "mission_relapsed"}

var ChannelTypes = []string{"slack", "discord", "teams", "telegram", "webhook"}

type Channel struct {
	ID   string `json:"id"`
	Name string `json:"name"`
	Type string `json:"type"`
	// Incoming webhook URL (slack, discord, teams, webhook).
	URL string `json:"url,omitempty"`
	// Slack bot token (xoxb-...) for direct messages; Telegram bot token.
	BotToken string `json:"bot_token,omitempty"`
	// Telegram chat id for channel messages.
	ChatID string `json:"chat_id,omitempty"`
}

type Rule struct {
	Events  []string `json:"events"`
	Channel string   `json:"channel"`
	// Also send a direct message to the person behind the event (deploy author), when mapped.
	DirectMessage bool `json:"direct_message"`
}

// Person maps a deploy author (name or email) to chat identities.
type Person struct {
	SlackUserID    string `json:"slack_user_id,omitempty"`
	TelegramChatID string `json:"telegram_chat_id,omitempty"`
}

type Notifications struct {
	Channels  []Channel         `json:"channels"`
	Rules     []Rule            `json:"rules"`
	People    map[string]Person `json:"people"`
	Templates map[string]string `json:"templates"`
	Quiet     struct {
		Enabled bool   `json:"enabled"`
		From    string `json:"from"` // "22:00"
		To      string `json:"to"`   // "08:00"
	} `json:"quiet_hours"`
	Weekly struct {
		Enabled bool   `json:"enabled"`
		Channel string `json:"channel"`
		Weekday int    `json:"weekday"` // 0 = Sunday
		Hour    int    `json:"hour"`
	} `json:"weekly"`
}

func Defaults() Settings {
	var s Settings
	s.Score.Components = []Component{
		{Key: "http_slow", Label: "Requests slower than 2 seconds", Metric: "http_slow_ratio", Weight: 25, Good: 0.002, Bad: 0.02},
		{Key: "http_p95", Label: "Response time for most people", Metric: "http_p95", Weight: 20, Good: 0.3, Bad: 1.5},
		{Key: "php_stalls", Label: "How often PHP gets stuck", Metric: "php_stalls", Weight: 15, Good: 0.5, Bad: 10},
		{Key: "db_time", Label: "Time lost on slow queries", Metric: "db_slow_per_hour", Weight: 20, Good: 30, Bad: 900},
		{Key: "db_worst", Label: "The slowest query of all", Metric: "db_worst", Weight: 10, Good: 5, Bad: 60},
		{Key: "errors", Label: "Requests the server failed", Metric: "http_error_rate", Weight: 10, Good: 1, Bad: 20},
	}
	s.Score.Levels = []Level{
		{Min: 95, Name: "Platinum", Icon: "💎", Color: "#6366f1"},
		{Min: 85, Name: "Gold", Icon: "🥇", Color: "#d97706"},
		{Min: 70, Name: "Silver", Icon: "🥈", Color: "#64748b"},
		{Min: 50, Name: "Bronze", Icon: "🥉", Color: "#b45309"},
		{Min: 0, Name: "Climbing", Icon: "🌱", Color: "#94a3b8"},
	}
	s.Score.MinHoursForFullDay = 20
	s.HTTP.SlowSeconds = 1
	s.DB.ExplainMinSecondsPerDay = 30
	s.Missions.MaxOpen = 5
	s.Missions.WindowHours = 24
	s.Missions.GoodWindows = 3
	s.Missions.TargetRatio = 0.5
	s.Missions.MinSeconds = 60
	s.Missions.ExpireDays = 30
	s.Bosses.Growth = 2
	s.Bosses.MinSeconds = 120
	s.Bosses.DeadlineHours = 72
	s.Deploys.CompareMinutes = 60
	s.Debt.WeeklyGoalSeconds = 600
	s.Debt.CapacityPercent = 20
	s.Notifications.People = map[string]Person{}
	s.Notifications.Templates = map[string]string{}
	s.Notifications.Quiet.From, s.Notifications.Quiet.To = "22:00", "08:00"
	s.Notifications.Weekly.Weekday, s.Notifications.Weekly.Hour = 1, 9
	return s
}

// ParseSettings overlays the stored JSON on the defaults.
func ParseSettings(raw string) (Settings, error) {
	s := Defaults()
	if raw == "" || raw == "{}" {
		return s, nil
	}
	if err := json.Unmarshal([]byte(raw), &s); err != nil {
		return Defaults(), err
	}
	return s, nil
}

func (s Settings) Validate() error {
	var errs []error
	if tr := s.Traffic; len(tr.BusyDays) > 0 {
		for _, d := range tr.BusyDays {
			if d < 0 || d > 6 {
				errs = append(errs, fmt.Errorf("traffic: busy day %d must be 0 (Sunday) to 6", d))
			}
		}
		if tr.BusyFrom < 0 || tr.BusyTo > 24 || tr.BusyFrom >= tr.BusyTo {
			errs = append(errs, errors.New("traffic: busy hours must go from 0 to 24, the start before the end"))
		}
	}
	if len(s.Score.Components) == 0 {
		errs = append(errs, errors.New("score: at least one component"))
	}
	keys := map[string]bool{}
	for _, c := range s.Score.Components {
		if c.Key == "" || keys[c.Key] {
			errs = append(errs, fmt.Errorf("score: component key %q empty or repeated", c.Key))
		}
		keys[c.Key] = true
		if _, ok := Metrics[c.Metric]; !ok {
			errs = append(errs, fmt.Errorf("score: %s: unknown metric %q", c.Key, c.Metric))
		}
		if c.Weight < 0 || c.Weight > 1000 {
			errs = append(errs, fmt.Errorf("score: %s: weight must be between 0 and 1000", c.Key))
		}
		if strings.HasPrefix(c.Metric, "custom_") && strings.TrimSpace(c.Name) == "" {
			errs = append(errs, fmt.Errorf("score: %s: custom metrics need the metric name", c.Key))
		}
		if c.Good <= 0 || c.Bad <= c.Good {
			errs = append(errs, fmt.Errorf("score: %s: need 0 < good < bad", c.Key))
		}
	}
	hasZero := false
	for _, l := range s.Score.Levels {
		if l.Min == 0 {
			hasZero = true
		}
		if l.Name == "" || l.Min < 0 || l.Min > 100 {
			errs = append(errs, fmt.Errorf("score: invalid level %+v", l))
		}
	}
	if !hasZero {
		errs = append(errs, errors.New("score: levels need one starting at 0"))
	}
	if s.HTTP.SlowSeconds <= 0 {
		errs = append(errs, errors.New("http: slow_seconds must be positive"))
	}
	m := s.Missions
	if m.WindowHours < 1 || m.WindowHours != math.Trunc(m.WindowHours) || m.WindowHours > 168 || m.GoodWindows < 1 || m.TargetRatio <= 0 || m.TargetRatio >= 1 || m.MaxOpen < 0 || m.ExpireDays < 1 {
		errs = append(errs, errors.New("missions: invalid values"))
	}
	if s.Bosses.Growth <= 1 || s.Bosses.DeadlineHours <= 0 {
		errs = append(errs, errors.New("bosses: growth must be > 1 and deadline positive"))
	}
	if s.Debt.WeeklyGoalSeconds < 0 || s.Debt.CapacityPercent < 0 || s.Debt.CapacityPercent > 100 {
		errs = append(errs, errors.New("debt: weekly goal must be positive and capacity between 0 and 100"))
	}
	if s.Deploys.CompareMinutes < 5 {
		errs = append(errs, errors.New("deploys: compare_minutes must be at least 5"))
	}
	errs = append(errs, s.Notifications.validate()...)
	taskIDs := map[string]bool{}
	for _, ti := range s.Integrations.Tasks {
		if err := ti.Validate(); err != nil {
			errs = append(errs, err)
		}
		if taskIDs[ti.ID] {
			errs = append(errs, fmt.Errorf("task integration id %q repeated", ti.ID))
		}
		taskIDs[ti.ID] = true
	}
	return errors.Join(errs...)
}

func (s Settings) sortedLevels() []Level {
	levels := append([]Level(nil), s.Score.Levels...)
	sort.Slice(levels, func(i, j int) bool { return levels[i].Min > levels[j].Min })
	return levels
}

func (s Settings) LevelFor(score float64) Level {
	for _, l := range s.sortedLevels() {
		if score >= l.Min {
			return l
		}
	}
	return Level{Name: "—"}
}

// NextLevel is the closest level above the score, nil at the top.
func (s Settings) NextLevel(score float64) *Level {
	var next *Level
	for _, l := range s.sortedLevels() {
		if l.Min > score {
			l := l
			next = &l
		}
	}
	return next
}

func validHTTPURL(u string) bool {
	parsed, err := url.Parse(u)
	return err == nil && (parsed.Scheme == "https" || parsed.Scheme == "http") && parsed.Host != ""
}

var clockRe = regexp.MustCompile(`^([01]\d|2[0-3]):[0-5]\d$`)

func (n Notifications) Channel(id string) (Channel, bool) {
	for _, c := range n.Channels {
		if c.ID == id {
			return c, true
		}
	}
	return Channel{}, false
}

func (n Notifications) validate() []error {
	var errs []error
	ids := map[string]bool{}
	for _, c := range n.Channels {
		if c.ID == "" || ids[c.ID] {
			errs = append(errs, fmt.Errorf("notifications: channel id %q empty or repeated", c.ID))
		}
		ids[c.ID] = true
		switch c.Type {
		case "slack", "discord", "teams", "webhook":
			if !validHTTPURL(c.URL) && !(c.Type == "slack" && c.BotToken != "") {
				errs = append(errs, fmt.Errorf("notifications: channel %s needs an http(s) webhook URL", c.ID))
			}
		case "telegram":
			if c.BotToken == "" {
				errs = append(errs, fmt.Errorf("notifications: channel %s needs a bot token", c.ID))
			}
		default:
			errs = append(errs, fmt.Errorf("notifications: channel %s has unknown type %q", c.ID, c.Type))
		}
	}
	known := map[string]bool{}
	for _, e := range NotificationEvents {
		known[e] = true
	}
	for i, r := range n.Rules {
		if !ids[r.Channel] {
			errs = append(errs, fmt.Errorf("notifications: rule %d uses unknown channel %q", i+1, r.Channel))
		}
		for _, e := range r.Events {
			if !known[e] && e != "*" {
				errs = append(errs, fmt.Errorf("notifications: rule %d has unknown event %q", i+1, e))
			}
		}
	}
	for event, tpl := range n.Templates {
		if _, err := template.New(event).Parse(tpl); err != nil {
			errs = append(errs, fmt.Errorf("notifications: template %s: %v", event, err))
		}
	}
	if n.Quiet.Enabled && (!clockRe.MatchString(n.Quiet.From) || !clockRe.MatchString(n.Quiet.To)) {
		errs = append(errs, errors.New("notifications: quiet hours must be HH:MM"))
	}
	if n.Weekly.Enabled && (!ids[n.Weekly.Channel] || n.Weekly.Weekday < 0 || n.Weekly.Weekday > 6 || n.Weekly.Hour < 0 || n.Weekly.Hour > 23) {
		errs = append(errs, errors.New("notifications: weekly digest needs a known channel, weekday 0-6 and hour 0-23"))
	}
	return errs
}

// SecretMask replaces stored secrets in API responses; sending it back keeps the stored value.
const SecretMask = "••••••••"

// Masked returns a copy without secrets, for the browser.
func (s Settings) Masked() Settings {
	out := s
	out.Integrations.Tasks = nil
	for _, t := range s.Integrations.Tasks {
		if t.Token != "" {
			t.Token = SecretMask
		}
		out.Integrations.Tasks = append(out.Integrations.Tasks, t)
	}
	out.Notifications.Channels = nil
	for _, c := range s.Notifications.Channels {
		if c.BotToken != "" {
			c.BotToken = SecretMask
		}
		out.Notifications.Channels = append(out.Notifications.Channels, c)
	}
	return out
}

// RestoreSecrets puts back the stored secrets where the client sent the mask.
func (s *Settings) RestoreSecrets(stored Settings) {
	for i, t := range s.Integrations.Tasks {
		if t.Token == SecretMask {
			for _, old := range stored.Integrations.Tasks {
				if old.ID == t.ID {
					s.Integrations.Tasks[i].Token = old.Token
				}
			}
		}
	}
	for i, c := range s.Notifications.Channels {
		if c.BotToken == SecretMask {
			for _, old := range stored.Notifications.Channels {
				if old.ID == c.ID {
					s.Notifications.Channels[i].BotToken = old.BotToken
				}
			}
		}
	}
}
