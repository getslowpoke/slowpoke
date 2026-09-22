package ingest

import (
	"bytes"
	"compress/gzip"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"sync"
	"sync/atomic"
	"time"

	"github.com/getslowpoke/slowpoke/internal/protocol"
	"github.com/getslowpoke/slowpoke/internal/server/project"
	"github.com/getslowpoke/slowpoke/internal/server/store"
)

const (
	MaxAttempts  = 5
	claimTimeout = 2 * time.Minute
)

// Queue is a durable ingest queue in the database: the HTTP handler only
// stores the payload, workers write the rollups. With PostgreSQL several
// server instances can share it (FOR UPDATE SKIP LOCKED).
type Queue struct {
	DB  *store.DB
	Now func() time.Time
	mu  sync.Mutex // SQLite: serialize claims
}

func NewQueue(db *store.DB) *Queue {
	return &Queue{DB: db, Now: time.Now}
}

func (q *Queue) Enqueue(ctx context.Context, projectID int64, body []byte, gzipped bool) error {
	return q.EnqueueFrom(ctx, projectID, 0, body, gzipped)
}

// EnqueueFrom records which agent token sent the payload (0 = project token).
func (q *Queue) EnqueueFrom(ctx context.Context, projectID, tokenID int64, body []byte, gzipped bool) error {
	now := q.Now().Unix()
	var token any
	if tokenID > 0 {
		token = tokenID
	}
	_, err := q.DB.Q().Exec(ctx, "INSERT INTO ingest_queue (project_id, payload, gzipped, received_at, available_at, token_id) VALUES (?, ?, ?, ?, ?, ?)", projectID, body, gzipped, now, now, token)
	return err
}

// RequeueDead gives the workers back every payload they gave up on. A payload dies after
// MaxAttempts, which on a server that was broken for a few minutes (a migration that did not
// run) means real measures sitting in the queue: they are kept, so they can come back.
func (q *Queue) RequeueDead(ctx context.Context) (int64, error) {
	res, err := q.DB.Q().Exec(ctx,
		"UPDATE ingest_queue SET dead = FALSE, attempts = 0, error = NULL, available_at = ? WHERE dead", time.Now().Unix())
	if err != nil {
		return 0, err
	}
	return res.RowsAffected()
}

func (q *Queue) Depth(ctx context.Context) (int, error) {
	var n int
	err := q.DB.Q().Row(ctx, "SELECT COUNT(*) FROM ingest_queue WHERE NOT dead").Scan(&n)
	return n, err
}

type job struct {
	id, projectID int64
	payload       []byte
	gzipped       bool
	attempts      int
	tokenID       sql.NullInt64
}

// claim locks up to n ready jobs for this worker.
func (q *Queue) claim(ctx context.Context, n int) ([]job, error) {
	now := q.Now().Unix()
	var jobs []job
	scan := func(rows *sql.Rows) error {
		defer rows.Close()
		for rows.Next() {
			var j job
			if err := rows.Scan(&j.id, &j.projectID, &j.payload, &j.gzipped, &j.attempts, &j.tokenID); err != nil {
				return err
			}
			jobs = append(jobs, j)
		}
		return rows.Err()
	}

	if q.DB.Dialect == store.Postgres {
		rows, err := q.DB.Q().Query(ctx, `UPDATE ingest_queue SET attempts = attempts + 1, available_at = ?
			WHERE id IN (SELECT id FROM ingest_queue WHERE NOT dead AND available_at <= ? ORDER BY id LIMIT ? FOR UPDATE SKIP LOCKED)
			RETURNING id, project_id, payload, gzipped, attempts, token_id`, now+int64(claimTimeout.Seconds()), now, n)
		if err != nil {
			return nil, err
		}
		return jobs, scan(rows)
	}

	q.mu.Lock()
	defer q.mu.Unlock()
	err := q.DB.Tx(ctx, func(tx store.Q) error {
		rows, err := tx.Query(ctx, `UPDATE ingest_queue SET attempts = attempts + 1, available_at = ?
			WHERE id IN (SELECT id FROM ingest_queue WHERE NOT dead AND available_at <= ? ORDER BY id LIMIT ?)
			RETURNING id, project_id, payload, gzipped, attempts, token_id`, now+int64(claimTimeout.Seconds()), now, n)
		if err != nil {
			return err
		}
		return scan(rows)
	})
	return jobs, err
}

func (q *Queue) done(ctx context.Context, id int64) error {
	_, err := q.DB.Q().Exec(ctx, "DELETE FROM ingest_queue WHERE id = ?", id)
	return err
}

