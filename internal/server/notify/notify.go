// Package notify delivers activities to Slack, Discord, Teams, Telegram and
// webhooks, following the rules and templates of each project.
package notify

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"math"
	"net/http"
	"strings"
	"text/template"
	"time"

	"github.com/getslowpoke/slowpoke/internal/server/game"
	"github.com/getslowpoke/slowpoke/internal/server/project"
	"github.com/getslowpoke/slowpoke/internal/server/store"
)

const retryAfter = time.Minute

type Notifier struct {
	DB          *store.DB
	Engine      *game.Engine
	HTTP        *http.Client
	Now         func() time.Time
	PublicURL   string
	SlackAPI    string
	TelegramAPI string
	lastFailure map[int64]time.Time
}

func New(db *store.DB, engine *game.Engine, publicURL string) *Notifier {
	return &Notifier{
		DB: db, Engine: engine, HTTP: &http.Client{Timeout: 10 * time.Second}, Now: time.Now,
		PublicURL: strings.TrimRight(publicURL, "/"), SlackAPI: "https://slack.com/api", TelegramAPI: "https://api.telegram.org",
		lastFailure: map[int64]time.Time{},
	}
}

// Message is what templates see.
type Message struct {
	Event   string
	Project string
	Icon    string
	Title   string
	Person  string
	Mention string
	Points  int
	Saved   string
	URL     string
	Data    map[string]any
}

const DefaultTemplate = "{{.Icon}} *{{.Project}}* · {{.Title}}{{if .Points}} · +{{.Points}} pts{{end}}{{if .Saved}} · {{.Saved}} of waiting removed per day{{end}}{{if .Mention}} · {{.Mention}}{{end}}{{if .URL}}\n{{.URL}}{{end}}"

func HumanDuration(seconds float64) string {
	s := int64(math.Round(seconds))
	switch {
	case s >= 3600:
		return fmt.Sprintf("%dh %dm", s/3600, (s%3600)/60)
	case s >= 60:
		return fmt.Sprintf("%dm %ds", s/60, s%60)
	}
	return fmt.Sprintf("%ds", s)
}

func (n *Notifier) Run(ctx context.Context, interval time.Duration) {
	repo := project.Repo{DB: n.DB}
	tick := time.NewTicker(interval)
	defer tick.Stop()
	for {
		projects, err := repo.List(ctx)
		if err != nil {
			slog.Error("notify: cannot list projects", "error", err)
		}
		for _, p := range projects {
			unlock, ok, err := n.DB.TryLock(ctx, fmt.Sprintf("notify:%d", p.ID))
			if err != nil || !ok {
				continue
			}
			if err := n.ProcessProject(ctx, p); err != nil {
				slog.Error("notify: failed", "project", p.Slug, "error", err)
			}
			unlock()
		}
		select {
		case <-ctx.Done():
			return
		case <-tick.C:
		}
	}
}

func eventOf(kind string, data map[string]any) string {
	if kind == "deploy" {
		if v, _ := data["verdict"].(string); v != "" {
			return "deploy_" + v
		}
	}
	return kind
}

func (n *Notifier) pageFor(p project.Project, kind string) string {
	if n.PublicURL == "" {
		return ""
	}
	page := map[string]string{"mission_completed": "missions", "mission_claimed": "missions", "mission_shipped": "missions",
		"mission_confirmed": "missions", "mission_relapsed": "missions", "boss": "missions", "boss_defeated": "missions", "boss_escaped": "missions",
		"deploy": "deploys", "command_overlap": "jobs"}[kind]
	url := n.PublicURL + "/projects/" + p.Slug
	if page != "" {
		url += "/" + page
	}
	return url
}

func inQuietHours(p project.Project, now time.Time) bool {
	q := p.Settings.Notifications.Quiet
	if !q.Enabled {
		return false
	}
	clock := now.In(p.Location()).Format("15:04")
	if q.From <= q.To {
		return clock >= q.From && clock < q.To
	}
	return clock >= q.From || clock < q.To
}

