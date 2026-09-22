package game

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/getslowpoke/slowpoke/internal/protocol"
	"github.com/getslowpoke/slowpoke/internal/server/project"
)

var rome, _ = time.LoadLocation("Europe/Rome")

func TestScoreDaysFollowTheProjectTimezoneAndClose(t *testing.T) {
	f := newFixture(t, nil)
	yesterday := time.Date(2026, 9, 15, 0, 0, 0, 0, rome)
	// A full healthy day: 24 hours of fast traffic.
	f.traffic(yesterday, 24*60, 100, 1, 0, 0)
	// Today: two slow hours (HTTP components lost, PHP and database still clean).
	today := time.Date(2026, 9, 16, 0, 0, 0, 0, rome)
	f.traffic(today, 120, 100, 12, 50, 5)

	f.evaluate(today.Add(2*time.Hour + 30*time.Minute))

	type day struct {
		score           float64
		partial, closed bool
	}
	read := func(d string) day {
		var x day
		if err := f.db.Q().Row(f.ctx, "SELECT score, partial, closed FROM score_days WHERE project_id = ? AND day = ?", f.project.ID, d).Scan(&x.score, &x.partial, &x.closed); err != nil {
			t.Fatalf("%s: %v", d, err)
		}
		return x
	}
	y := read("2026-09-15")
	if !y.closed || y.score < 99 {
		t.Fatalf("yesterday %+v", y)
	}
	td := read("2026-09-16")
	if td.closed || !td.partial || td.score >= 50 {
		t.Fatalf("today %+v", td)
	}
}

func TestBadgesAndLevelUpAreAwardedOnce(t *testing.T) {
	f := newFixture(t, nil)
	day := time.Date(2026, 9, 15, 0, 0, 0, 0, rome)
	f.traffic(day, 24*60, 100, 1, 0, 0)

	f.evaluate(day.Add(25 * time.Hour))
	f.evaluate(day.Add(26 * time.Hour))

	for _, badge := range []string{"start", "zero_500", "half_second", "level_platinum"} {
		if f.count("SELECT COUNT(*) FROM unlocked_badges WHERE project_id = ? AND badge = ?", f.project.ID, badge) != 1 {
			t.Errorf("badge %s missing", badge)
		}
	}
	if n := f.count("SELECT COUNT(*) FROM activities WHERE project_id = ? AND type = 'level_up'", f.project.ID); n != 1 {
		t.Fatalf("level up activities %d", n)
	}
	if f.points() <= 0 {
		t.Fatal("badges must give points")
	}
	before := f.points()
	f.evaluate(day.Add(27 * time.Hour))
	if f.points() != before {
		t.Fatal("evaluating again must not award twice")
	}
}

func hourly(mutate func(*project.Settings)) func(*project.Settings) {
	return func(s *project.Settings) {
		s.Missions.WindowHours = 1
		s.Missions.GoodWindows = 2
		s.Missions.MinSeconds = 60
		s.Bosses.MinSeconds = 60
		if mutate != nil {
			mutate(s)
		}
	}
}

const slowSQL = "select * from orders where note like '%gift%'"

