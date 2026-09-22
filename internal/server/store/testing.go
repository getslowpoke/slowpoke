package store

import (
	"context"
	"fmt"
	"os"
	"strings"
	"sync/atomic"
	"testing"
)

var testDBCounter atomic.Int64

// OpenTest returns a migrated empty database. By default an in-memory SQLite;
// with SLOWPOKE_TEST_POSTGRES=postgres://... every test gets its own schema.
func OpenTest(t testing.TB) *DB {
	t.Helper()
	ctx := context.Background()

	if url := os.Getenv("SLOWPOKE_TEST_POSTGRES"); url != "" {
		admin, err := Open(url)
		if err != nil {
			t.Fatal(err)
		}
		schema := fmt.Sprintf("t_%d_%d", os.Getpid(), testDBCounter.Add(1))
		if _, err := admin.ExecContext(ctx, "CREATE SCHEMA "+schema); err != nil {
			t.Fatal(err)
		}
		sep := "?"
		if strings.Contains(url, "?") {
			sep = "&"
		}
		db, err := Open(url + sep + "search_path=" + schema)
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() {
			db.Close()
			_, _ = admin.ExecContext(ctx, "DROP SCHEMA "+schema+" CASCADE")
			admin.Close()
		})
		if err := db.Migrate(ctx); err != nil {
			t.Fatal(err)
		}
		return db
	}

	db, err := Open("sqlite://:memory:")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	if err := db.Migrate(ctx); err != nil {
		t.Fatal(err)
	}
	return db
}