func (n *Notifier) ProcessProject(ctx context.Context, p project.Project) error {
	now := n.Now()
	if err := n.weekly(ctx, p, now); err != nil {
		slog.Warn("notify: weekly digest failed", "project", p.Slug, "error", err)
	}

	settings := p.Settings.Notifications
	if len(settings.Rules) == 0 {
		// Nothing to deliver: mark as seen so enabling rules later does not flood the channels.
		_, err := n.DB.Q().Exec(ctx, "UPDATE activities SET notified = TRUE WHERE project_id = ? AND NOT notified", p.ID)
		return err
	}
	if inQuietHours(p, now) {
		return nil
	}
	if last, ok := n.lastFailure[p.ID]; ok && now.Sub(last) < retryAfter {
		return nil
	}

	rows, err := n.DB.Q().Query(ctx, "SELECT id, type, icon, title, data FROM activities WHERE project_id = ? AND NOT notified ORDER BY id LIMIT 50", p.ID)
	if err != nil {
		return err
	}
	type activity struct {
		id                     int64
		kind, icon, title, raw string
	}
	var pending []activity
	for rows.Next() {
		var a activity
		if err := rows.Scan(&a.id, &a.kind, &a.icon, &a.title, &a.raw); err != nil {
			rows.Close()
			return err
		}
		pending = append(pending, a)
	}
	rows.Close()

	for _, a := range pending {
		var data map[string]any
		_ = json.Unmarshal([]byte(a.raw), &data)
		msg := Message{Event: eventOf(a.kind, data), Project: p.Name, Icon: a.icon, Title: a.title, URL: n.pageFor(p, a.kind), Data: data}
		msg.Person, _ = data["person"].(string)
		if pts, ok := data["points"].(float64); ok {
			msg.Points = int(pts)
		}
		if saved, ok := data["saved_seconds_per_day"].(float64); ok && saved > 0 {
			msg.Saved = HumanDuration(saved) + " per day"
		}

		if err := n.deliver(ctx, p, msg); err != nil {
			n.lastFailure[p.ID] = now
			slog.Warn("notify: delivery failed, will retry", "project", p.Slug, "activity", a.id, "error", err)
			return nil
		}
		if _, err := n.DB.Q().Exec(ctx, "UPDATE activities SET notified = TRUE WHERE id = ?", a.id); err != nil {
			return err
		}
	}
	delete(n.lastFailure, p.ID)
	return nil
}

func matches(r project.Rule, event string) bool {
	for _, e := range r.Events {
		if e == "*" || e == event {
			return true
		}
	}
	return false
}

func (n *Notifier) deliver(ctx context.Context, p project.Project, msg Message) error {
	settings := p.Settings.Notifications
	person, hasPerson := settings.People[msg.Person]
	var errs []error
	for _, rule := range settings.Rules {
		if !matches(rule, msg.Event) {
			continue
		}
		ch, ok := settings.Channel(rule.Channel)
		if !ok {
			continue
		}
		m := msg
		if ch.Type == "slack" && hasPerson && person.SlackUserID != "" {
			m.Mention = "<@" + person.SlackUserID + ">"
		}
		text, err := render(settings.Templates[msg.Event], m)
		if err != nil {
			return err
		}
		if err := n.sendChannel(ctx, ch, m, text); err != nil {
			errs = append(errs, fmt.Errorf("%s: %w", ch.ID, err))
		}
		if rule.DirectMessage && hasPerson {
			if err := n.sendDirect(ctx, ch, person, text); err != nil {
				errs = append(errs, fmt.Errorf("%s dm: %w", ch.ID, err))
			}
		}
	}
	return errors.Join(errs...)
}

