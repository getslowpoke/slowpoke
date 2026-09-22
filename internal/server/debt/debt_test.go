package debt

import (
	"context"
	"testing"
	"time"

	"github.com/getslowpoke/slowpoke/internal/fingerprint"
	"github.com/getslowpoke/slowpoke/internal/protocol"
	"github.com/getslowpoke/slowpoke/internal/server/doctor"
	"github.com/getslowpoke/slowpoke/internal/server/ingest"
	"github.com/getslowpoke/slowpoke/internal/server/project"
	"github.com/getslowpoke/slowpoke/internal/server/store"
)

func TestEffortFromDiagnoses(t *testing.T) {
	cases := []struct {
		kind, typ string
		codes     []string
		want      string
	}{
		{"query", "mission", []string{"full_scan", "filesort"}, "xs"},
		{"query", "mission", []string{"implicit_conversion"}, "xs"},
		{"query", "mission", []string{"n_plus_one"}, "s"},
		{"query", "mission", []string{"full_scan", "leading_wildcard"}, "m"},
		{"query", "mission", nil, "m"},
		{"endpoint", "mission", nil, "m"},
		{"php", "mission", nil, "s"},
		{"query", "boss", []string{"deep_offset"}, "s"},
	}
	for _, c := range cases {
		var ds []doctor.Diagnosis
		for _, code := range c.codes {
			ds = append(ds, doctor.Diagnosis{Code: code})
		}
		if got := EstimateEffort(c.kind, c.typ, ds); got != c.want {
			t.Errorf("%s/%s %v: got %s want %s", c.kind, c.typ, c.codes, got, c.want)
		}
	}
	if ROI(3600, "xs") <= ROI(3600, "l") || EffortHours["m"] <= EffortHours["s"] {
		t.Fatal("cheap fixes must rank higher")
	}
}

type fixture struct {
	ctx context.Context
	db  *store.DB
	p   project.Project
	now time.Time
}

func setup(t *testing.T) fixture {
	db := store.OpenTest(t)
	ctx := context.Background()
	p, _ := project.Repo{DB: db}.Create(ctx, "Shop", "UTC")
	return fixture{ctx, db, p, time.Date(2026, 9, 17, 12, 0, 0, 0, time.UTC)}
}

func (f fixture) mission(t *testing.T, typ, status string, baseline, current float64, opened, closed time.Time, deployID any) int64 {
	var closedAt any
	if !closed.IsZero() {
		closedAt = closed.Unix()
	}
	id, err := f.db.Q().InsertID(f.ctx, `INSERT INTO missions (project_id, type, kind, target, label, baseline, current, points, status, good_windows, person, opened_at, checked_at, closed_at, deploy_id)
		VALUES (?, ?, 'query', 'x', 'x', ?, ?, 10, ?, 0, '', ?, ?, ?, ?)`, f.p.ID, typ, baseline, current, status, opened.Unix(), opened.Unix(), closedAt, deployID)
	if err != nil {
		t.Fatal(err)
	}
	return id
}

func TestWeeklyGoalProgress(t *testing.T) {
	f := setup(t)
	// window 24h: seconds per window = seconds per day
	f.mission(t, "mission", "completed", 500, 100, f.now.Add(-10*24*time.Hour), f.now.Add(-2*24*time.Hour), nil) // 400 repaid this week
	f.mission(t, "boss", "defeated", 300, 200, f.now.Add(-3*24*time.Hour), f.now.Add(-24*time.Hour), nil)        // 100 more
	f.mission(t, "mission", "completed", 900, 0, f.now.Add(-30*24*time.Hour), f.now.Add(-20*24*time.Hour), nil)  // older week
	f.mission(t, "mission", "open", 700, 650, f.now.Add(-24*time.Hour), time.Time{}, nil)

	g, err := WeekGoal(f.ctx, f.db, f.p, f.now)
	if err != nil {
		t.Fatal(err)
	}
	if g.RepaidSecondsPerDay != 500 || g.GoalSecondsPerDay != 600 || g.Reached || g.Progress < 0.83 || g.Progress > 0.84 {
		t.Fatalf("%+v", g)
	}
}

