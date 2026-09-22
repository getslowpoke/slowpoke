package game

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/getslowpoke/slowpoke/internal/protocol"
	"github.com/getslowpoke/slowpoke/internal/server/project"
)

// officeTraffic writes `days` days of traffic in the project's time zone: busy on weekdays from 9
// to 19, a trickle the rest of the time.
func officeTraffic(f *fixture, start time.Time, days int) {
	f.t.Helper()
	loc := f.project.Location()
	var buckets []protocol.HTTPBucket
	for d := 0; d < days; d++ {
		for h := 0; h < 24; h++ {
			t := time.Date(start.Year(), start.Month(), start.Day()+d, h, 0, 0, 0, loc)
			n := 40
			if wd := t.Weekday(); wd != time.Saturday && wd != time.Sunday && h >= 9 && h < 19 {
				n = 1000
			}
			hist := make([]int, 15)
			hist[2] = n
			buckets = append(buckets, protocol.HTTPBucket{Source: "nginx:app", Minute: t.Unix(), N: n, Sum: float64(n) * 0.1, Hist: hist})
		}
	}
	f.send(start.AddDate(0, 0, days), protocol.Payload{HTTP: buckets})
}

// The hours a project is judged on are the ones its people use it: found in its own traffic.
func TestBusyHoursAreFoundInTheTraffic(t *testing.T) {
	f := newFixture(t, nil)
	start := time.Date(2026, 8, 17, 0, 0, 0, 0, f.project.Location()) // a Monday
	officeTraffic(f, start, 28)
	now := start.AddDate(0, 0, 28)
	b, err := BusyHours(f.ctx, f.db, f.project, now)
	if err != nil {
		t.Fatal(err)
	}
	if !b.Known || b.Manual {
		t.Fatalf("known %v manual %v", b.Known, b.Manual)
	}
	tuesday10 := time.Date(2026, 9, 15, 10, 30, 0, 0, f.project.Location())
	if !b.At(tuesday10) || b.At(tuesday10.Add(10*time.Hour)) || b.At(time.Date(2026, 9, 19, 11, 0, 0, 0, f.project.Location())) {
		t.Fatal("busy hours are weekdays 9-19")
	}
	if got := b.Describe(); got != "Mon-Fri 9-19" {
		t.Fatalf("described as %q", got)
	}
	// A weekday window is judged on its ten busy hours, a Saturday on none.
	day := time.Date(2026, 9, 15, 0, 0, 0, 0, f.project.Location())
	r := b.Ranges(day, day.Add(24*time.Hour))
	if len(r) != 1 || r[0][0].Hour() != 9 || r[0][1].Hour() != 19 {
		t.Fatalf("ranges %v", r)
	}
	if r := b.Ranges(day.AddDate(0, 0, 4), day.AddDate(0, 0, 5)); len(r) != 0 {
		t.Fatalf("a Saturday has busy hours: %v", r)
	}
}

// A project a few days old does not know its habits yet: every hour counts, as before.
func TestAYoungProjectIsJudgedOnEveryHour(t *testing.T) {
	f := newFixture(t, nil)
	start := time.Date(2026, 9, 14, 0, 0, 0, 0, f.project.Location())
	officeTraffic(f, start, 3)
	b, err := BusyHours(f.ctx, f.db, f.project, start.AddDate(0, 0, 3))
	if err != nil {
		t.Fatal(err)
	}
	if b.Known || !b.At(start.Add(3*time.Hour)) {
		t.Fatalf("a young project must count every hour: %+v", b.Known)
	}
}