func render(tpl string, m Message) (string, error) {
	if strings.TrimSpace(tpl) == "" {
		tpl = DefaultTemplate
	}
	t, err := template.New("message").Parse(tpl)
	if err != nil {
		return "", err
	}
	var b strings.Builder
	if err := t.Execute(&b, m); err != nil {
		return "", err
	}
	return b.String(), nil
}

func (n *Notifier) post(ctx context.Context, url string, body any, headers map[string]string) error {
	raw, _ := json.Marshal(body)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(raw))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	for k, v := range headers {
		req.Header.Set(k, v)
	}
	res, err := n.HTTP.Do(req)
	if err != nil {
		return err
	}
	defer res.Body.Close()
	if res.StatusCode >= 300 {
		return fmt.Errorf("HTTP %d", res.StatusCode)
	}
	if strings.Contains(url, "slack.com/api") || strings.HasPrefix(url, n.SlackAPI) {
		var out struct {
			OK    bool   `json:"ok"`
			Error string `json:"error"`
		}
		if json.NewDecoder(res.Body).Decode(&out) == nil && !out.OK {
			return fmt.Errorf("slack: %s", out.Error)
		}
	}
	return nil
}

func (n *Notifier) sendChannel(ctx context.Context, ch project.Channel, m Message, text string) error {
	switch ch.Type {
	case "slack":
		if ch.URL == "" {
			return nil
		}
		return n.post(ctx, ch.URL, map[string]string{"text": text}, nil)
	case "discord":
		return n.post(ctx, ch.URL, map[string]string{"content": text}, nil)
	case "teams":
		return n.post(ctx, ch.URL, map[string]string{"text": strings.ReplaceAll(text, "\n", "\n\n")}, nil)
	case "telegram":
		if ch.ChatID == "" {
			return nil
		}
		return n.post(ctx, fmt.Sprintf("%s/bot%s/sendMessage", n.TelegramAPI, ch.BotToken), map[string]string{"chat_id": ch.ChatID, "text": text}, nil)
	case "webhook":
		return n.post(ctx, ch.URL, map[string]any{"event": m.Event, "project": m.Project, "title": m.Title, "icon": m.Icon, "text": text,
			"person": m.Person, "points": m.Points, "url": m.URL, "data": m.Data, "sent_at": n.Now().Unix()}, nil)
	}
	return fmt.Errorf("unknown channel type %q", ch.Type)
}

func (n *Notifier) sendDirect(ctx context.Context, ch project.Channel, person project.Person, text string) error {
	switch {
	case ch.Type == "slack" && ch.BotToken != "" && person.SlackUserID != "":
		return n.post(ctx, n.SlackAPI+"/chat.postMessage", map[string]string{"channel": person.SlackUserID, "text": text},
			map[string]string{"Authorization": "Bearer " + ch.BotToken})
	case ch.Type == "telegram" && person.TelegramChatID != "":
		return n.post(ctx, fmt.Sprintf("%s/bot%s/sendMessage", n.TelegramAPI, ch.BotToken), map[string]string{"chat_id": person.TelegramChatID, "text": text}, nil)
	}
	return nil
}

func (n *Notifier) SendTest(ctx context.Context, p project.Project, channelID string) error {
	ch, ok := p.Settings.Notifications.Channel(channelID)
	if !ok {
		return fmt.Errorf("unknown channel %q", channelID)
	}
	msg := Message{Event: "test", Project: p.Name, Icon: "🐢", Title: "Slowpoke is connected. Missions, bosses and badges will show up here.", URL: n.pageFor(p, "")}
	text, err := render("", msg)
	if err != nil {
		return err
	}
	return n.sendChannel(ctx, ch, msg, text)
}

// ---------------------------------------------------------------- weekly digest

