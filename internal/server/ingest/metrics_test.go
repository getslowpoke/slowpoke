package ingest

import (
	"testing"

	"github.com/getslowpoke/slowpoke/internal/fingerprint"
	"github.com/getslowpoke/slowpoke/internal/protocol"
)

func TestCustomMetricsMerge(t *testing.T) {
	svc, p, ctx := setup(t)
	hist := make([]int, 15)
	hist[4] = 2
	send := func(batch string, b protocol.MetricBucket) {
		if _, err := svc.Ingest(ctx, p, protocol.Payload{BatchID: batch, Agent: agentInfo(), Metrics: []protocol.MetricBucket{b}}); err != nil {
			t.Fatal(err)
		}
	}
	send("1", protocol.MetricBucket{Minute: minute, Name: "invoices.pdf", Kind: "timer", Count: 2, Sum: 0.8, Min: 0.3, Max: 0.5, Last: 0.5, Hist: hist})
	send("2", protocol.MetricBucket{Minute: minute, Name: "invoices.pdf", Kind: "timer", Count: 1, Sum: 2, Min: 2, Max: 2, Last: 2, Hist: hist})
	send("3", protocol.MetricBucket{Minute: minute, Name: "queue.backlog", Kind: "gauge", Count: 1, Sum: 9, Min: 9, Max: 9, Last: 9})
	send("4", protocol.MetricBucket{Minute: minute, Name: "", Kind: "gauge", Count: 1})
	send("5", protocol.MetricBucket{Minute: minute, Name: "x", Kind: "histogram?", Count: 1})

	var count, h4 int
	var sum, mn, mx, last float64
	if err := svc.db.Q().Row(ctx, "SELECT count, sum, min, max, last, h4 FROM metric_minutes WHERE name = 'invoices.pdf'").Scan(&count, &sum, &mn, &mx, &last, &h4); err != nil {
		t.Fatal(err)
	}
	if count != 3 || sum != 2.8 || mn != 0.3 || mx != 2 || last != 2 || h4 != 4 {
		t.Fatalf("count=%d sum=%v min=%v max=%v last=%v h4=%d", count, sum, mn, mx, last, h4)
	}
	var rows int
	_ = svc.db.Q().Row(ctx, "SELECT COUNT(*) FROM metric_minutes").Scan(&rows)
	if rows != 2 {
		t.Fatalf("invalid metrics must be skipped, rows=%d", rows)
	}
}

// Four Redis servers report "redis.cache.memory_bytes": one series per machine, never one sum.
func TestCustomMetricsKeepMachinesApart(t *testing.T) {
	svc, p, ctx := setup(t)
	send := func(batch, host string, value float64) {
		info := agentInfo()
		info.Host = host
		b := protocol.MetricBucket{Minute: minute, Name: "redis.cache.memory_bytes", Kind: "gauge", Count: 1, Sum: value, Min: value, Max: value, Last: value}
		if _, err := svc.Ingest(ctx, p, protocol.Payload{BatchID: batch, Agent: info, Metrics: []protocol.MetricBucket{b}}); err != nil {
			t.Fatal(err)
		}
	}
	send("1", "web-1", 100)
	send("2", "db-1", 900)
	send("3", "web-1", 150) // the same machine again in the same minute: this one merges

	rows, err := svc.db.Q().Query(ctx, `SELECT h.name, m.count, m.last FROM metric_minutes m JOIN hosts h ON h.id = m.host_id
		WHERE m.name = 'redis.cache.memory_bytes' ORDER BY h.name`)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	type row struct {
		host  string
		count int64
		last  float64
	}
	var got []row
	for rows.Next() {
		var r row
		if err := rows.Scan(&r.host, &r.count, &r.last); err != nil {
			t.Fatal(err)
		}
		got = append(got, r)
	}
	want := []row{{"db-1", 1, 900}, {"web-1", 2, 150}}
	if len(got) != len(want) {
		t.Fatalf("one row per machine, got %+v", got)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("row %d = %+v, want %+v", i, got[i], want[i])
		}
	}
}

// Two databases on two machines run the same statement: the project total counts it once, and
// each machine can still say what it saw. The host is known when the payload arrives — before
// this it was thrown away while writing the hour.
func TestQueriesRememberWhichMachineSawThem(t *testing.T) {
	svc, p, ctx := setup(t)
	fp := "select * from orders where note like ?"
	hash := fingerprint.Hash(fp)
	send := func(host string, seconds float64) {
		info := agentInfo("mysql-slowlog:mysql-slowlog")
		info.Host = host
		_, err := svc.Ingest(ctx, p, protocol.Payload{
			BatchID: host, Agent: info,
			DB: []protocol.DBBucket{{Source: "mysql-slowlog", Minute: minute, SlowN: 1, SlowSum: seconds, SlowMax: seconds,
				Queries: map[string]protocol.QueryStat{hash: {Fingerprint: fp, N: 1, Sum: seconds, Max: seconds}}}},
		})
		if err != nil {
			t.Fatal(err)
		}
	}
	send("db-1", 10)
	send("db-2", 4)

	rows, err := svc.db.Q().Query(ctx, `SELECT h.name, q.sum FROM query_hours q
		JOIN hosts h ON h.id = q.host_id JOIN query_fingerprints f ON f.id = q.fingerprint_id
		WHERE f.hash = ? ORDER BY h.name`, hash)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	seen := map[string]float64{}
	for rows.Next() {
		var name string
		var sum float64
		if err := rows.Scan(&name, &sum); err != nil {
			t.Fatal(err)
		}
		seen[name] = sum
	}
	if len(seen) != 2 || seen["db-1"] != 10 || seen["db-2"] != 4 {
		t.Fatalf("each machine keeps what it saw: %v", seen)
	}
}
