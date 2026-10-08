package scheduler

import (
	"context"
	"errors"
	"log/slog"
	"sync"
	"testing"
	"time"
	"uuid"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/aniruddha81/task-queue/internal/pgtest"
	"github.com/aniruddha81/task-queue/internal/queue"
)

var quiet = slog.New(slog.DiscardHandler)

func leader(t *testing.T, pool *pgxpool.Pool) (holder uuid.UUID, epoch int64) {
	t.Helper()
	var h *uuid.UUID
	pool.QueryRow(t.Context(), `SELECT holder, epoch FROM leader_leases WHERE name = 'scheduler' AND expires_at > now()`).Scan(&h, &epoch)
	if h != nil {
		holder = *h
	}
	return holder, epoch
}

func waitFor(t *testing.T, what string, limit time.Duration, ok func() bool) time.Duration {
	t.Helper()
	start := time.Now()
	for !ok() {
		if time.Since(start) > limit {
			t.Fatalf("%s: not within %v", what, limit)
		}
		time.Sleep(5 * time.Millisecond)
	}
	return time.Since(start)
}

func start(s *Scheduler) (stop func()) {
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { s.Run(ctx); close(done) }()
	return func() { cancel(); <-done }
}

func TestHandoverWhenLeaderDies(t *testing.T) {
	pool := pgtest.Migrated(t, "jobs")
	cfg := Config{TTL: 600 * time.Millisecond, Every: 10 * time.Millisecond}
	a, b := New(pool, quiet, cfg), New(pool, quiet, cfg)

	stopA := start(a)
	waitFor(t, "a leads", 2*time.Second, func() bool { h, _ := leader(t, pool); return h == a.me })
	stopB := start(b)
	defer stopB()

	stopA() // a crash: no Resign, so b must wait for the lease to expire
	took := waitFor(t, "b leads", 3*time.Second, func() bool { h, _ := leader(t, pool); return h == b.me })
	if limit := cfg.TTL + cfg.TTL/3 + 100*time.Millisecond; took > limit {
		t.Errorf("handover took %v, want within the lease TTL plus one campaign interval (%v)", took, limit)
	}
}

func TestResignHandsOverAtOnce(t *testing.T) {
	pool := pgtest.Migrated(t, "jobs")
	cfg := Config{TTL: 10 * time.Second, Every: 10 * time.Millisecond}
	a, b := New(pool, quiet, cfg), New(pool, quiet, cfg)
	stopA := start(a)
	waitFor(t, "a leads", 2*time.Second, func() bool { h, _ := leader(t, pool); return h == a.me })
	stopB := start(b)
	defer stopB()

	stopA()
	a.Resign(t.Context())
	// Far sooner than the 10 s TTL: b's next campaign (every TTL/3) takes over.
	waitFor(t, "b leads after resign", cfg.TTL/3+time.Second, func() bool { h, _ := leader(t, pool); return h == b.me })
}

// TestPausedLeaderIsFenced: a leader that was paused past its lease resumes and tries a
// chore with its old epoch. The chore must be rejected and change nothing.
func TestPausedLeaderIsFenced(t *testing.T) {
	pool := pgtest.Migrated(t, "jobs")
	cfg := Config{TTL: time.Minute}
	a, b := New(pool, quiet, cfg), New(pool, quiet, cfg)

	old, err := a.acquire(t.Context())
	if err != nil || old == 0 {
		t.Fatalf("a acquire: %d %v", old, err)
	}
	// a "pauses"; its lease expires; b takes over.
	pool.Exec(t.Context(), `UPDATE leader_leases SET expires_at = now() - interval '1 second'`)
	if e, _ := b.acquire(t.Context()); e != old+1 {
		t.Fatalf("b epoch = %d, want %d", e, old+1)
	}

	ran := false
	_, err = a.chore(t.Context(), old, "test", func(*queue.Store) (int, error) { ran = true; return 1, nil })
	if !errors.Is(err, ErrNotLeader) || ran {
		t.Errorf("stale chore: err=%v ran=%v, want ErrNotLeader and not run", err, ran)
	}
	if held, _ := a.renew(t.Context(), old); held {
		t.Error("stale leader renewed a lease it no longer holds")
	}
}