// The team can say its hours instead: they win over what the traffic says.
func TestBusyHoursSetByHand(t *testing.T) {
	f := newFixture(t, func(s *project.Settings) {
		s.Traffic.BusyDays = []int{1, 2, 3, 4, 5, 6}
		s.Traffic.BusyFrom, s.Traffic.BusyTo = 8, 20
	})
	b, err := BusyHours(f.ctx, f.db, f.project, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	saturday := time.Date(2026, 9, 19, 8, 30, 0, 0, f.project.Location())
	if !b.Manual || !b.Known || !b.At(saturday) || b.At(saturday.Add(-time.Hour)) || b.Describe() != "Mon-Sat 8-20" {
		t.Fatalf("manual hours %+v %q", b, b.Describe())
	}
}

// dailyFixture is a project judged on daily windows, with four weeks of office traffic behind it.
func dailyFixture(t *testing.T, mutate func(*project.Settings)) (*fixture, time.Time) {
	f := newFixture(t, func(s *project.Settings) {
		s.Missions.WindowHours = 24
		s.Missions.GoodWindows = 2
		s.Missions.MinSeconds = 60
		s.Bosses.MinSeconds = 60
		if mutate != nil {
			mutate(s)
		}
	})
	start := time.Date(2026, 8, 17, 0, 0, 0, 0, f.project.Location()) // a Monday
	officeTraffic(f, start, 35)
	return f, start
}

// slowQueryAt spreads a query's seconds over the hours of a day in proportion to the traffic:
// the same code, costing the same per request whatever the day.
func slowQueryDay(f *fixture, day time.Time, perRequest float64) {
	f.t.Helper()
	for h := 0; h < 24; h++ {
		t := time.Date(day.Year(), day.Month(), day.Day(), h, 5, 0, 0, day.Location())
		n := 40.0
		if wd := t.Weekday(); wd != time.Saturday && wd != time.Sunday && h >= 9 && h < 19 {
			n = 1000
		}
		f.query(t, slowSQL, n*perRequest)
	}
}

// A weekend halves the traffic: the waiting per day halves too, and a mission used to be won on
// a Saturday and a Sunday with nobody touching the code. Judged per request on busy hours, a
// weekend decides nothing.
func TestAQuietWeekendDoesNotWinAMission(t *testing.T) {
	f, start := dailyFixture(t, func(s *project.Settings) { s.Bosses.MinSeconds = 1e9 })
	friday := start.AddDate(0, 0, 25) // Friday 11 September
	slowQueryDay(f, friday, 0.02)
	f.evaluate(friday.AddDate(0, 0, 1).Add(3*time.Hour))
	var id int64
	if err := f.db.Q().Row(f.ctx, "SELECT id FROM missions WHERE type = 'mission'").Scan(&id); err != nil {
		t.Fatal(err)
	}
	for d := 1; d <= 3; d++ { // Saturday, Sunday: same code; Monday: same code
		slowQueryDay(f, friday.AddDate(0, 0, d), 0.02)
		f.evaluate(friday.AddDate(0, 0, d+1).Add(3*time.Hour))
	}
	var status string
	var good int
	_ = f.db.Q().Row(f.ctx, "SELECT status, good_windows FROM missions WHERE id = ?", id).Scan(&status, &good)
	if status != "open" || good != 0 {
		t.Fatalf("the same code won a mission over a weekend: %s, %d good windows", status, good)
	}
	// The fix: a quarter of the time per request from Tuesday, two busy days in a row win it.
	for d := 4; d <= 5; d++ {
		slowQueryDay(f, friday.AddDate(0, 0, d), 0.005)
		f.evaluate(friday.AddDate(0, 0, d+1).Add(3*time.Hour))
	}
	_ = f.db.Q().Row(f.ctx, "SELECT status FROM missions WHERE id = ?", id).Scan(&status)
	if status != "completed" {
		t.Fatalf("the fix did not win it: %s", status)
	}
}

// A busy Monday is not a regression: the same cost per request on more requests.
func TestABusyMondayIsNotARegression(t *testing.T) {
	f, start := dailyFixture(t, func(s *project.Settings) { s.Missions.MaxOpen = 0 })
	for d := 11; d < 18; d++ { // the query existed before: it is not new
		slowQueryDay(f, start.AddDate(0, 0, d), 0.02)
	}
	for d := 18; d <= 28; d++ { // from Friday 4 to Monday 14 September, the same code every day
		day := start.AddDate(0, 0, d)
		slowQueryDay(f, day, 0.02)
		f.evaluate(day.AddDate(0, 0, 1).Add(3*time.Hour))
	}
	if n := f.count("SELECT COUNT(*) FROM missions WHERE type = 'boss'"); n != 0 {
		t.Fatalf("%d bosses from weekdays after a weekend", n)
	}
	// Twice the time per request is one.
	tuesday := start.AddDate(0, 0, 29)
	slowQueryDay(f, tuesday, 0.05)
	f.evaluate(tuesday.AddDate(0, 0, 1).Add(3*time.Hour))
	if n := f.count("SELECT COUNT(*) FROM missions WHERE type = 'boss'"); n != 1 {
		t.Fatalf("%d bosses after the cost per request doubled", n)
	}
}

// Every evening at 18 the people leave and the requests left are the heavy ones: the hour after
// 18 is slower than the hour before on any day, deploy or not. A deploy at 18 is compared with
// the same hour of the days before, not with the afternoon.
func TestAnEveningDeployIsComparedWithTheSameHourOfTheDaysBefore(t *testing.T) {
	f := newFixture(t, func(s *project.Settings) { s.Deploys.CompareMinutes = 60 })
	loc := f.project.Location()
	deployAt := time.Date(2026, 9, 16, 18, 0, 0, 0, loc) // a Wednesday
	for k := 0; k <= 7; k++ {
		day := deployAt.AddDate(0, 0, -k)
		f.traffic(day.Add(-time.Hour), 60, 100, 1, 0, 0) // the afternoon: fast
		f.traffic(day, 60, 60, 9, 3, 0)                  // the evening: slower, every day
	}
	_, _ = f.db.Q().Exec(f.ctx, "INSERT INTO deploys (project_id, version, author, deployed_at) VALUES (?, 'v5', 'eve', ?)", f.project.ID, deployAt.Unix())
	f.evaluate(deployAt.Add(61 * time.Minute))
	var raw string
	_ = f.db.Q().Row(f.ctx, "SELECT impact FROM deploys").Scan(&raw)
	var impact Impact
	if err := json.Unmarshal([]byte(raw), &impact); err != nil {
		t.Fatalf("%v %q", err, raw)
	}
	if impact.Verdict != "neutral" || impact.Compared != "same_hour" {
		t.Fatalf("an evening like every evening judged %s against %s: %+v", impact.Verdict, impact.Compared, impact)
	}
}
