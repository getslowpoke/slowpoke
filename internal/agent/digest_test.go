package agent

import "testing"

func TestDigestDeltas(t *testing.T) {
	prev := map[string]digestRow{
		"shop|d1": {Schema: "shop", Text: "SELECT * FROM `orders` WHERE `id` = ?", Count: 100, Seconds: 10, Rows: 100},
		"shop|d2": {Schema: "shop", Text: "SELECT SLEEP (?)", Count: 5, Seconds: 5, Rows: 0},
	}
	cur := map[string]digestRow{
		"shop|d1": {Schema: "shop", Text: "SELECT * FROM `orders` WHERE `id` = ?", Count: 110, Seconds: 11, MaxSeconds: 0.5, Rows: 110, Sample: "SELECT * FROM orders WHERE id = 7"},
		"shop|d2": {Schema: "shop", Text: "SELECT SLEEP (?)", Count: 7, Seconds: 9, MaxSeconds: 2, Rows: 0},
		"shop|d3": {Schema: "shop", Text: "SELECT ?", Count: 3, Seconds: 0.003},
	}

	stats, slowN, slowSum, slowMax := digestDeltas(prev, cur, 1.0, true)

	if len(stats) != 3 {
		t.Fatalf("want 3 stats (new digest counts from zero), got %d", len(stats))
	}
	var orders, sleep bool
	for _, s := range stats {
		switch s.Fingerprint {
		case "select * from orders where id = ?":
			orders = s.N == 10 && s.Sum > 0.99 && s.Sum < 1.01 && s.Rows == 10 && s.Sample == "SELECT * FROM orders WHERE id = 7" && s.Database == "shop"
		case "select sleep(?)":
			sleep = s.N == 2 && s.Sum == 4
		}
	}
	if !orders || !sleep {
		t.Fatalf("stats %+v", stats)
	}
	// Only the sleep digest averages above 1 s.
	if slowN != 2 || slowSum != 4 || slowMax != 2 {
		t.Fatalf("slow %d %v %v", slowN, slowSum, slowMax)
	}

	// Counters reset (server restart): current values are the delta.
	reset := map[string]digestRow{"shop|d1": {Schema: "shop", Text: "SELECT 1", Count: 4, Seconds: 1}}
	stats, _, _, _ = digestDeltas(prev, reset, 1.0, false)
	for _, s := range stats {
		if s.N != 4 {
			t.Fatalf("reset %+v", s)
		}
	}
}