func (q *Queue) fail(ctx context.Context, j job, cause error) error {
	msg := cause.Error()
	if len(msg) > 1000 {
		msg = msg[:1000]
	}
	if j.attempts >= MaxAttempts {
		_, err := q.DB.Q().Exec(ctx, "UPDATE ingest_queue SET dead = TRUE, error = ? WHERE id = ?", msg, j.id)
		return err
	}
	backoff := time.Duration(1<<j.attempts) * 5 * time.Second
	_, err := q.DB.Q().Exec(ctx, "UPDATE ingest_queue SET available_at = ?, error = ? WHERE id = ?", q.Now().Add(backoff).Unix(), msg, j.id)
	return err
}

// PruneDead removes dead payloads older than a week.
func (q *Queue) PruneDead(ctx context.Context) error {
	_, err := q.DB.Q().Exec(ctx, "DELETE FROM ingest_queue WHERE dead AND received_at < ?", q.Now().Add(-7*24*time.Hour).Unix())
	return err
}

type Worker struct {
	// Processed counts payloads written by this instance (load tests, status).
	Processed atomic.Int64
	Failed    atomic.Int64
	Queue     *Queue
	Service   *Service
	projects  project.Repo
	mu        sync.Mutex
	cache     map[int64]cachedProject
}

type cachedProject struct {
	p  project.Project
	at time.Time
}

func NewWorker(q *Queue, s *Service) *Worker {
	return &Worker{Queue: q, Service: s, projects: project.Repo{DB: s.db}, cache: map[int64]cachedProject{}}
}

func (w *Worker) project(ctx context.Context, id int64) (project.Project, error) {
	w.mu.Lock()
	c, ok := w.cache[id]
	w.mu.Unlock()
	if ok && time.Since(c.at) < 30*time.Second {
		return c.p, nil
	}
	p, err := w.projects.ByID(ctx, id)
	if err != nil {
		return p, err
	}
	w.mu.Lock()
	w.cache[id] = cachedProject{p, time.Now()}
	w.mu.Unlock()
	return p, nil
}

func decodePayload(j job) (protocol.Payload, error) {
	var r io.Reader = bytes.NewReader(j.payload)
	if j.gzipped {
		zr, err := gzip.NewReader(r)
		if err != nil {
			return protocol.Payload{}, fmt.Errorf("gzip: %w", err)
		}
		defer zr.Close()
		r = io.LimitReader(zr, 200<<20)
	}
	var p protocol.Payload
	if err := json.NewDecoder(r).Decode(&p); err != nil {
		return p, fmt.Errorf("json: %w", err)
	}
	return p, nil
}

// ProcessBatch handles up to n jobs and returns how many it claimed.
func (w *Worker) ProcessBatch(ctx context.Context, n int) (int, error) {
	jobs, err := w.Queue.claim(ctx, n)
	if err != nil {
		return 0, err
	}
	for _, j := range jobs {
		err := w.process(ctx, j)
		if err == nil {
			w.Processed.Add(1)
			if err := w.Queue.done(ctx, j.id); err != nil {
				return len(jobs), err
			}
			continue
		}
		if errors.Is(err, project.ErrNotFound) {
			_ = w.Queue.done(ctx, j.id)
			continue
		}
		w.Failed.Add(1)
		slog.Warn("ingest: payload failed", "id", j.id, "attempt", j.attempts, "error", err)
		if err := w.Queue.fail(ctx, j, err); err != nil {
			return len(jobs), err
		}
	}
	return len(jobs), nil
}

func (w *Worker) process(ctx context.Context, j job) error {
	p, err := w.project(ctx, j.projectID)
	if err != nil {
		return err
	}
	payload, err := decodePayload(j)
	if err != nil {
		return err
	}
	if j.tokenID.Valid {
		ctx = WithTokenID(ctx, j.tokenID.Int64)
	}
	_, err = w.Service.Write(ctx, p, payload)
	return err
}

// Run starts `concurrency` workers polling the queue until the context ends.
func (w *Worker) Run(ctx context.Context, concurrency int, idle time.Duration) {
	var wg sync.WaitGroup
	for i := 0; i < max(1, concurrency); i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for ctx.Err() == nil {
				n, err := w.ProcessBatch(ctx, 20)
				if err != nil && ctx.Err() == nil {
					slog.Error("ingest worker", "error", err)
				}
				if n == 0 || err != nil {
					select {
					case <-ctx.Done():
					case <-time.After(idle):
					}
				}
			}
		}()
	}
	wg.Wait()
}

// Drain processes everything ready (tests, CLI).
func (w *Worker) Drain(ctx context.Context) error {
	for {
		n, err := w.ProcessBatch(ctx, 50)
		if err != nil || n == 0 {
			return err
		}
	}
}
