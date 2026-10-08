package dispatch

import (
	"context"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"
	"uuid"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/aniruddha81/task-queue/gen/taskqueue/worker/v1/workerv1connect"
	"github.com/aniruddha81/task-queue/internal/pgtest"
	"github.com/aniruddha81/task-queue/internal/queue"
	"github.com/aniruddha81/task-queue/sdk/go/worker"
)

// startDispatch runs a real dispatch server (HTTP/2 without TLS, like production until week 6).
func startDispatch(t *testing.T) (*pgxpool.Pool, string) {
	pool := pgtest.Migrated(t, "jobs")
	srv := New(queue.NewStore(pool), slog.New(slog.DiscardHandler))
	ctx, cancel := context.WithCancel(context.Background())
	go srv.Listen(ctx, pool)
	_, h := workerv1connect.NewWorkerServiceHandler(srv)
	hs := httptest.NewUnstartedServer(h)
	hs.Config.Protocols = new(http.Protocols)
	hs.Config.Protocols.SetHTTP1(true)
	hs.Config.Protocols.SetUnencryptedHTTP2(true)
	hs.Start()
	t.Cleanup(func() { hs.Close(); cancel() })
	return pool, hs.URL
}

// TestConcurrentWorkersRunEachJobOnce is week 3's acceptance test: 5 workers race over
// 1,000 jobs, and every job is claimed and run exactly once.
func TestConcurrentWorkersRunEachJobOnce(t *testing.T) {
	const nJobs, nWorkers = 1000, 5
	pool, url := startDispatch(t)
	_, err := pool.Exec(t.Context(), `
		INSERT INTO jobs (owner_id, idempotency_key, request, queue, type, payload)
		SELECT '00000000-0000-7000-8000-000000000001', i::text, '{}', 'default', 'test.count', '{}'
		FROM generate_series(1, $1) AS i`, nJobs)
	if err != nil {
		t.Fatal(err)
	}

	var mu sync.Mutex
	runs := map[string]int{}
	count := func(_ context.Context, j worker.Job) error {
		mu.Lock()
		runs[j.ID]++
		mu.Unlock()
		return nil
	}
	ctx, stop := context.WithCancel(context.Background())
	var workers sync.WaitGroup
	for i := range nWorkers {
		w := worker.New(worker.Config{
			Dispatchers: []string{url}, Cloud: "aws", Queues: []string{"default"},
			Name: string(rune('a' + i)), Concurrency: 8, Log: slog.New(slog.DiscardHandler),
		})
		w.Handle("test.count", count)
		workers.Go(func() { w.Run(ctx) })
	}

	deadline := time.Now().Add(60 * time.Second)
	for {
		var done int
		pool.QueryRow(t.Context(), `SELECT count(*) FROM jobs WHERE state = 'succeeded'`).Scan(&done)
		if done == nJobs {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("only %d/%d jobs succeeded in 60 s", done, nJobs)
		}
		time.Sleep(100 * time.Millisecond)
	}
	stop()
	workers.Wait() // graceful drain returns once in-flight jobs finish

	if len(runs) != nJobs {
		t.Errorf("%d distinct jobs ran, want %d", len(runs), nJobs)
	}
	for id, n := range runs {
		if n != 1 {
			t.Errorf("job %s ran %d times, want 1", id, n)
		}
	}
	var attempts, multi int
	pool.QueryRow(t.Context(), `SELECT count(*) FROM job_attempts`).Scan(&attempts)
	pool.QueryRow(t.Context(), `SELECT count(*) FROM jobs WHERE attempt <> 1`).Scan(&multi)
	if attempts != nJobs || multi != 0 {
		t.Errorf("attempts = %d (want %d), jobs with attempt != 1: %d", attempts, nJobs, multi)
	}
	var nodes int
	pool.QueryRow(t.Context(), `SELECT count(*) FROM nodes`).Scan(&nodes)
	if nodes != nWorkers {
		t.Errorf("nodes registry has %d workers, want %d", nodes, nWorkers)
	}
}

// TestCancelStopsRunningHandler: a cancel reaches a running handler through the heartbeat,
// the handler stops, and the job ends cancelled.
func TestCancelStopsRunningHandler(t *testing.T) {
	pool, url := startDispatch(t)
	store := queue.NewStore(pool)
	owner := mustUUID(t, "00000000-0000-7000-8000-000000000002")
	job, _, err := store.Submit(t.Context(), queue.NewJob{Owner: owner, Key: "slow", Request: []byte(`{}`),
		Queue: "default", Type: "test.block", Payload: []byte(`{}`), MaxAttempts: 5, TimeoutSeconds: 600})
	if err != nil {
		t.Fatal(err)
	}

	started := make(chan struct{})
	w := worker.New(worker.Config{Dispatchers: []string{url}, Cloud: "aws", Queues: []string{"default"},
		HeartbeatEvery: 100 * time.Millisecond, Log: slog.New(slog.DiscardHandler)})
	w.Handle("test.block", func(ctx context.Context, _ worker.Job) error {
		close(started)
		<-ctx.Done() // a well-behaved handler stops when its context ends
		return ctx.Err()
	})
	ctx, stop := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { w.Run(ctx); close(done) }()
	defer func() { stop(); <-done }()

	<-started
	if _, err := store.Cancel(t.Context(), owner, job.ID); err != nil {
		t.Fatal(err)
	}
	for range 50 {
		if got, _ := store.Get(t.Context(), owner, job.ID); got.State == "cancelled" {
			return
		}
		time.Sleep(100 * time.Millisecond)
	}
	t.Fatal("job was not cancelled within 5 s")
}

func mustUUID(t *testing.T, s string) uuid.UUID {
	t.Helper()
	u, err := uuid.Parse(s)
	if err != nil {
		t.Fatal(err)
	}
	return u
}
