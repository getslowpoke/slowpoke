package game

import (
	"errors"
	"testing"
	"time"

	"github.com/getslowpoke/slowpoke/internal/server/project"
)

// openMission creates one mission from a costly query and returns its id.
func openMission(f *fixture, h time.Time) int64 {
	f.t.Helper()
	f.query(h.Add(10*time.Minute), slowSQL, 300)
	f.evaluate(h.Add(61 * time.Minute))
	var id int64
	if err := f.db.Q().Row(f.ctx, "SELECT id FROM missions WHERE project_id = ? AND type = 'mission'", f.project.ID).Scan(&id); err != nil {
		f.t.Fatal(err)
	}
	return id
}

// Two people must not fix the same thing: a mission is taken by one person at a time, and whoever
// tries to take it again is told by whom and since when.
func TestAMissionIsTakenByOnePersonAtATime(t *testing.T) {
	f := newFixture(t, hourly(nil))
	h := time.Date(2026, 9, 16, 10, 0, 0, 0, time.UTC)
	id := openMission(f, h)
	now := h.Add(70 * time.Minute)

	if err := Claim(f.ctx, f.db, f.project.ID, id, "ada@example.com", false, now); err != nil {
		t.Fatal(err)
	}
	// Taking it again yourself is not a conflict.
	if err := Claim(f.ctx, f.db, f.project.ID, id, "Ada@Example.com", false, now.Add(time.Minute)); err != nil {
		t.Fatalf("the same person: %v", err)
	}
	err := Claim(f.ctx, f.db, f.project.ID, id, "linus@example.com", false, now.Add(time.Hour))
	var taken *Taken
	if !errors.As(err, &taken) || taken.By != "ada@example.com" || taken.Since != now.Unix() {
		t.Fatalf("expected taken by ada since %d, got %v", now.Unix(), err)
	}
	if err := Claim(f.ctx, f.db, f.project.ID, id, "linus@example.com", true, now.Add(time.Hour)); err != nil {
		t.Fatalf("forced: %v", err)
	}
	var person string
	_ = f.db.Q().Row(f.ctx, "SELECT person FROM missions WHERE id = ?", id).Scan(&person)
	if person != "linus@example.com" {
		t.Fatalf("person %q", person)
	}
	if f.count("SELECT COUNT(*) FROM activities WHERE type = 'mission_claimed'") != 2 {
		t.Fatal("each change of hands is in the activity")
	}

	if err := Release(f.ctx, f.db, f.project.ID, id); err != nil {
		t.Fatal(err)
	}
	if f.count("SELECT COUNT(*) FROM missions WHERE id = ? AND person = '' AND claimed_at IS NULL", id) != 1 {
		t.Fatal("released mission still has an owner")
	}
	_, _ = f.db.Q().Exec(f.ctx, "UPDATE missions SET status = 'completed' WHERE id = ?", id)
	if err := Claim(f.ctx, f.db, f.project.ID, id, "ada@example.com", false, now); !errors.Is(err, ErrClosed) {
		t.Fatalf("a closed mission cannot be taken: %v", err)
	}
}

