package agent

import (
	"context"
	"fmt"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/getslowpoke/slowpoke/internal/protocol"
)

// pg_stat_statements keeps statements with $n placeholders and no values: in the lab no
// PostgreSQL query was ever explained. PostgreSQL 16+ plans them with GENERIC_PLAN.
// Runs only against a real server (./scripts/test sets SLOWPOKE_TEST_POSTGRES).
func TestPostgresExplainWithoutValues(t *testing.T) {
	url := os.Getenv("SLOWPOKE_TEST_POSTGRES")
	if url == "" {
		t.Skip("SLOWPOKE_TEST_POSTGRES not set")
	}
	ctx := context.Background()
	d, err := openDBSource(Source{Type: "postgres", Name: "pg", DSN: url}, true)
	if err != nil {
		t.Fatal(err)
	}
	defer d.db.Close()
	table := fmt.Sprintf("orders_generic_%d", time.Now().UnixNano())
	for _, stmt := range []string{
		"CREATE TABLE " + table + " (id bigint primary key, status text, total numeric)",
		"CREATE INDEX " + table + "_status ON " + table + " (status)",
	} {
		if _, err := d.db.ExecContext(ctx, stmt); err != nil {
			t.Fatal(err)
		}
	}
	defer d.db.ExecContext(ctx, "DROP TABLE "+table)

	res := d.explain(ctx, protocol.ExplainRequest{Hash: "h", SQL: "select id, total from " + table + " where status = $1 limit $2"})
	if res.Error != "" {
		t.Fatalf("explain failed: %s", res.Error)
	}
	if !res.Generic || !strings.Contains(string(res.Plan), `"Node Type"`) {
		t.Fatalf("no generic plan: generic=%v plan=%s", res.Generic, res.Plan)
	}
	if def, ok := res.Tables[table]; !ok || len(def.Columns) != 3 || len(def.Indexes) != 2 {
		t.Fatalf("table not described: %+v", res.Tables)
	}

	// A statement with values keeps the ordinary plan.
	if res := d.explain(ctx, protocol.ExplainRequest{Hash: "h", SQL: "select id from " + table + " where status = 'paid'"}); res.Error != "" || res.Generic {
		t.Fatalf("plain explain: %+v", res)
	}
	// Placeholders never make an unsafe statement explainable.
	if res := d.explain(ctx, protocol.ExplainRequest{Hash: "h", SQL: "select * from " + table + " where id = $1 for update"}); res.Error == "" {
		t.Fatal("for update explained")
	}
}

// The generic-plan sample must survive redaction: placeholders are not values.
func TestPostgresPlaceholderSampleSurvivesRedaction(t *testing.T) {
	a := NewAggregator()
	sql := "select id, name from products where description ilike $1 limit $2"
	stats := map[string]protocol.QueryStat{hashOf(sql): {Fingerprint: sql, Sample: sql, N: 1, Sum: 2, Max: 2}}
	a.AddQueryStats("pg-statements", SampleOwner{Engine: "postgres", Source: "0"}, t0, stats, 0, 0, 0)
	for _, q := range a.Drain(t0.Add(time.Minute)).DB[0].Queries {
		if q.Sample != sql {
			t.Fatalf("sent %q", q.Sample)
		}
	}
}
