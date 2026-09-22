package game

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"strings"
	"time"

	"github.com/getslowpoke/slowpoke/internal/server/project"
	"github.com/getslowpoke/slowpoke/internal/server/store"
)

// The life of a mission between "opened" and "done for good": somebody takes it, the fix goes
// online, the numbers confirm it, and for a while after that Slowpoke keeps looking. Without
// it two people fix the same thing, and a fix that came back a week later is still counted as
// repaid debt.

// watchWindows is how many windows a completed mission stays under watch: 30 days with the
// default daily window. Holding for all of them confirms it.
const watchWindows = 30

var (
	ErrClosed   = errors.New("the mission is closed")
	ErrNotFound = errors.New("mission not found")
)

// Taken is the answer to somebody taking a mission that somebody else already took.
type Taken struct {
	By    string
	Since int64
}

func (t *Taken) Error() string {
	return fmt.Sprintf("already taken by %s since %s", t.By, time.Unix(t.Since, 0).UTC().Format("2006-01-02 15:04"))
}

// samePerson compares how a person is written in a claim and in a deploy: "Ada@Example.com",
// "ada@example.com" and "ada" (the part before the @) are the same person.
func samePerson(a, b string) bool {
	norm := func(s string) string { return strings.ToLower(strings.TrimSpace(s)) }
	a, b = norm(a), norm(b)
	if a == "" || b == "" {
		return false
	}
	if a == b {
		return true
	}
	local := func(s string) string {
		if i := strings.Index(s, "@"); i > 0 {
			return s[:i]
		}
		return s
	}
	return local(a) == local(b)
}

func addActivity(ctx context.Context, q store.Q, projectID int64, kind, icon, title string, data map[string]any, now time.Time) error {
	raw, _ := json.Marshal(data)
	_, err := q.Exec(ctx, `INSERT INTO activities (project_id, type, icon, title, data, notified, created_at) VALUES (?, ?, ?, ?, ?, FALSE, ?)`,
		projectID, kind, icon, title, string(raw), now.Unix())
	return err
}

// Claim gives a mission to a person. Somebody else already on it is an error that says who and
// since when, unless force: taking over is allowed, silently doubling the work is not.
func Claim(ctx context.Context, db *store.DB, projectID, id int64, person string, force bool, now time.Time) error {
	person = strings.TrimSpace(person)
	if person == "" {
		return errors.New("person required")
	}
	if len(person) > 100 {
		person = person[:100]
	}
	return db.Tx(ctx, func(q store.Q) error {
		var status, label, current string
		var since sql.NullInt64
		err := q.Row(ctx, `SELECT status, label, person, claimed_at FROM missions WHERE id = ? AND project_id = ?`, id, projectID).Scan(&status, &label, &current, &since)
		if errors.Is(err, sql.ErrNoRows) {
			return ErrNotFound
		}
		if err != nil {
			return err
		}
		if status != "open" {
			return ErrClosed
		}
		if since.Valid && current != "" {
			if samePerson(current, person) {
				return nil
			}
			if !force {
				return &Taken{By: current, Since: since.Int64}
			}
		}
		if _, err := q.Exec(ctx, `UPDATE missions SET person = ?, claimed_at = ? WHERE id = ?`, person, now.Unix(), id); err != nil {
			return err
		}
		return addActivity(ctx, q, projectID, "mission_claimed", "🙋", fmt.Sprintf("%s took: %s", person, label),
			map[string]any{"mission_id": id, "person": person, "from": current}, now)
	})
}

// Release puts a mission back among the ones nobody is on.
func Release(ctx context.Context, db *store.DB, projectID, id int64) error {
	res, err := db.Q().Exec(ctx, `UPDATE missions SET person = '', claimed_at = NULL, shipped_at = NULL, shipped_deploy_id = NULL
		WHERE id = ? AND project_id = ? AND status = 'open'`, id, projectID)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrNotFound
	}
	return nil
}