// Whoever takes a mission deploys the fix: that deploy puts the mission online, and everybody
// can see it is being verified. A deploy by somebody else is not the fix.
func TestTheDeployOfWhoeverTookItPutsTheMissionOnline(t *testing.T) {
	f := newFixture(t, hourly(func(s *project.Settings) { s.Missions.GoodWindows = 3 }))
	h := time.Date(2026, 9, 16, 10, 0, 0, 0, time.UTC)
	id := openMission(f, h)
	if err := Claim(f.ctx, f.db, f.project.ID, id, "ada@example.com", false, h.Add(62*time.Minute)); err != nil {
		t.Fatal(err)
	}
	_, _ = f.db.Q().Exec(f.ctx, "INSERT INTO deploys (project_id, version, author, deployed_at) VALUES (?, 'v2', 'linus@example.com', ?)", f.project.ID, h.Add(64*time.Minute).Unix())
	f.query(h.Add(70*time.Minute), slowSQL, 300)
	f.evaluate(h.Add(121 * time.Minute))
	if f.count("SELECT COUNT(*) FROM missions WHERE id = ? AND shipped_at IS NULL", id) != 1 {
		t.Fatal("somebody else's deploy put it online")
	}
	_, _ = f.db.Q().Exec(f.ctx, "INSERT INTO deploys (project_id, version, author, deployed_at) VALUES (?, 'v3', 'Ada@Example.com', ?)", f.project.ID, h.Add(125*time.Minute).Unix())
	f.query(h.Add(130*time.Minute), slowSQL, 20)
	f.evaluate(h.Add(181 * time.Minute))
	var shippedAt int64
	var version string
	if err := f.db.Q().Row(f.ctx, `SELECT m.shipped_at, d.version FROM missions m JOIN deploys d ON d.id = m.shipped_deploy_id WHERE m.id = ?`, id).Scan(&shippedAt, &version); err != nil {
		t.Fatalf("not online: %v", err)
	}
	if shippedAt != h.Add(125*time.Minute).Unix() || version != "v3" {
		t.Fatalf("shipped at %d with %s", shippedAt, version)
	}
	if f.count("SELECT COUNT(*) FROM activities WHERE type = 'mission_shipped'") != 1 {
		t.Fatal("going online is in the activity")
	}

	// Without a deploy pipeline, it is said by hand.
	other := h.Add(200 * time.Minute)
	_, _ = f.db.Q().Exec(f.ctx, "UPDATE missions SET shipped_at = NULL, shipped_deploy_id = NULL WHERE id = ?", id)
	if err := MarkShipped(f.ctx, f.db, f.project.ID, id, "ada@example.com", other); err != nil {
		t.Fatal(err)
	}
	if f.count("SELECT COUNT(*) FROM missions WHERE id = ? AND shipped_at = ? AND shipped_deploy_id IS NOT NULL", id, other.Unix()) != 1 {
		t.Fatal("marked online by hand, linked to the latest deploy")
	}
}

// completeMission takes a mission to completed in two windows.
func completeMission(f *fixture, h time.Time) (int64, time.Time) {
	f.t.Helper()
	id := openMission(f, h)
	f.query(h.Add(70*time.Minute), slowSQL, 20)
	f.evaluate(h.Add(121 * time.Minute))
	var status string
	_ = f.db.Q().Row(f.ctx, "SELECT status FROM missions WHERE id = ?", id).Scan(&status)
	if status != "completed" {
		f.t.Fatalf("status %s", status)
	}
	return id, h.Add(2 * time.Hour)
}

// A completed mission is watched: when it holds for the whole watch it is confirmed, with a
// bonus to whoever fixed it.
func TestACompletedMissionIsConfirmedWhenItHolds(t *testing.T) {
	f := newFixture(t, hourly(func(s *project.Settings) { s.Missions.GoodWindows = 1 }))
	h := time.Date(2026, 9, 16, 10, 0, 0, 0, time.UTC)
	id, end := completeMission(f, h)
	_, _ = f.db.Q().Exec(f.ctx, "UPDATE missions SET person = 'ada' WHERE id = ?", id)
	before := f.points()
	for i := 0; i < watchWindows; i++ {
		f.query(end.Add(time.Duration(i)*time.Hour+10*time.Minute), slowSQL, 15)
	}
	f.evaluate(end.Add(time.Duration(watchWindows)*time.Hour + time.Minute))
	var verdict string
	var held int
	_ = f.db.Q().Row(f.ctx, "SELECT verdict, held_windows FROM missions WHERE id = ?", id).Scan(&verdict, &held)
	if verdict != "confirmed" || held < watchWindows {
		t.Fatalf("verdict %q held %d", verdict, held)
	}
	if f.points() <= before || f.count("SELECT COUNT(*) FROM activities WHERE type = 'mission_confirmed'") != 1 {
		t.Fatal("confirmation pays a bonus and says so")
	}
}

