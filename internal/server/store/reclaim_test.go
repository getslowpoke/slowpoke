package store

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// SQLite keeps the pages freed by deletions inside the file and never gives them back: the
// retention deleted rows every hour and the file of a real installation only grew. Reclaim switches the
// file to incremental vacuum once, then hands the free pages back at every call.
func TestReclaimGivesFreedSpaceBackToTheDisk(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "slowpoke.db")
	db, err := Open("sqlite://" + path)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if _, err := db.ExecContext(ctx, `CREATE TABLE junk (id INTEGER PRIMARY KEY, v TEXT)`); err != nil {
		t.Fatal(err)
	}
	fill := func() {
		for i := 0; i < 2000; i++ {
			db.ExecContext(ctx, `INSERT INTO junk (v) VALUES (?)`, strings.Repeat("x", 1000))
		}
		db.ExecContext(ctx, `DELETE FROM junk`)
	}
	pages := func() (n int64) {
		db.QueryRowContext(ctx, `PRAGMA page_count`).Scan(&n)
		return
	}

	fill()
	before := pages()
	if err := db.Reclaim(ctx); err != nil {
		t.Fatal(err)
	}
	var mode int
	db.QueryRowContext(ctx, `PRAGMA auto_vacuum`).Scan(&mode)
	if mode != 2 || pages() >= before/4 {
		t.Fatalf("first reclaim: auto_vacuum=%d pages %d -> %d", mode, before, pages())
	}
	// In WAL mode VACUUM writes the whole database into the log, and SQLite keeps a log at its
	// largest size: on a real installation the file went from 203 to 80 MB and left an 80 MB log behind.
	if info, err := os.Stat(path + "-wal"); err == nil && info.Size() > 64*1024 {
		t.Fatalf("the write-ahead log was left at %d bytes", info.Size())
	}

	fill()
	before = pages()
	if err := db.Reclaim(ctx); err != nil {
		t.Fatal(err)
	}
	if pages() >= before/4 {
		t.Fatalf("incremental reclaim: pages %d -> %d", before, pages())
	}
}