// MarkShipped says by hand that the fix is online, for a team without a deploy that tells
// Slowpoke. The latest deploy, if there is one, is taken as the one that carried it.
func MarkShipped(ctx context.Context, db *store.DB, projectID, id int64, person string, now time.Time) error {
	return db.Tx(ctx, func(q store.Q) error {
		var status, label, owner string
		err := q.Row(ctx, `SELECT status, label, person FROM missions WHERE id = ? AND project_id = ?`, id, projectID).Scan(&status, &label, &owner)
		if errors.Is(err, sql.ErrNoRows) {
			return ErrNotFound
		}
		if err != nil {
			return err
		}
		if status != "open" {
			return ErrClosed
		}
		var deployID sql.NullInt64
		if err := q.Row(ctx, `SELECT id FROM deploys WHERE project_id = ? AND deployed_at <= ? ORDER BY deployed_at DESC LIMIT 1`, projectID, now.Unix()).Scan(&deployID); err != nil && !errors.Is(err, sql.ErrNoRows) {
			return err
		}
		if strings.TrimSpace(owner) == "" && strings.TrimSpace(person) != "" {
			if _, err := q.Exec(ctx, `UPDATE missions SET person = ?, claimed_at = ? WHERE id = ?`, strings.TrimSpace(person), now.Unix(), id); err != nil {
				return err
			}
			owner = person
		}
		if _, err := q.Exec(ctx, `UPDATE missions SET shipped_at = ?, shipped_deploy_id = ? WHERE id = ?`, now.Unix(), deployID, id); err != nil {
			return err
		}
		return addActivity(ctx, q, projectID, "mission_shipped", "🚀", "Online, being verified: "+label,
			map[string]any{"mission_id": id, "person": owner, "deploy_id": deployID.Int64}, now)
	})
}

// shipByDeploy puts online the missions whose owner deployed after taking them: that deploy
// carried the fix. Another person's deploy is not taken as the fix.
func (e *Engine) shipByDeploy(ctx context.Context, p project.Project) error {
	rows, err := e.DB.Q().Query(ctx, `SELECT id, label, person, claimed_at FROM missions
		WHERE project_id = ? AND status = 'open' AND claimed_at IS NOT NULL AND shipped_at IS NULL AND person <> ''`, p.ID)
	if err != nil {
		return err
	}
	type claimed struct {
		id           int64
		label, owner string
		since        int64
	}
	var list []claimed
	for rows.Next() {
		var c claimed
		if err := rows.Scan(&c.id, &c.label, &c.owner, &c.since); err != nil {
			rows.Close()
			return err
		}
		list = append(list, c)
	}
	rows.Close()
	for _, c := range list {
		drows, err := e.DB.Q().Query(ctx, `SELECT id, author, deployed_at FROM deploys WHERE project_id = ? AND deployed_at >= ? AND deployed_at <= ? ORDER BY deployed_at`,
			p.ID, c.since, e.Now().Unix())
		if err != nil {
			return err
		}
		var deployID, at int64
		for drows.Next() {
			var id, when int64
			var author string
			if err := drows.Scan(&id, &author, &when); err != nil {
				drows.Close()
				return err
			}
			if samePerson(author, c.owner) {
				deployID, at = id, when
				break
			}
		}
		drows.Close()
		if deployID == 0 {
			continue
		}
		err = e.DB.Tx(ctx, func(q store.Q) error {
			if _, err := q.Exec(ctx, `UPDATE missions SET shipped_at = ?, shipped_deploy_id = ? WHERE id = ?`, at, deployID, c.id); err != nil {
				return err
			}
			return addActivity(ctx, q, p.ID, "mission_shipped", "🚀", "Online, being verified: "+c.label,
				map[string]any{"mission_id": c.id, "person": c.owner, "deploy_id": deployID}, e.Now())
		})
		if err != nil {
			return err
		}
	}
	return nil
}

