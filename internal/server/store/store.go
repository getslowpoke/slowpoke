// Package store wraps database/sql for PostgreSQL and SQLite with one SQL
// dialect: queries use "?" placeholders, times are unix seconds (BIGINT),
// JSON is TEXT. Only the few differences are behind Dialect helpers.
package store

import (
	"context"
	"database/sql"
	"fmt"
	"strconv"
	"strings"

	_ "github.com/jackc/pgx/v5/stdlib"
	_ "modernc.org/sqlite"
)

type Dialect string

const (
	Postgres Dialect = "postgres"
	SQLite   Dialect = "sqlite"
)

type DB struct {
	*sql.DB
	Dialect Dialect
}

// Open accepts "postgres://..." or "sqlite:///path/to/file.db" ("sqlite://:memory:" for tests).
func Open(url string) (*DB, error) {
	switch {
	case strings.HasPrefix(url, "postgres://"), strings.HasPrefix(url, "postgresql://"):
		db, err := sql.Open("pgx", url)
		if err != nil {
			return nil, err
		}
		db.SetMaxOpenConns(20)
		db.SetMaxIdleConns(5)
		return &DB{DB: db, Dialect: Postgres}, nil
	case strings.HasPrefix(url, "sqlite://"):
		path := strings.TrimPrefix(url, "sqlite://")
		dsn := path + "?_pragma=busy_timeout(10000)&_pragma=journal_mode(WAL)&_pragma=foreign_keys(1)&_pragma=synchronous(NORMAL)"
		if path == ":memory:" {
			dsn = "file::memory:?_pragma=foreign_keys(1)"
		}
		db, err := sql.Open("sqlite", dsn)
		if err != nil {
			return nil, err
		}
		// SQLite has a single writer: one connection avoids "database is locked".
		db.SetMaxOpenConns(1)
		return &DB{DB: db, Dialect: SQLite}, nil
	}
	return nil, fmt.Errorf("unsupported database url %q (use postgres:// or sqlite://)", url)
}

// Rebind converts "?" placeholders to "$n" for PostgreSQL.
func (d *DB) Rebind(query string) string {
	if d.Dialect != Postgres {
		return query
	}
	var b strings.Builder
	n := 0
	inString := false
	for i := 0; i < len(query); i++ {
		c := query[i]
		if c == '\'' {
			inString = !inString
		}
		if c == '?' && !inString {
			n++
			b.WriteByte('$')
			b.WriteString(strconv.Itoa(n))
			continue
		}
		b.WriteByte(c)
	}
	return b.String()
}

// Greatest is GREATEST(a, b) on PostgreSQL and MAX(a, b) on SQLite.
func (d *DB) Greatest(a, b string) string {
	if d.Dialect == Postgres {
		return "GREATEST(" + a + ", " + b + ")"
	}
	return "MAX(" + a + ", " + b + ")"
}

type Querier interface {
	ExecContext(ctx context.Context, query string, args ...any) (sql.Result, error)
	QueryContext(ctx context.Context, query string, args ...any) (*sql.Rows, error)
	QueryRowContext(ctx context.Context, query string, args ...any) *sql.Row
}

// Q binds a Querier (DB or Tx) to the dialect.
type Q struct {
	q Querier
	d *DB
}

func (d *DB) Q() Q                { return Q{q: d.DB, d: d} }
func (d *DB) WithTx(tx *sql.Tx) Q { return Q{q: tx, d: d} }

func (q Q) DB() *DB { return q.d }

func (q Q) Exec(ctx context.Context, query string, args ...any) (sql.Result, error) {
	return q.q.ExecContext(ctx, q.d.Rebind(query), args...)
}

func (q Q) Query(ctx context.Context, query string, args ...any) (*sql.Rows, error) {
	return q.q.QueryContext(ctx, q.d.Rebind(query), args...)
}

func (q Q) Row(ctx context.Context, query string, args ...any) *sql.Row {
	return q.q.QueryRowContext(ctx, q.d.Rebind(query), args...)
}

// InsertID runs an INSERT and returns the new id on both dialects.
func (q Q) InsertID(ctx context.Context, query string, args ...any) (int64, error) {
	if q.d.Dialect == Postgres {
		var id int64
		err := q.Row(ctx, query+" RETURNING id", args...).Scan(&id)
		return id, err
	}
	res, err := q.Exec(ctx, query, args...)
	if err != nil {
		return 0, err
	}
	return res.LastInsertId()
}

// Tx runs fn in a transaction.
func (d *DB) Tx(ctx context.Context, fn func(q Q) error) error {
	tx, err := d.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	if err := fn(d.WithTx(tx)); err != nil {
		_ = tx.Rollback()
		return err
	}
	return tx.Commit()
}

// Reclaim gives the disk back the space freed by deletions. SQLite keeps freed pages inside the
// file for reuse and never shrinks it, so a database pruned every hour stays as large as it ever
// was. The first call switches the file to incremental vacuum, which needs one full VACUUM (a
// few seconds, twice the file in temporary space); every later call returns the free pages, a
// cheap operation. PostgreSQL does this on its own with autovacuum.
func (d *DB) Reclaim(ctx context.Context) error {
	if d.Dialect != SQLite {
		return nil
	}
	var mode int
	if err := d.QueryRowContext(ctx, `PRAGMA auto_vacuum`).Scan(&mode); err != nil {
		return err
	}
	const incremental = 2
	if mode != incremental {
		if _, err := d.ExecContext(ctx, `PRAGMA auto_vacuum = INCREMENTAL`); err != nil {
			return err
		}
		if _, err := d.ExecContext(ctx, `VACUUM`); err != nil {
			return err
		}
	} else if _, err := d.ExecContext(ctx, `PRAGMA incremental_vacuum`); err != nil {
		return err
	}
	// In WAL mode the pages moved above go through the log, and SQLite keeps the log file at its
	// largest size: truncating it is what actually returns the space.
	_, err := d.ExecContext(ctx, `PRAGMA wal_checkpoint(TRUNCATE)`)
	return err
}
