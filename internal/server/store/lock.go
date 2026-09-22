package store

import (
	"context"
	"hash/fnv"
	"sync"
)

var localLocks sync.Map

// TryLock takes a lock shared by every server instance (PostgreSQL advisory
// lock; in-process for SQLite, which runs as a single instance). It returns
// false when another instance holds it.
func (d *DB) TryLock(ctx context.Context, name string) (unlock func(), ok bool, err error) {
	if d.Dialect != Postgres {
		m, _ := localLocks.LoadOrStore(d.lockKey(name), &sync.Mutex{})
		mu := m.(*sync.Mutex)
		if !mu.TryLock() {
			return func() {}, false, nil
		}
		return mu.Unlock, true, nil
	}
	conn, err := d.Conn(ctx)
	if err != nil {
		return func() {}, false, err
	}
	key := d.lockKey(name)
	if err := conn.QueryRowContext(ctx, "SELECT pg_try_advisory_lock($1)", key).Scan(&ok); err != nil || !ok {
		conn.Close()
		return func() {}, false, err
	}
	return func() {
		_, _ = conn.ExecContext(context.Background(), "SELECT pg_advisory_unlock($1)", key)
		conn.Close()
	}, true, nil
}

func (d *DB) lockKey(name string) int64 {
	h := fnv.New64a()
	_, _ = h.Write([]byte("slowpoke:" + name))
	return int64(h.Sum64() >> 1)
}
