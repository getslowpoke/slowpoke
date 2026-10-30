package game

import (
	"strconv"
	"strings"
	"testing"
	"time"
)

func (f *fixture) boss(label string, due time.Time) int64 {
	f.t.Helper()
	id, err := f.db.Q().InsertID(f.ctx, `INSERT INTO missions (project_id, type, kind, target, label, baseline, points, status, person, opened_at, due_at, checked_at)
		VALUES (?, 'boss', 'endpoint', ?, ?, 120, 25, 'open', 'mario@acme.example', ?, ?, ?)`, f.project.ID, label, label, f.now.Unix(), due.Unix(), f.now.Unix())
	if err != nil {
		f.t.Fatal(err)
	}
	return id
}

// A boss that escapes in silence is a missed chance: two hours before its deadline the team hears
// about it once, while there is still time to ship a fix.
func TestABossAboutToEscapeIsAnnouncedOnce(t *testing.T) {
	f := newFixture(t, nil)
	f.now = time.Date(2026, 9, 26, 10, 0, 0, 0, time.UTC)
	soon := f.boss("GET /contract/{id}/pdf", f.now.Add(90*time.Minute))
	f.boss("GET /later", f.now.Add(5*time.Hour))

	f.evaluate(f.now)
	f.evaluate(f.now.Add(10 * time.Minute))
	if n := f.count("SELECT COUNT(*) FROM activities WHERE type = 'boss_deadline'"); n != 1 {
		t.Fatalf("want one reminder, got %d", n)
	}
	var title, data string
	_ = f.db.Q().Row(f.ctx, "SELECT title, data FROM activities WHERE type = 'boss_deadline'").Scan(&title, &data)
	if title != "Escapes in 2 hours: GET /contract/{id}/pdf" {
		t.Errorf("title %q", title)
	}
	if want := `"mission_id":` + strconv.FormatInt(soon, 10); !strings.Contains(data, want) {
		t.Errorf("data %s misses %s", data, want)
	}
}

// Passing a colleague in the month's points is the moment worth a message; gaining points while
// nobody is passed is not.
func TestOvertakingSomeoneInTheLeaderboardIsAnnounced(t *testing.T) {
	f := newFixture(t, nil)
	f.now = time.Date(2026, 9, 26, 10, 0, 0, 0, time.UTC)
	award := func(points int, person string) {
		t.Helper()
		if err := f.engine.award(f.ctx, f.db.Q(), f.project.ID, points, "test", person, "mission", 1); err != nil {
			t.Fatal(err)
		}
	}
	award(325, "luca@acme.example")
	award(300, "mario@acme.example")
	award(40, "mario@acme.example") // 340 against 325: passed
	award(10, "mario@acme.example") // nobody left to pass
	award(50, "")                  // points without a person rank nobody

	if n := f.count("SELECT COUNT(*) FROM activities WHERE type = 'leaderboard'"); n != 1 {
		t.Fatalf("want one overtake, got %d", n)
	}
	var title string
	_ = f.db.Q().Row(f.ctx, "SELECT title FROM activities WHERE type = 'leaderboard'").Scan(&title)
	if title != "mario overtakes luca: 340 to 325 points this month" {
		t.Errorf("title %q", title)
	}
}