// watchCompleted keeps looking at completed missions: a cost that stays down for the whole watch
// confirms the fix; a cost that comes back over three quarters of where it started, two windows
// in a row, is a fix that did not hold.
func (e *Engine) watchCompleted(ctx context.Context, p project.Project, w time.Duration, end time.Time) error {
	rows, err := e.DB.Q().Query(ctx, `SELECT id, kind, target, label, baseline, COALESCE(current, 0), points, person, checked_at, held_windows, bad_windows, baseline_rate
		FROM missions WHERE project_id = ? AND type = 'mission' AND status = 'completed' AND verdict = '' AND closed_at >= ?`,
		p.ID, end.Add(-time.Duration(watchWindows+2)*w).Unix())
	if err != nil {
		return err
	}
	type watched struct {
		id                   int64
		kind, target, label  string
		baseline, current    float64
		points               int
		person               string
		checked              int64
		held, bad            int
		rate                 sql.NullFloat64
	}
	var list []watched
	for rows.Next() {
		var m watched
		if err := rows.Scan(&m.id, &m.kind, &m.target, &m.label, &m.baseline, &m.current, &m.points, &m.person, &m.checked, &m.held, &m.bad, &m.rate); err != nil {
			rows.Close()
			return err
		}
		list = append(list, m)
	}
	rows.Close()

	relapseAt := (1 + p.Settings.Missions.TargetRatio) / 2
	for _, m := range list {
		verdict := ""
		var cost float64
		checked := time.Unix(m.checked, 0)
		for ; !checked.Add(w).After(end); checked = checked.Add(w) {
			from, to := checked, checked.Add(w)
			// The same reading as the mission: per request on busy hours, a window nobody can
			// judge neither confirms nor reopens anything.
			j, err := e.judge(ctx, p, from, to)
			if err != nil {
				return err
			}
			if !j.ok {
				continue
			}
			costs, err := e.Metrics.Costs(ctx, p.ID, from, to)
			if err != nil {
				return err
			}
			cost = findCost(costs, m.kind, m.target) // seconds: what the boss that reopens it shows
			value, base := cost, m.baseline
			if j.perRequest && m.rate.Valid {
				value, base = j.value(m.kind, m.target), m.rate.Float64
			}
			if value > base*relapseAt {
				m.bad++
			} else {
				m.bad = 0
				m.held++
			}
			if m.bad >= 2 {
				verdict = "relapsed"
				checked = checked.Add(w)
				break
			}
			if m.held >= watchWindows {
				verdict = "confirmed"
				checked = checked.Add(w)
				break
			}
		}
		err := e.DB.Tx(ctx, func(q store.Q) error {
			if _, err := q.Exec(ctx, `UPDATE missions SET checked_at = ?, held_windows = ?, bad_windows = ? WHERE id = ?`, checked.Unix(), m.held, m.bad, m.id); err != nil {
				return err
			}
			switch verdict {
			case "confirmed":
				if _, err := q.Exec(ctx, `UPDATE missions SET verdict = 'confirmed', verdict_at = ? WHERE id = ?`, checked.Unix(), m.id); err != nil {
					return err
				}
				bonus := max(5, m.points/4)
				if err := e.award(ctx, q, p.ID, bonus, "Fix confirmed: "+m.label, m.person, "mission", m.id); err != nil {
					return err
				}
				return e.activity(ctx, q, p.ID, "mission_confirmed", "🏅", "Fix confirmed: "+m.label,
					map[string]any{"mission_id": m.id, "person": m.person, "points": bonus, "held_windows": m.held})
			case "relapsed":
				return e.relapse(ctx, q, p, w, checked, m.id, m.kind, m.target, m.label, m.current, cost, m.person)
			}
			return nil
		})
		if err != nil {
			return err
		}
	}
	return nil
}

// relapse marks a completed mission as came back and opens the boss that says so, or links the
// one the regression detector already opened on the same target.
func (e *Engine) relapse(ctx context.Context, q store.Q, p project.Project, w time.Duration, end time.Time, missionID int64, kind, target, label string, reached, cost float64, fixer string) error {
	if _, err := q.Exec(ctx, `UPDATE missions SET verdict = 'relapsed', verdict_at = ? WHERE id = ?`, end.Unix(), missionID); err != nil {
		return err
	}
	var bossID int64
	err := q.Row(ctx, `SELECT id FROM missions WHERE project_id = ? AND type = 'boss' AND status = 'open' AND kind = ? AND target = ? ORDER BY id DESC LIMIT 1`,
		p.ID, kind, target).Scan(&bossID)
	switch {
	case err == nil:
		if _, err := q.Exec(ctx, `UPDATE missions SET relapse_of = ? WHERE id = ?`, missionID, bossID); err != nil {
			return err
		}
	case errors.Is(err, sql.ErrNoRows):
		var deployID sql.NullInt64
		var author string
		if err := q.Row(ctx, `SELECT id, author FROM deploys WHERE project_id = ? AND deployed_at >= ? AND deployed_at < ? ORDER BY deployed_at DESC LIMIT 1`,
			p.ID, end.Add(-2*w).Unix(), end.Unix()).Scan(&deployID, &author); err != nil && !errors.Is(err, sql.ErrNoRows) {
			return err
		}
		points := clamp(int(math.Round(perDay(cost, w)/30)), 25, 1000)
		due := e.Now().Add(time.Duration(p.Settings.Bosses.DeadlineHours * float64(time.Hour)))
		// The boss starts from the level the fix had reached, per request when it was measured so.
		bossID, err = q.InsertID(ctx, `INSERT INTO missions (project_id, type, kind, target, label, baseline, current, points, status, good_windows, deploy_id, person, opened_at, due_at, checked_at, relapse_of, baseline_rate)
			VALUES (?, 'boss', ?, ?, ?, ?, ?, ?, 'open', 0, ?, ?, ?, ?, ?, ?, (SELECT current_rate FROM missions WHERE id = ?))`,
			p.ID, kind, target, label, reached, cost, points, deployID, author, e.Now().Unix(), due.Unix(), end.Unix(), missionID, missionID)
		if err != nil {
			return err
		}
	default:
		return err
	}
	return e.activity(ctx, q, p.ID, "mission_relapsed", "🔁", "Came back: "+label,
		map[string]any{"mission_id": missionID, "boss_id": bossID, "person": fixer, "seconds_per_day": math.Round(perDay(cost, w))})
}