func TestMissionOpensFromTheCostliestQueryAndCompletes(t *testing.T) {
	f := newFixture(t, hourly(nil))
	h := time.Date(2026, 9, 16, 10, 0, 0, 0, time.UTC)

	f.query(h.Add(10*time.Minute), slowSQL, 300)
	f.query(h.Add(20*time.Minute), "select 1 from dual where a = 1", 5) // too cheap
	f.evaluate(h.Add(61 * time.Minute))

	var id int64
	var baseline float64
	var points int
	var kind, status string
	if err := f.db.Q().Row(f.ctx, "SELECT id, kind, baseline, points, status FROM missions WHERE project_id = ? AND type = 'mission'", f.project.ID).Scan(&id, &kind, &baseline, &points, &status); err != nil {
		t.Fatal(err)
	}
	if kind != "query" || baseline != 300 || status != "open" || points < 10 {
		t.Fatalf("mission kind=%s baseline=%v points=%d status=%s", kind, baseline, points, status)
	}
	if f.count("SELECT COUNT(*) FROM missions WHERE project_id = ?", f.project.ID) != 1 {
		t.Fatal("only one mission expected")
	}

	// A deploy by Ada, then two windows under 50% of the baseline.
	f.db.Q().Exec(f.ctx, "INSERT INTO deploys (project_id, version, author, deployed_at) VALUES (?, 'v2', 'ada', ?)", f.project.ID, h.Add(65*time.Minute).Unix())
	f.query(h.Add(70*time.Minute), slowSQL, 20)
	f.evaluate(h.Add(121 * time.Minute))
	f.query(h.Add(130*time.Minute), slowSQL, 10)
	f.evaluate(h.Add(181 * time.Minute))

	var person string
	if err := f.db.Q().Row(f.ctx, "SELECT status, person FROM missions WHERE id = ?", id).Scan(&status, &person); err != nil {
		t.Fatal(err)
	}
	if status != "completed" || person != "ada" {
		t.Fatalf("status=%s person=%s", status, person)
	}
	if f.count("SELECT COUNT(*) FROM point_entries WHERE subject_type = 'mission' AND subject_id = ? AND person = 'ada' AND points = ?", id, points) != 1 {
		t.Fatal("points not credited to the deploy author")
	}
	if f.count("SELECT COUNT(*) FROM unlocked_badges WHERE badge = 'first_mission'") != 1 {
		t.Fatal("first mission badge")
	}
}

func TestMissionProgressResetsWhenTheCostComesBack(t *testing.T) {
	f := newFixture(t, hourly(func(s *project.Settings) { s.Missions.GoodWindows = 2 }))
	h := time.Date(2026, 9, 16, 10, 0, 0, 0, time.UTC)
	f.query(h.Add(time.Minute), slowSQL, 300)
	f.evaluate(h.Add(61 * time.Minute))
	f.query(h.Add(62*time.Minute), slowSQL, 10)
	f.evaluate(h.Add(121 * time.Minute))
	f.query(h.Add(122*time.Minute), slowSQL, 290)
	f.evaluate(h.Add(181 * time.Minute))

	var good int
	var status string
	_ = f.db.Q().Row(f.ctx, "SELECT good_windows, status FROM missions WHERE project_id = ?", f.project.ID).Scan(&good, &status)
	if good != 0 || status != "open" {
		t.Fatalf("good=%d status=%s", good, status)
	}
}

func TestMissionExpires(t *testing.T) {
	f := newFixture(t, hourly(func(s *project.Settings) { s.Missions.ExpireDays = 1 }))
	h := time.Date(2026, 9, 16, 10, 0, 0, 0, time.UTC)
	f.query(h.Add(time.Minute), slowSQL, 300)
	f.evaluate(h.Add(61 * time.Minute))
	f.evaluate(h.Add(27 * time.Hour))
	if f.count("SELECT COUNT(*) FROM missions WHERE status = 'expired'") != 1 {
		t.Fatal("mission should expire")
	}
}

func TestBossAppearsAfterARegressionAndIsDefeated(t *testing.T) {
	f := newFixture(t, hourly(func(s *project.Settings) { s.Missions.MaxOpen = 0 }))
	h := time.Date(2026, 9, 16, 0, 0, 0, 0, time.UTC)
	// Seven quiet hours with some traffic, the query costs 30 s per hour.
	for i := 0; i < 7; i++ {
		f.traffic(h.Add(time.Duration(i)*time.Hour), 1, 10, 1, 0, 0)
		f.query(h.Add(time.Duration(i)*time.Hour+5*time.Minute), slowSQL, 30)
		f.evaluate(h.Add(time.Duration(i+1)*time.Hour + time.Minute))
	}
	if f.count("SELECT COUNT(*) FROM missions WHERE type = 'boss'") != 0 {
		t.Fatal("no boss while stable")
	}

	// Deploy at 07:10 by Bob, the query explodes.
	f.db.Q().Exec(f.ctx, "INSERT INTO deploys (project_id, version, author, deployed_at) VALUES (?, 'v3', 'bob', ?)", f.project.ID, h.Add(7*time.Hour+10*time.Minute).Unix())
	f.query(h.Add(7*time.Hour+20*time.Minute), slowSQL, 400)
	f.evaluate(h.Add(8*time.Hour + time.Minute))

	var id, deployID int64
	var person string
	if err := f.db.Q().Row(f.ctx, "SELECT id, deploy_id, person FROM missions WHERE type = 'boss' AND status = 'open'", nil...).Scan(&id, &deployID, &person); err != nil {
		t.Fatalf("boss not created: %v", err)
	}
	if deployID == 0 || person != "bob" {
		t.Fatalf("boss must be linked to the deploy: %d %s", deployID, person)
	}

	f.query(h.Add(8*time.Hour+20*time.Minute), slowSQL, 35)
	f.evaluate(h.Add(9*time.Hour + time.Minute))
	if f.count("SELECT COUNT(*) FROM missions WHERE id = ? AND status = 'defeated'", id) != 1 {
		t.Fatal("boss should be defeated")
	}
	if f.count("SELECT COUNT(*) FROM unlocked_badges WHERE badge = 'boss_slayer'") != 1 {
		t.Fatal("boss slayer badge")
	}
}

