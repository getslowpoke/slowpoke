package notify

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/getslowpoke/slowpoke/internal/server/project"
)

// Slack gets a card instead of a line: what happened on what, before and after, who and how
// many points, a button to the mission and the Slowpoke logo. The plain text still goes along:
// it is what the phone notification and the channel preview show.

// missionInfo is what a card says about the mission behind an event.
type missionInfo struct {
	ID       int64
	Type     string
	Label    string
	Before   float64 // seconds of waiting per day when it opened
	Now      float64 // seconds per day at the last check, -1 when not measured yet
	Points   int
	Person   string
	DueAt    int64
	Status   string
}

func (n *Notifier) mission(ctx context.Context, p project.Project, id int64) (*missionInfo, error) {
	var m missionInfo
	var current sql.NullFloat64
	var due sql.NullInt64
	err := n.DB.Q().Row(ctx, "SELECT id, type, label, baseline, current, points, person, due_at, status FROM missions WHERE id = ? AND project_id = ?", id, p.ID).
		Scan(&m.ID, &m.Type, &m.Label, &m.Before, &current, &m.Points, &m.Person, &due, &m.Status)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	perDay := 24 / max(1, p.Settings.Missions.WindowHours) // missions keep seconds per judging window
	m.Before *= perDay
	m.Now = -1
	if current.Valid {
		m.Now = current.Float64 * perDay
	}
	m.DueAt = due.Int64
	return &m, nil
}

// What each event says in one line, above the thing it is about.
var headlines = map[string]string{
	"mission_claimed":   "took a mission",
	"mission_shipped":   "Fix online, being verified",
	"mission_completed": "Debt repaid",
	"mission_confirmed": "Fix confirmed, it held",
	"mission_relapsed":  "It came back",
	"boss":              "A regression appeared",
	"boss_defeated":     "Regression defeated",
	"boss_escaped":      "A regression escaped",
	"boss_deadline":     "A regression escapes soon",
	"pull_request":      "A pull request for a mission",
	"test":              "Slowpoke is connected",
}

var cardColors = map[string]string{
	"mission_completed": "#7ee0a1", "mission_confirmed": "#7ee0a1", "boss_defeated": "#7ee0a1", "deploy_better": "#7ee0a1",
	"level_up": "#7ee0a1", "badge": "#7ee0a1", "debt_goal": "#7ee0a1", "leaderboard": "#7ee0a1",
	"boss": "#ff7a7a", "boss_escaped": "#ff7a7a", "boss_deadline": "#ff7a7a", "deploy_worse": "#ff7a7a", "mission_relapsed": "#ff7a7a",
	"mission_claimed": "#8ab4ff", "mission_shipped": "#8ab4ff", "pull_request": "#8ab4ff",
}

// slackEscape keeps a label from being read as a link or a mention: SQL has < and >.
func slackEscape(s string) string {
	return strings.NewReplacer("&", "&amp;", "<", "&lt;", ">", "&gt;").Replace(s)
}

func (n *Notifier) slackCard(m Message, text string, custom bool) map[string]any {
	color := cardColors[m.Event]
	if color == "" {
		color = "#ff6f8e"
	}
	who := m.Mention
	if who == "" && m.Person != "" {
		who = slackEscape(m.Person)
	}

	// The main line: our headline and the thing it is about, or the team's own template.
	main := text
	if h, ok := headlines[m.Event]; ok && !custom {
		if m.Event == "mission_claimed" && who != "" {
			h = who + " " + h
		} else if m.Event == "mission_claimed" {
			h = "Someone " + h
		}
		main = fmt.Sprintf("*%s %s*", m.Icon, h)
		switch {
		case m.Mission != nil:
			main += "\n`" + slackEscape(truncate(m.Mission.Label, 150)) + "`"
		case m.Event != "test":
			main += "\n" + slackEscape(m.Title)
		default:
			main += "\nMissions, bosses and badges of *" + slackEscape(m.Project) + "* will show up here."
		}
	} else if !custom && m.Event != "weekly" {
		main = fmt.Sprintf("*%s %s*", m.Icon, slackEscape(m.Title))
	}
	section := map[string]any{"type": "section", "text": map[string]any{"type": "mrkdwn", "text": main}}
	if m.URL != "" {
		section["accessory"] = map[string]any{"type": "button", "text": map[string]any{"type": "plain_text", "text": "Open in Slowpoke", "emoji": true}, "url": m.URL}
	}
	blocks := []any{section}

	var fields []any
	field := func(name, value string) { fields = append(fields, map[string]any{"type": "mrkdwn", "text": "*" + name + "*\n" + value}) }
	if mi := m.Mission; mi != nil && !custom {
		if mi.Now >= 0 && HumanDuration(mi.Now) != HumanDuration(mi.Before) {
			field("Before", HumanDuration(mi.Before)+" of waiting a day")
			field("Now", HumanDuration(mi.Now)+" a day")
		} else if mi.Before > 0 {
			field("Costs", HumanDuration(mi.Before)+" of waiting a day")
		}
		if mi.Type == "boss" && mi.DueAt > 0 && (m.Event == "boss" || m.Event == "boss_deadline") {
			fallback := time.Unix(mi.DueAt, 0).UTC().Format("Jan 2 15:04 UTC")
			field("Escapes", fmt.Sprintf("<!date^%d^{date_short_pretty} at {time}|%s>", mi.DueAt, fallback))
		}
	} else if m.Saved != "" && !custom {
		field("Saved", m.Saved)
	}
	if !custom {
		if m.Points > 0 {
			field("Points", fmt.Sprintf("+%d", m.Points))
		}
		if who != "" && m.Event != "mission_claimed" {
			field("Who", who)
		}
	}
	if len(fields) > 0 {
		blocks = append(blocks, map[string]any{"type": "section", "fields": fields})
	}

	footer := []any{}
	if n.PublicURL != "" {
		footer = append(footer, map[string]any{"type": "image", "image_url": n.PublicURL + "/slowpoke-logo.png", "alt_text": "Slowpoke"})
	}
	footer = append(footer, map[string]any{"type": "mrkdwn", "text": "*Slowpoke* · " + slackEscape(m.Project)})
	blocks = append(blocks, map[string]any{"type": "context", "elements": footer})

	return map[string]any{
		"text":        text,
		"attachments": []any{map[string]any{"color": color, "blocks": blocks}},
	}
}

func truncate(s string, n int) string {
	if r := []rune(s); len(r) > n {
		return string(r[:n]) + "…"
	}
	return s
}