func TestDeliveryHealth(t *testing.T) {
	f := setup(t)
	deploy := func(daysAgo int, verdict string) int64 {
		impact := `{"verdict":"` + verdict + `"}`
		id, _ := f.db.Q().InsertID(f.ctx, "INSERT INTO deploys (project_id, version, deployed_at, impact) VALUES (?, 'v', ?, ?)", f.p.ID, f.now.Add(-time.Duration(daysAgo)*24*time.Hour).Unix(), impact)
		return id
	}
	deploy(1, "better")
	deploy(3, "neutral")
	bad := deploy(5, "worse")
	withBoss := deploy(8, "neutral")
	deploy(12, "no_data")
	deploy(40, "worse") // outside the 28 days
	f.mission(t, "boss", "defeated", 30, 30, f.now.Add(-8*24*time.Hour), f.now.Add(-8*24*time.Hour+6*time.Hour), withBoss)
	f.mission(t, "boss", "defeated", 30, 30, f.now.Add(-5*24*time.Hour), f.now.Add(-5*24*time.Hour+2*time.Hour), bad)

	h, err := Delivery(f.ctx, f.db, f.p, f.now)
	if err != nil {
		t.Fatal(err)
	}
	// 5 deploys in 28 days = 1.25/week; measured deploys 4, failures 2 (worse + caused a boss) = 50%; restore 4h average.
	if h.Deploys != 5 || h.PerWeek != 1.25 || h.ChangeFailureRate != 50 || h.MeanHoursToRestore != 4 {
		t.Fatalf("%+v", h)
	}
	if h.Rating == "" {
		t.Fatal("rating")
	}
}

func TestHotspotsByFile(t *testing.T) {
	f := setup(t)
	svc := ingest.New(f.db)
	svc.Now = func() time.Time { return f.now }
	minute := f.now.Add(-time.Hour).Unix()
	q := func(sql, origin string, seconds float64) (string, protocol.AppQuery, protocol.QueryStat) {
		fp := fingerprint.Of(sql)
		return fingerprint.Hash(fp), protocol.AppQuery{Fingerprint: fp, Origin: origin, N: 10, Sum: seconds}, protocol.QueryStat{Fingerprint: fp, N: 10, Sum: seconds}
	}
	h1, a1, s1 := q("select * from orders where id = 1", "app/Http/Controllers/OrderController.php:42", 300)
	h2, a2, s2 := q("select * from items where order_id = 1", "app/Http/Controllers/OrderController.php:77", 100)
	h3, a3, s3 := q("select * from users where id = 1", "app/Models/User.php:10", 50)
	_, err := svc.Ingest(f.ctx, f.p, protocol.Payload{BatchID: "h", Agent: protocol.AgentInfo{Host: "w", Kind: "agent"},
		App: []protocol.AppBucket{{Minute: minute, Queries: map[string]protocol.AppQuery{h1: a1, h2: a2, h3: a3}}},
		DB:  []protocol.DBBucket{{Source: "mysql-digest", Minute: minute, Queries: map[string]protocol.QueryStat{h1: s1, h2: s2, h3: s3}}},
		PHP: []protocol.PHPBucket{{Source: "php-fpm:www", Minute: minute, N: 20, Stalls: []protocol.PHPStall{{Frame: "curl_exec()", Code: "app/Services/Shipping.php:57", N: 20}}}},
	})
	if err != nil {
		t.Fatal(err)
	}
	spots, err := Hotspots(f.ctx, f.db, f.p, f.now)
	if err != nil {
		t.Fatal(err)
	}
	if len(spots) != 3 || spots[0].File != "app/Http/Controllers/OrderController.php" || spots[0].SecondsPerDay != 57.143 || spots[0].Items != 2 || len(spots[0].Lines) != 2 {
		t.Fatalf("%+v", spots)
	}
	if spots[1].File != "app/Models/User.php" || spots[2].File != "app/Services/Shipping.php" || spots[2].Kind != "php" {
		t.Fatalf("%+v", spots)
	}
}