func TestBossEscapesAfterTheDeadline(t *testing.T) {
	f := newFixture(t, hourly(func(s *project.Settings) { s.Missions.MaxOpen = 0; s.Bosses.DeadlineHours = 2 }))
	h := time.Date(2026, 9, 16, 0, 0, 0, 0, time.UTC)
	for i := 0; i < 5; i++ {
		f.traffic(h.Add(time.Duration(i)*time.Hour), 1, 10, 1, 0, 0)
		f.query(h.Add(time.Duration(i)*time.Hour+5*time.Minute), slowSQL, 20)
	}
	f.evaluate(h.Add(5*time.Hour + time.Minute))
	for i := 5; i < 9; i++ {
		f.query(h.Add(time.Duration(i)*time.Hour+5*time.Minute), slowSQL, 500)
		f.evaluate(h.Add(time.Duration(i+1)*time.Hour + time.Minute))
	}
	if f.count("SELECT COUNT(*) FROM missions WHERE type = 'boss' AND status = 'escaped'") != 1 {
		t.Fatal("boss should escape")
	}
	if f.count("SELECT COUNT(*) FROM missions WHERE type = 'boss'") != 1 {
		t.Fatal("an escaped boss must not respawn every hour")
	}
}

func TestDeployImpactComparesBeforeAndAfter(t *testing.T) {
	f := newFixture(t, func(s *project.Settings) { s.Deploys.CompareMinutes = 30 })
	d := time.Date(2026, 9, 16, 10, 0, 0, 0, time.UTC)
	f.traffic(d.Add(-30*time.Minute), 30, 100, 1, 0, 0)
	f.traffic(d, 30, 100, 12, 40, 3)
	f.db.Q().Exec(f.ctx, "INSERT INTO deploys (project_id, version, author, deployed_at) VALUES (?, 'v4', 'eve', ?)", f.project.ID, d.Unix())

	f.evaluate(d.Add(20 * time.Minute))
	if f.count("SELECT COUNT(*) FROM deploys WHERE impact IS NOT NULL") != 0 {
		t.Fatal("too early")
	}
	f.evaluate(d.Add(31 * time.Minute))

	var raw string
	_ = f.db.Q().Row(f.ctx, "SELECT impact FROM deploys").Scan(&raw)
	var impact Impact
	if err := json.Unmarshal([]byte(raw), &impact); err != nil {
		t.Fatalf("%v %q", err, raw)
	}
	if impact.Verdict != "worse" || impact.Delta == nil || *impact.Delta > -10 || impact.After.P95 <= impact.Before.P95 {
		t.Fatalf("impact %+v", impact)
	}
	if f.count("SELECT COUNT(*) FROM activities WHERE type = 'deploy'") != 1 {
		t.Fatal("deploy activity")
	}
}

