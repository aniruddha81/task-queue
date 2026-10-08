package queue

import (
	"errors"
	"sync"
	"testing"
	"time"
	"uuid"

	"github.com/jackc/pgx/v5/pgxpool"
)

// due skips a job's backoff delay so tests needn't wait for it.
func due(t *testing.T, pool *pgxpool.Pool) {
	t.Helper()
	if _, err := pool.Exec(t.Context(), `UPDATE jobs SET run_at = now() WHERE state = 'available'`); err != nil {
		t.Fatal(err)
	}
}

// expire makes every running lease expire now, as if each worker had crashed.
func expire(t *testing.T, pool *pgxpool.Pool) {
	t.Helper()
	if _, err := pool.Exec(t.Context(), `UPDATE jobs SET lease_expires_at = now() - interval '1 second'
		WHERE state = 'running'`); err != nil {
		t.Fatal(err)
	}
}

func outcomes(t *testing.T, pool *pgxpool.Pool, id uuid.UUID) []string {
	t.Helper()
	rows, _ := pool.Query(t.Context(), `SELECT coalesce(outcome, 'none') FROM job_attempts WHERE job_id = $1 ORDER BY attempt`, id)
	var out []string
	for rows.Next() {
		var o string
		rows.Scan(&o)
		out = append(out, o)
	}
	return out
}

func TestFailingForeverEndsDeadAfterExactlyMaxAttempts(t *testing.T) {
	s, pool := newStore(t)
	owner := uuid.NewV7()
	j := newJob(owner, "k", `{}`)
	j.MaxAttempts = 3
	job, _, _ := s.Submit(t.Context(), j)

	for i := 1; i <= 3; i++ {
		c := claimOne(t, s, uuid.NewV7())
		if c.Attempt != i {
			t.Fatalf("attempt = %d, want %d", c.Attempt, i)
		}
		if err := s.Fail(t.Context(), Lease{c.ID, c.LeaseToken}, "boom", false); err != nil {
			t.Fatal(err)
		}
		due(t, pool)
	}
	if got, _ := s.Get(t.Context(), owner, job.ID); got.State != "dead" || got.Attempt != 3 {
		t.Errorf("after 3 failures: state=%s attempt=%d, want dead after 3", got.State, got.Attempt)
	}
	if extra, _ := s.Claim(t.Context(), ClaimParams{Node: uuid.NewV7(), Cloud: "aws", Queues: []string{"default"},
		Types: []string{"email.send"}, Max: 1, Lease: time.Minute}); len(extra) != 0 {
		t.Error("a dead job was claimed again")
	}
}

func TestCrashingEveryTimeEndsDeadAfterExactlyMaxAttempts(t *testing.T) {
	s, pool := newStore(t)
	owner := uuid.NewV7()
	j := newJob(owner, "k", `{}`)
	j.MaxAttempts = 4
	job, _, _ := s.Submit(t.Context(), j)

	for range 4 {
		claimOne(t, s, uuid.NewV7()) // the worker crashes: it never reports
		expire(t, pool)
		if n, err := s.Reap(t.Context(), 100); err != nil || n != 1 {
			t.Fatalf("reap: n=%d err=%v, want 1", n, err)
		}
		due(t, pool)
	}
	got, _ := s.Get(t.Context(), owner, job.ID)
	if got.State != "dead" || got.Attempt != 4 {
		t.Errorf("state=%s attempt=%d, want dead after 4", got.State, got.Attempt)
	}
	want := []string{"lease_expired", "lease_expired", "lease_expired", "lease_expired"}
	if o := outcomes(t, pool, job.ID); len(o) != 4 || o[0] != want[0] || o[3] != want[3] {
		t.Errorf("attempt outcomes = %v, want %v", o, want)
	}
}

func TestTwoReapersNeverRequeueTwice(t *testing.T) {
	const n = 500
	s, pool := newStore(t)
	_, err := pool.Exec(t.Context(), `
		INSERT INTO jobs (owner_id, idempotency_key, request, queue, type, payload)
		SELECT gen_random_uuid(), i::text, '{}', 'default', 'email.send', '{}' FROM generate_series(1, $1) i`, n)
	if err != nil {
		t.Fatal(err)
	}
	for claimed := 0; claimed < n; {
		got, err := s.Claim(t.Context(), ClaimParams{Node: uuid.NewV7(), Cloud: "aws", Queues: []string{"default"},
			Types: []string{"email.send"}, Max: 100, Lease: time.Minute})
		if err != nil {
			t.Fatal(err)
		}
		claimed += len(got)
	}
	expire(t, pool)

	var mu sync.Mutex
	total := 0
	var wg sync.WaitGroup
	for range 2 {
		wg.Go(func() {
			for {
				k, err := s.Reap(t.Context(), 7) // small batches maximise interleaving
				if err != nil {
					t.Error(err)
					return
				}
				mu.Lock()
				total += k
				mu.Unlock()
				if k == 0 {
					return
				}
			}
		})
	}
	wg.Wait()
	if total != n {
		t.Errorf("reaped %d times, want exactly %d", total, n)
	}
	var available int
	pool.QueryRow(t.Context(), `SELECT count(*) FROM jobs WHERE state = 'available' AND attempt = 1`).Scan(&available)
	if available != n {
		t.Errorf("%d jobs requeued once, want %d", available, n)
	}
}