// linkFixingDeploy records the deploy that most likely fixed a mission: the latest one between
// the window before the cost went down and the end of the window it went down in. Nothing is
// recorded when there was no deploy: the cost went down for another reason.
func (e *Engine) linkFixingDeploy(ctx context.Context, p project.Project, m *mission, from, to time.Time) error {
	var deployID, at int64
	var version, author string
	err := e.DB.Q().Row(ctx, `SELECT id, deployed_at, COALESCE(NULLIF(version, ''), commit_sha), author FROM deploys WHERE project_id = ? AND deployed_at >= ? AND deployed_at < ?
		ORDER BY deployed_at DESC LIMIT 1`, p.ID, from.Unix(), to.Unix()).Scan(&deployID, &at, &version, &author)
	if errors.Is(err, sql.ErrNoRows) {
		return nil
	}
	if err != nil {
		return err
	}
	m.Shipped = sql.NullInt64{Int64: deployID, Valid: true}
	return e.DB.Tx(ctx, func(q store.Q) error {
		if _, err := q.Exec(ctx, `UPDATE missions SET shipped_deploy_id = ?, shipped_at = COALESCE(shipped_at, ?) WHERE id = ?`, deployID, at, m.ID); err != nil {
			return err
		}
		return e.activity(ctx, q, p.ID, "mission_shipped", "🚀", fmt.Sprintf("Going down after %s: %s", version, m.Label),
			map[string]any{"mission_id": m.ID, "deploy_id": deployID, "person": author, "probable": true})
	})
}

// maxQuietDays is the longest a dismissed regression stays quiet: past that the team should be
// told again, because a slow endpoint nobody hears about for a year is a lie.
const maxQuietDays = 90

// Dismiss waves a regression away without taking its target out of the score: the boss closes
// with no points and the same target opens nothing for the days asked for. For something that is
// slow by design and always will be, the answer is an exclusion, not this.
func Dismiss(ctx context.Context, db *store.DB, projectID, id int64, note string, days int, now time.Time) error {
	if days <= 0 {
		days = 14
	}
	if days > maxQuietDays {
		days = maxQuietDays
	}
	if len(note) > 500 {
		note = note[:500]
	}
	return db.Tx(ctx, func(q store.Q) error {
		var status, label, kind string
		err := q.Row(ctx, `SELECT status, label, kind FROM missions WHERE id = ? AND project_id = ?`, id, projectID).Scan(&status, &label, &kind)
		if errors.Is(err, sql.ErrNoRows) {
			return ErrNotFound
		}
		if err != nil {
			return err
		}
		if status != "open" {
			return ErrClosed
		}
		quiet := now.Add(time.Duration(days) * 24 * time.Hour).Unix()
		if _, err := q.Exec(ctx, `UPDATE missions SET status = 'dismissed', closed_at = ?, quiet_until = ? WHERE id = ?`, now.Unix(), quiet, id); err != nil {
			return err
		}
		return addActivity(ctx, q, projectID, "mission_dismissed", "🤫", fmt.Sprintf("Waved away: %s", label),
			map[string]any{"mission_id": id, "kind": kind, "note": note, "quiet_until": quiet, "days": days}, now)
	})
}
