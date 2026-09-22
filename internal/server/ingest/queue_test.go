package ingest

import (
	"bytes"
	"database/sql"
	"compress/gzip"
	"context"
	"encoding/json"
	"fmt"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/getslowpoke/slowpoke/internal/protocol"
)

func gz(t testing.TB, v any) []byte {
	var buf bytes.Buffer
	zw := gzip.NewWriter(&buf)
	_ = json.NewEncoder(zw).Encode(v)
	_ = zw.Close()
	return buf.Bytes()
}

func TestQueueProcessesEveryPayloadOnceWithConcurrentWorkers(t *testing.T) {
	svc, p, ctx := setup(t)
	svc.Now = time.Now
	q := NewQueue(svc.db)
	minute := time.Now().Add(-10*time.Minute).Unix() / 60 * 60

	const payloads = 60
	for i := 0; i < payloads; i++ {
		body := gz(t, protocol.Payload{BatchID: fmt.Sprintf("q%d", i), Agent: agentInfo("nginx:app"),
			HTTP: []protocol.HTTPBucket{{Source: "nginx:app", Minute: minute, N: 1, Hist: make([]int, 15)}}})
		if err := q.Enqueue(ctx, p.ID, body, true); err != nil {
			t.Fatal(err)
		}
	}
	if depth, _ := q.Depth(ctx); depth != payloads {
		t.Fatalf("depth %d", depth)
	}

	w := NewWorker(q, svc)
	var wg sync.WaitGroup
	for i := 0; i < 4; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for {
				n, err := w.ProcessBatch(ctx, 7)
				if err != nil {
					t.Error(err)
					return
				}
				if n == 0 {
					return
				}
			}
		}()
	}
	wg.Wait()

	var n int
	_ = svc.db.Q().Row(ctx, "SELECT n FROM http_minutes WHERE project_id = ?", p.ID).Scan(&n)
	if n != payloads {
		t.Fatalf("every payload must count exactly once, got %d", n)
	}
	if depth, _ := q.Depth(ctx); depth != 0 {
		t.Fatalf("queue not empty: %d", depth)
	}
}

func TestBrokenPayloadsAreRetriedThenDead(t *testing.T) {
	svc, p, ctx := setup(t)
	q := NewQueue(svc.db)
	now := time.Unix(1_800_000_000, 0)
	q.Now = func() time.Time { return now }
	if err := q.Enqueue(ctx, p.ID, []byte("{not json"), false); err != nil {
		t.Fatal(err)
	}
	w := NewWorker(q, svc)
	for i := 0; i < MaxAttempts; i++ {
		if _, err := w.ProcessBatch(ctx, 10); err != nil {
			t.Fatal(err)
		}
		now = now.Add(time.Hour) // past any backoff
	}
	var dead int
	var msg string
	_ = svc.db.Q().Row(ctx, "SELECT COUNT(*), MAX(error) FROM ingest_queue WHERE dead").Scan(&dead, &msg)
	if dead != 1 || msg == "" {
		t.Fatalf("dead=%d error=%q", dead, msg)
	}
	if depth, _ := q.Depth(ctx); depth != 0 {
		t.Fatal("dead payloads do not count in the depth")
	}
}

func TestWorkerRunStopsWithTheContext(t *testing.T) {
	svc, _, _ := setup(t)
	w := NewWorker(NewQueue(svc.db), svc)
	ctx, cancel := context.WithCancel(context.Background())
	var done atomic.Bool
	go func() {
		w.Run(ctx, 2, 10*time.Millisecond)
		done.Store(true)
	}()
	time.Sleep(50 * time.Millisecond)
	cancel()
	time.Sleep(100 * time.Millisecond)
	if !done.Load() {
		t.Fatal("worker did not stop")
	}
}

// A payload that failed for a reason since fixed (a migration that did not run, on 2026-09-18)
// is not lost: the queue keeps it, and requeueing gives it back to the workers.
func TestDeadPayloadsCanBeRequeued(t *testing.T) {
	svc, p, ctx := setup(t)
	q := NewQueue(svc.db)
	if err := q.EnqueueFrom(ctx, p.ID, 0, []byte(`{"batch_id":"1"}`), false); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.db.Q().Exec(ctx, "UPDATE ingest_queue SET dead = TRUE, error = 'no such column: host_id', attempts = 5"); err != nil {
		t.Fatal(err)
	}
	if depth, _ := q.Depth(ctx); depth != 0 {
		t.Fatalf("a dead payload is not waiting: %d", depth)
	}
	n, err := q.RequeueDead(ctx)
	if err != nil || n != 1 {
		t.Fatalf("requeued %d: %v", n, err)
	}
	if depth, _ := q.Depth(ctx); depth != 1 {
		t.Fatalf("the payload is back in the queue: %d", depth)
	}
	var attempts int
	var errText sql.NullString
	_ = svc.db.Q().Row(ctx, "SELECT attempts, error FROM ingest_queue").Scan(&attempts, &errText)
	if attempts != 0 || errText.Valid {
		t.Fatalf("it starts again from zero: attempts=%d error=%v", attempts, errText)
	}
}
