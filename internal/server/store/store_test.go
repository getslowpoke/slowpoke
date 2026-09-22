package store

import (
	"context"
	"crypto/sha256"
	"flag"
	"fmt"
	"os"
	"strings"
	"testing"
)

func TestMigrateIsIdempotent(t *testing.T) {
	db := OpenTest(t)
	if err := db.Migrate(context.Background()); err != nil {
		t.Fatal(err)
	}
	var n int
	if err := db.Q().Row(context.Background(), "SELECT COUNT(*) FROM schema_migrations").Scan(&n); err != nil || n != len(migrations) {
		t.Fatalf("n=%d err=%v", n, err)
	}
}

func TestRebindSkipsStrings(t *testing.T) {
	d := &DB{Dialect: Postgres}
	if got := d.Rebind("SELECT '?' , a FROM t WHERE b = ? AND c = ?"); got != "SELECT '?' , a FROM t WHERE b = $1 AND c = $2" {
		t.Fatal(got)
	}
}

func TestInsertIDAndAdditiveUpsert(t *testing.T) {
	db := OpenTest(t)
	ctx := context.Background()
	q := db.Q()
	id, err := q.InsertID(ctx, "INSERT INTO projects (name, slug, token, created_at) VALUES (?, ?, ?, ?)", "Shop", "shop", "spk_1", 1)
	if err != nil || id == 0 {
		t.Fatalf("id=%d err=%v", id, err)
	}
	upsert := "INSERT INTO endpoint_hours (project_id, source, endpoint, hour, n, max) VALUES (?, 'nginx:app', 'GET /', 0, ?, ?) " +
		"ON CONFLICT (project_id, source, endpoint, hour) DO UPDATE SET n = endpoint_hours.n + excluded.n, max = " + db.Greatest("endpoint_hours.max", "excluded.max")
	for _, v := range [][2]float64{{2, 0.5}, {3, 0.2}} {
		if _, err := q.Exec(ctx, upsert, id, v[0], v[1]); err != nil {
			t.Fatal(err)
		}
	}
	var n int
	var mx float64
	if err := q.Row(ctx, "SELECT n, max FROM endpoint_hours").Scan(&n, &mx); err != nil || n != 5 || mx != 0.5 {
		t.Fatalf("n=%d max=%v err=%v", n, mx, err)
	}
}

// Versions are positions in the list, so a migration inserted or changed in the middle takes a
// number that older databases have already recorded: it is skipped for ever, and so is every
// migration after it. That is how production ended up without metric_minutes.host_id on
// 2026-09-18. This test pins the list: existing entries may never change, only new ones at the
// end. Regenerate deliberately with: go test ./internal/server/store -run AppendOnly -update
func TestMigrationsAreAppendOnly(t *testing.T) {
	sums := make([]string, len(migrations))
	for i, m := range migrations {
		sums[i] = fmt.Sprintf("%x", sha256.Sum256([]byte(m)))
	}
	path := "migrations.sum"
	if *updateSums {
		if err := os.WriteFile(path, []byte(strings.Join(sums, "\n")+"\n"), 0o644); err != nil {
			t.Fatal(err)
		}
		t.Log("migrations.sum rewritten")
		return
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	want := strings.Fields(string(raw))
	if len(sums) < len(want) {
		t.Fatalf("a migration was removed: %d left, %d already released", len(sums), len(want))
	}
	for i := range want {
		if sums[i] != want[i] {
			t.Fatalf("migration %d changed: databases that already recorded version %d would never run it, "+
				"and every migration after it would be skipped. Add a new migration at the end instead.", i+1, i+1)
		}
	}
}

var updateSums = flag.Bool("update", false, "rewrite migrations.sum")

// A database that recorded a version number without running its migration — because that
// migration was inserted in the middle of the list — is repaired by the copy at the end.
// This is exactly what happened to the production database on 2026-09-18.
func TestASkippedMigrationIsRepairedByTheOneAtTheEnd(t *testing.T) {
	db := OpenTest(t)
	ctx := context.Background()

	// Put it back the way production was: metric_minutes without host_id, every version recorded
	// except the repair at the end.
	for _, sql := range []string{
		"DROP TABLE metric_minutes",
		`CREATE TABLE metric_minutes (
			project_id BIGINT NOT NULL REFERENCES projects(id) ON DELETE CASCADE,
			name TEXT NOT NULL, bucket BIGINT NOT NULL, kind TEXT NOT NULL,
			count BIGINT NOT NULL DEFAULT 0, sum REAL NOT NULL DEFAULT 0, min REAL NOT NULL DEFAULT 0,
			max REAL NOT NULL DEFAULT 0, last REAL NOT NULL DEFAULT 0,
			h0 BIGINT NOT NULL DEFAULT 0, h1 BIGINT NOT NULL DEFAULT 0, h2 BIGINT NOT NULL DEFAULT 0,
			h3 BIGINT NOT NULL DEFAULT 0, h4 BIGINT NOT NULL DEFAULT 0, h5 BIGINT NOT NULL DEFAULT 0,
			h6 BIGINT NOT NULL DEFAULT 0, h7 BIGINT NOT NULL DEFAULT 0, h8 BIGINT NOT NULL DEFAULT 0,
			h9 BIGINT NOT NULL DEFAULT 0, h10 BIGINT NOT NULL DEFAULT 0, h11 BIGINT NOT NULL DEFAULT 0,
			h12 BIGINT NOT NULL DEFAULT 0, h13 BIGINT NOT NULL DEFAULT 0, h14 BIGINT NOT NULL DEFAULT 0,
			PRIMARY KEY (project_id, name, bucket))`,
		fmt.Sprintf("DELETE FROM schema_migrations WHERE version = %d", repairVersion(t)),
	} {
		if _, err := db.Q().Exec(ctx, sql); err != nil {
			t.Fatalf("%s: %v", sql, err)
		}
	}

	if err := db.Migrate(ctx); err != nil {
		t.Fatal(err)
	}
	if !schemaOf(t, db)["metric_minutes.host_id"] {
		t.Fatal("the repair at the end of the list did not bring host_id back")
	}
	// And it does nothing the second time.
	if err := db.Migrate(ctx); err != nil {
		t.Fatal(err)
	}
}

// repairVersion is the position of the guarded rebuild of metric_minutes, wherever it sits in
// the list: new migrations are appended after it.
func repairVersion(t testing.TB) int {
	t.Helper()
	for i := len(migrations) - 1; i >= 0; i-- {
		if strings.Contains(migrations[i], "-- if-missing-column: metric_minutes.host_id") {
			return i + 1
		}
	}
	t.Fatal("the repair migration is gone")
	return 0
}

// schemaOf lists every "table.column" of a database, whatever the dialect.
func schemaOf(t testing.TB, db *DB) map[string]bool {
	t.Helper()
	q := `SELECT m.name, p.name FROM sqlite_master m JOIN pragma_table_info(m.name) p WHERE m.type = 'table'`
	if db.Dialect == Postgres {
		q = `SELECT table_name, column_name FROM information_schema.columns WHERE table_schema = current_schema()`
	}
	rows, err := db.Q().Query(context.Background(), q)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	out := map[string]bool{}
	for rows.Next() {
		var table, column string
		if err := rows.Scan(&table, &column); err != nil {
			t.Fatal(err)
		}
		out[table+"."+column] = true
	}
	return out
}