// A completed mission whose cost comes back is marked as came back, and the regression is a boss
// linked to it: somebody sees that the fix did not hold, and where it was fixed before.
func TestACompletedMissionThatComesBackIsReopenedAsABoss(t *testing.T) {
	f := newFixture(t, hourly(func(s *project.Settings) { s.Missions.GoodWindows = 1 }))
	h := time.Date(2026, 9, 16, 10, 0, 0, 0, time.UTC)
	id, end := completeMission(f, h)
	// It holds for a while, then the cost comes back for two windows.
	for i := 0; i < 5; i++ {
		f.query(end.Add(time.Duration(i)*time.Hour+10*time.Minute), slowSQL, 15)
	}
	back := end.Add(5 * time.Hour)
	f.query(back.Add(10*time.Minute), slowSQL, 280)
	f.query(back.Add(70*time.Minute), slowSQL, 290)
	f.evaluate(back.Add(121 * time.Minute))

	var verdict string
	_ = f.db.Q().Row(f.ctx, "SELECT verdict FROM missions WHERE id = ?", id).Scan(&verdict)
	if verdict != "relapsed" {
		t.Fatalf("verdict %q", verdict)
	}
	if f.count("SELECT COUNT(*) FROM missions WHERE type = 'boss' AND status = 'open' AND relapse_of = ?", id) != 1 {
		t.Fatal("the regression is a boss linked to the mission it undoes")
	}
	if f.count("SELECT COUNT(*) FROM missions WHERE type = 'boss'") != 1 {
		t.Fatal("one boss, not two")
	}
	if f.count("SELECT COUNT(*) FROM activities WHERE type = 'mission_relapsed'") != 1 {
		t.Fatal("coming back is in the activity")
	}
	repaid, err := f.engine.repaidPerDay(f.ctx, f.project)
	if err != nil || repaid != 0 {
		t.Fatalf("a fix that did not hold is not repaid debt: %v %v", repaid, err)
	}
}

// Nobody took the mission, somebody deployed, and right after that the cost went down: that
// deploy most likely carried the fix, and the deploy and the mission say so to each other.
func TestTheDeployRightBeforeTheCostWentDownIsTakenAsTheFix(t *testing.T) {
	f := newFixture(t, hourly(func(s *project.Settings) { s.Missions.GoodWindows = 3 }))
	h := time.Date(2026, 9, 16, 10, 0, 0, 0, time.UTC)
	id := openMission(f, h)
	// An old deploy long before, and the one that fixed it just before the cost went down.
	_, _ = f.db.Q().Exec(f.ctx, "INSERT INTO deploys (project_id, version, author, deployed_at) VALUES (?, 'v1', 'linus', ?)", f.project.ID, h.Add(-5*time.Hour).Unix())
	fix, _ := f.db.Q().InsertID(f.ctx, "INSERT INTO deploys (project_id, version, author, deployed_at) VALUES (?, 'v2', 'grace', ?)", f.project.ID, h.Add(65*time.Minute).Unix())
	f.query(h.Add(70*time.Minute), slowSQL, 20)
	f.evaluate(h.Add(121 * time.Minute))

	var shipped int64
	var status string
	if err := f.db.Q().Row(f.ctx, "SELECT shipped_deploy_id, status FROM missions WHERE id = ?", id).Scan(&shipped, &status); err != nil {
		t.Fatalf("no deploy linked: %v", err)
	}
	if shipped != fix || status != "open" {
		t.Fatalf("linked %d (want %d), status %s", shipped, fix, status)
	}
	lives, err := Lives(f.ctx, f.db, []int64{id})
	if err != nil || lives[id].Stage != "shipped" || lives[id].ShippedDeploy != "v2" {
		t.Fatalf("life %+v err %v", lives[id], err)
	}

	// Without a deploy, a cost that goes down is not attributed to anybody.
	g := newFixture(t, hourly(func(s *project.Settings) { s.Missions.GoodWindows = 3 }))
	other := openMission(g, h)
	g.query(h.Add(70*time.Minute), slowSQL, 20)
	g.evaluate(h.Add(121 * time.Minute))
	if g.count("SELECT COUNT(*) FROM missions WHERE id = ? AND shipped_deploy_id IS NULL", other) != 1 {
		t.Fatal("a deploy was invented")
	}
}