func TestReaperSparesLiveLeases(t *testing.T) {
	s, _ := newStore(t)
	s.Submit(t.Context(), newJob(uuid.NewV7(), "k", `{}`))
	c := claimOne(t, s, uuid.NewV7())
	if n, _ := s.Reap(t.Context(), 100); n != 0 {
		t.Fatal("reaped a lease that had not expired")
	}
	if err := s.Complete(t.Context(), Lease{c.ID, c.LeaseToken}); err != nil {
		t.Errorf("complete after a no-op reap: %v", err)
	}
}

func TestPriorityOrder(t *testing.T) {
	s, _ := newStore(t)
	owner := uuid.NewV7()
	for i, p := range []int{0, 5, -3, 10, 5} {
		j := newJob(owner, string(rune('a'+i)), `{}`)
		j.Priority = p
		s.Submit(t.Context(), j)
	}
	var got []int
	for range 5 {
		c := claimOne(t, s, uuid.NewV7())
		job, _ := s.Get(t.Context(), owner, c.ID)
		got = append(got, job.Priority)
	}
	want := []int{10, 5, 5, 0, -3}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("claim order by priority = %v, want %v", got, want)
		}
	}
}

func TestDelayedJobWaitsForRunAt(t *testing.T) {
	s, pool := newStore(t)
	j := newJob(uuid.NewV7(), "k", `{}`)
	later := time.Now().Add(time.Hour)
	j.RunAt = &later
	job, _, _ := s.Submit(t.Context(), j)

	p := ClaimParams{Node: uuid.NewV7(), Cloud: "aws", Queues: []string{"default"}, Types: []string{"email.send"}, Max: 1, Lease: time.Minute}
	if got, _ := s.Claim(t.Context(), p); len(got) != 0 {
		t.Fatal("claimed a job before its run_at")
	}
	pool.Exec(t.Context(), `UPDATE jobs SET run_at = now() - interval '1 second' WHERE id = $1`, job.ID)
	if got, _ := s.Claim(t.Context(), p); len(got) != 1 {
		t.Fatal("job not claimable once run_at passed")
	}
}

func TestBackoff(t *testing.T) {
	s, pool := newStore(t)
	owner := uuid.NewV7()
	job, _, _ := s.Submit(t.Context(), newJob(owner, "k", `{}`))
	c := claimOne(t, s, uuid.NewV7())
	s.Fail(t.Context(), Lease{c.ID, c.LeaseToken}, "x", false)

	var inRange, capped bool
	pool.QueryRow(t.Context(), `SELECT run_at >= updated_at AND run_at < updated_at + interval '1 second'
		FROM jobs WHERE id = $1`, job.ID).Scan(&inRange)
	pool.QueryRow(t.Context(), `SELECT bool_and(retry_backoff(a) <= interval '10 minutes') FROM generate_series(1, 1000) a`).Scan(&capped) // every attempt, no overflow
	if !inRange {
		t.Error("first retry not scheduled within [0, 1 s)")
	}
	if !capped {
		t.Error("backoff exceeds its 10-minute cap")
	}
}

func TestRedrive(t *testing.T) {
	s, _ := newStore(t)
	owner := uuid.NewV7()
	j := newJob(owner, "k", `{"max_attempts":2}`)
	j.MaxAttempts = 2
	job, _, _ := s.Submit(t.Context(), j)

	if _, err := s.Redrive(t.Context(), owner, job.ID); !errors.Is(err, ErrNotDead) {
		t.Errorf("redrive of an available job: %v, want ErrNotDead", err)
	}
	c := claimOne(t, s, uuid.NewV7())
	s.Fail(t.Context(), Lease{c.ID, c.LeaseToken}, "bad input", true) // permanent: dead at once

	if _, err := s.Redrive(t.Context(), uuid.NewV7(), job.ID); !errors.Is(err, ErrNotFound) {
		t.Errorf("another owner's redrive: %v, want ErrNotFound", err)
	}
	got, err := s.Redrive(t.Context(), owner, job.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.State != "available" || got.Attempt != 1 || got.MaxAttempts != 3 {
		t.Errorf("after redrive: state=%s attempt=%d max=%d, want available, 1, 3 (1 used + budget 2)",
			got.State, got.Attempt, got.MaxAttempts)
	}
	if c2 := claimOne(t, s, uuid.NewV7()); c2.Attempt != 2 {
		t.Errorf("next attempt = %d, want 2 (attempt numbers are never reused)", c2.Attempt)
	}
}