func TestCommandOverlapOnTwoHosts(t *testing.T) {
	f := newFixture(t, nil)
	s := time.Date(2026, 9, 16, 3, 0, 0, 0, time.UTC)
	exit := 0
	f.send(s, protocol.Payload{Agent: protocol.AgentInfo{Host: "app", Kind: "laravel"}, Commands: []protocol.CommandRun{
		{Host: "old-server", Name: "scanner:retry", StartedAt: s.Unix(), FinishedAt: s.Add(10 * time.Minute).Unix(), ExitCode: &exit},
		{Host: "new-server", Name: "scanner:retry", StartedAt: s.Add(2 * time.Minute).Unix(), FinishedAt: s.Add(5 * time.Minute).Unix(), ExitCode: &exit},
		{Host: "new-server", Name: "reports:build", StartedAt: s.Unix(), FinishedAt: s.Add(time.Minute).Unix(), ExitCode: &exit},
	}})
	f.evaluate(s.Add(15 * time.Minute))
	f.evaluate(s.Add(16 * time.Minute))
	if n := f.count("SELECT COUNT(*) FROM activities WHERE type = 'command_overlap'"); n != 1 {
		t.Fatalf("overlaps %d", n)
	}
}

func TestDebtSummary(t *testing.T) {
	f := newFixture(t, nil)
	now := time.Date(2026, 9, 16, 12, 0, 0, 0, time.UTC)
	f.query(now.Add(-2*time.Hour), slowSQL, 600)
	f.query(now.Add(-26*time.Hour), slowSQL, 1200)
	f.now = now
	debt, err := f.engine.Debt(f.ctx, f.project)
	if err != nil {
		t.Fatal(err)
	}
	if debt.SecondsPerDay != 600 || debt.PreviousSecondsPerDay != 1200 || len(debt.Items) != 1 || debt.Items[0].Kind != "query" {
		t.Fatalf("%+v", debt)
	}
}

func TestWeeklyDebtBudgetIsCelebratedOnce(t *testing.T) {
	f := newFixture(t, func(s *project.Settings) { s.Debt.WeeklyGoalSeconds = 300 })
	now := time.Date(2026, 9, 17, 12, 0, 0, 0, time.UTC)
	f.db.Q().Exec(f.ctx, `INSERT INTO missions (project_id, type, kind, target, label, baseline, current, points, status, good_windows, person, opened_at, checked_at, closed_at)
		VALUES (?, 'mission', 'query', 'x', 'x', 500, 100, 10, 'completed', 2, '', ?, ?, ?)`, f.project.ID, now.Add(-72*time.Hour).Unix(), now.Unix(), now.Add(-time.Hour).Unix())
	f.evaluate(now)
	f.evaluate(now.Add(time.Hour))
	if n := f.count("SELECT COUNT(*) FROM activities WHERE project_id = ? AND type = 'debt_goal'", f.project.ID); n != 1 {
		t.Fatalf("goal activities %d", n)
	}
	if f.count("SELECT COUNT(*) FROM unlocked_badges WHERE badge = 'budget_kept'") != 1 {
		t.Fatal("budget kept badge")
	}
}

// Found on the demo, and true of any project in its first week: with 4 windows of history out of
// the 7 the detector looks back on, the average was divided by 7, came out at 4/7 of the truth,
// and anything 15% busier than usual became a "regression". The average is over the windows
// that were measured.
func TestAYoungProjectDoesNotSeeRegressionsEverywhere(t *testing.T) {
	f := newFixture(t, hourly(func(s *project.Settings) { s.Missions.MaxOpen = 0 }))
	h := time.Date(2026, 9, 16, 10, 0, 0, 0, time.UTC)
	for i := 0; i < 5; i++ {
		f.query(h.Add(time.Duration(i)*time.Hour+10*time.Minute), slowSQL, 200)
		f.evaluate(h.Add(time.Duration(i+1)*time.Hour + time.Minute))
	}
	// A 25% busier hour is not a regression.
	f.query(h.Add(5*time.Hour+10*time.Minute), slowSQL, 250)
	f.evaluate(h.Add(6*time.Hour + time.Minute))
	if n := f.count("SELECT COUNT(*) FROM missions WHERE type = 'boss'"); n != 0 {
		t.Fatalf("%d bosses from a 25%% busier hour", n)
	}
	// Doubling is.
	f.query(h.Add(6*time.Hour+10*time.Minute), slowSQL, 520)
	f.evaluate(h.Add(7*time.Hour + time.Minute))
	if n := f.count("SELECT COUNT(*) FROM missions WHERE type = 'boss'"); n != 1 {
		t.Fatalf("%d bosses after the cost doubled", n)
	}
}