func (n *Notifier) weekly(ctx context.Context, p project.Project, now time.Time) error {
	w := p.Settings.Notifications.Weekly
	local := now.In(p.Location())
	if !w.Enabled || int(local.Weekday()) != w.Weekday || local.Hour() != w.Hour {
		return nil
	}
	ch, ok := p.Settings.Notifications.Channel(w.Channel)
	if !ok {
		return nil
	}
	year, week := local.ISOWeek()
	res, err := n.DB.Q().Exec(ctx, "INSERT INTO app_settings (key, value) VALUES (?, '1') ON CONFLICT (key) DO NOTHING", fmt.Sprintf("weekly:%d:%d-%d", p.ID, year, week))
	if err != nil {
		return err
	}
	if affected, _ := res.RowsAffected(); affected == 0 {
		return nil
	}
	text, err := n.digest(ctx, p, local)
	if err != nil {
		return err
	}
	return n.sendChannel(ctx, ch, Message{Event: "weekly", Project: p.Name, Title: "Weekly report"}, text)
}

func (n *Notifier) digest(ctx context.Context, p project.Project, now time.Time) (string, error) {
	q := n.DB.Q()
	avg := func(from, to time.Time) (*float64, error) {
		var v *float64
		err := q.Row(ctx, "SELECT AVG(score) FROM score_days WHERE project_id = ? AND closed AND score IS NOT NULL AND day >= ? AND day < ?",
			p.ID, from.Format("2006-01-02"), to.Format("2006-01-02")).Scan(&v)
		return v, err
	}
	today := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, now.Location())
	last, err := avg(today.AddDate(0, 0, -7), today)
	if err != nil {
		return "", err
	}
	prev, err := avg(today.AddDate(0, 0, -14), today.AddDate(0, 0, -7))
	if err != nil {
		return "", err
	}

	var b strings.Builder
	fmt.Fprintf(&b, "📊 *%s* · weekly report\n", p.Name)
	if last != nil {
		level := p.Settings.LevelFor(*last)
		fmt.Fprintf(&b, "Speed index: %.1f %s %s", *last, level.Icon, level.Name)
		if prev != nil {
			fmt.Fprintf(&b, " (%+.1f vs previous week)", *last-*prev)
		}
		b.WriteString("\n")
	}

	debt, err := n.Engine.Debt(ctx, p)
	if err != nil {
		return "", err
	}
	fmt.Fprintf(&b, "Technical debt: %s of waiting per day", HumanDuration(debt.SecondsPerDay))
	if debt.PreviousSecondsPerDay > 0 {
		fmt.Fprintf(&b, " (%+.0f%% vs yesterday)", (debt.SecondsPerDay-debt.PreviousSecondsPerDay)/debt.PreviousSecondsPerDay*100)
	}
	b.WriteString("\n")

	var completed, points, bosses int
	weekAgo := now.Add(-7 * 24 * time.Hour).Unix()
	_ = q.Row(ctx, "SELECT COUNT(*) FROM missions WHERE project_id = ? AND status IN ('completed', 'defeated') AND closed_at >= ?", p.ID, weekAgo).Scan(&completed)
	_ = q.Row(ctx, "SELECT COALESCE(SUM(points), 0) FROM point_entries WHERE project_id = ? AND created_at >= ?", p.ID, weekAgo).Scan(&points)
	_ = q.Row(ctx, "SELECT COUNT(*) FROM missions WHERE project_id = ? AND type = 'boss' AND status = 'open'", p.ID).Scan(&bosses)
	fmt.Fprintf(&b, "Missions closed this week: %d · +%d pts", completed, points)
	if bosses > 0 {
		fmt.Fprintf(&b, " · 👾 %d open regressions", bosses)
	}
	b.WriteString("\n")

	for i, item := range debt.Items {
		if i == 3 {
			break
		}
		label := item.Label
		if len(label) > 70 {
			label = label[:70] + "…"
		}
		fmt.Fprintf(&b, "%d. %s · %s/day\n", i+1, label, HumanDuration(item.Seconds))
	}
	if url := n.pageFor(p, ""); url != "" {
		b.WriteString(url)
	}
	return b.String(), nil
}