// TestCronExactlyOnceAcrossForcedHandovers is week 5's acceptance test: a schedule 3 hours
// behind is caught up one tick per round while leadership is forced to change 100 times.
// Every tick must produce exactly one job, none may be skipped, and chores of different
// epochs must never overlap.
func TestCronExactlyOnceAcrossForcedHandovers(t *testing.T) {
	pool := pgtest.Migrated(t, "jobs")
	store := queue.NewStore(pool)
	owner := uuid.NewV7()
	c, _ := queue.ParseCron("* * * * *", "Asia/Kolkata")
	sc, err := store.CreateSchedule(t.Context(), owner, "every-minute", c, "* * * * *", "Asia/Kolkata",
		[]byte(`{"queue":"default","type":"report.daily"}`))
	if err != nil {
		t.Fatal(err)
	}
	var first time.Time
	pool.QueryRow(t.Context(), `UPDATE schedules SET next_tick_at = date_trunc('minute', now()) - interval '180 minutes'
		WHERE id = $1 RETURNING next_tick_at`, sc.ID).Scan(&first)

	cfg := Config{TTL: 300 * time.Millisecond, Every: 2 * time.Millisecond, TickBatch: 1}
	var stops []func()
	for range 2 {
		stops = append(stops, start(New(pool, quiet, cfg)))
	}

	for i := range 100 {
		_, before := leader(t, pool)
		pool.Exec(t.Context(), `UPDATE leader_leases SET expires_at = now() - interval '1 second'`)
		waitFor(t, "handover", 5*time.Second, func() bool { _, e := leader(t, pool); return e > before })
		if i%10 == 0 {
			time.Sleep(10 * time.Millisecond) // let some ticks happen between handovers
		}
	}
	waitFor(t, "catch-up", 30*time.Second, func() bool {
		var due bool
		pool.QueryRow(t.Context(), `SELECT next_tick_at <= now() FROM schedules WHERE id = $1`, sc.ID).Scan(&due)
		return !due
	})
	for _, stop := range stops {
		stop()
	}

	var next time.Time
	pool.QueryRow(t.Context(), `SELECT next_tick_at FROM schedules WHERE id = $1`, sc.ID).Scan(&next)
	var want []time.Time
	for at := first; at.Before(next); at = c.Next(at) {
		want = append(want, at)
	}
	rows, _ := pool.Query(t.Context(), `SELECT tick_at FROM schedule_ticks WHERE schedule_id = $1 ORDER BY tick_at`, sc.ID)
	var got []time.Time
	for rows.Next() {
		var at time.Time
		rows.Scan(&at)
		got = append(got, at)
	}
	if len(got) != len(want) || len(want) < 180 {
		t.Fatalf("%d ticks, want %d (>= 180)", len(got), len(want))
	}
	for i := range want {
		if !got[i].Equal(want[i]) {
			t.Fatalf("tick %d = %v, want %v (skipped or extra tick)", i, got[i], want[i])
		}
	}

	var jobs, epochs, overlaps int
	pool.QueryRow(t.Context(), `SELECT count(*) FROM jobs WHERE idempotency_key LIKE 'cron:%'`).Scan(&jobs)
	pool.QueryRow(t.Context(), `SELECT epoch FROM leader_leases`).Scan(&epochs)
	pool.QueryRow(t.Context(), `SELECT count(*) FROM chore_runs a JOIN chore_runs b
		ON a.epoch < b.epoch AND a.committed_at > b.started_at`).Scan(&overlaps)
	if jobs != len(want) {
		t.Errorf("%d cron jobs for %d ticks, want one each", jobs, len(want))
	}
	if epochs < 100 {
		t.Errorf("only %d leadership epochs, want >= 100 handovers", epochs)
	}
	if overlaps != 0 {
		t.Errorf("%d pairs of chores from different epochs overlapped (G7)", overlaps)
	}
}

// TestTwoSchedulersWithoutFencingStillNoDuplicateTicks: even with leadership bypassed,
// two processes ticking the same schedule can't create a second job for a tick.
func TestTickSchedulesConcurrentlyNoDuplicates(t *testing.T) {
	pool := pgtest.Migrated(t, "jobs")
	store := queue.NewStore(pool)
	c, _ := queue.ParseCron("* * * * *", "UTC")
	sc, _ := store.CreateSchedule(t.Context(), uuid.NewV7(), "s", c, "* * * * *", "UTC",
		[]byte(`{"queue":"default","type":"report.daily"}`))
	pool.Exec(t.Context(), `UPDATE schedules SET next_tick_at = date_trunc('minute', now()) - interval '60 minutes' WHERE id = $1`, sc.ID)

	var wg sync.WaitGroup
	for range 4 {
		wg.Go(func() {
			for {
				tx, err := pool.Begin(t.Context())
				if err != nil {
					t.Error(err)
					return
				}
				n, err := queue.NewStore(tx).TickSchedules(t.Context(), 3)
				if err != nil {
					tx.Rollback(t.Context())
					t.Error(err)
					return
				}
				tx.Commit(t.Context())
				if n == 0 {
					return
				}
			}
		})
	}
	wg.Wait()
	var ticks, distinct int
	pool.QueryRow(t.Context(), `SELECT count(*), count(DISTINCT tick_at) FROM schedule_ticks`).Scan(&ticks, &distinct)
	if ticks != distinct || ticks < 60 {
		t.Errorf("ticks=%d distinct=%d, want equal and >= 60", ticks, distinct)
	}
}
