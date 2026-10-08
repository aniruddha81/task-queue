package queue

import (
	"encoding/json"
	"errors"
	"sync"
	"testing"
	"uuid"

	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/aniruddha81/task-queue/internal/pgtest"
)

// newStore returns a Store on a fresh, fully migrated jobs database.
func newStore(t *testing.T) (*Store, *pgxpool.Pool) {
	pool := pgtest.Migrated(t, "jobs")
	return NewStore(pool), pool
}

func newJob(owner uuid.UUID, key, body string) NewJob {
	return NewJob{
		Owner: owner, Key: key, Request: json.RawMessage(body),
		Queue: "default", Type: "email.send", Payload: json.RawMessage(`{}`),
		MaxAttempts: 5, TimeoutSeconds: 60,
	}
}

func TestSubmitConcurrentSameKeyCreatesOneJob(t *testing.T) {
	s, pool := newStore(t)
	owner := uuid.NewV7()
	j := newJob(owner, "k1", `{"queue":"default","type":"email.send"}`)

	const n = 50
	ids := make([]uuid.UUID, n)
	created := make([]bool, n)
	var wg sync.WaitGroup
	for i := range n {
		wg.Go(func() {
			job, c, err := s.Submit(t.Context(), j)
			if err != nil {
				t.Error(err)
			}
			ids[i], created[i] = job.ID, c
		})
	}
	wg.Wait()

	nCreated := 0
	for i := range n {
		if created[i] {
			nCreated++
		}
		if ids[i] != ids[0] {
			t.Fatalf("submit %d got job %v, submit 0 got %v", i, ids[i], ids[0])
		}
	}
	if nCreated != 1 {
		t.Errorf("created = %d, want exactly 1", nCreated)
	}
	var rows int
	pool.QueryRow(t.Context(), `SELECT count(*) FROM jobs WHERE owner_id = $1`, owner).Scan(&rows)
	if rows != 1 {
		t.Errorf("rows = %d, want 1", rows)
	}
}

func TestSubmitKeyReuse(t *testing.T) {
	s, _ := newStore(t)
	owner := uuid.NewV7()
	first, _, err := s.Submit(t.Context(), newJob(owner, "k", `{"a":1,"b":2}`))
	if err != nil {
		t.Fatal(err)
	}

	// Same JSON, different key order and whitespace: the same request.
	again, created, err := s.Submit(t.Context(), newJob(owner, "k", `{ "b": 2, "a": 1 }`))
	if err != nil || created || again.ID != first.ID {
		t.Errorf("equivalent repeat: id=%v created=%v err=%v, want original job", again.ID, created, err)
	}
	// Same key, different request.
	if _, _, err := s.Submit(t.Context(), newJob(owner, "k", `{"a":1,"b":3}`)); !errors.Is(err, ErrKeyReused) {
		t.Errorf("changed request: err = %v, want ErrKeyReused", err)
	}
}

func TestOwnerIsolation(t *testing.T) {
	s, _ := newStore(t)
	alice, bob := uuid.NewV7(), uuid.NewV7()
	job, _, err := s.Submit(t.Context(), newJob(alice, "k", `{}`))
	if err != nil {
		t.Fatal(err)
	}

	if _, err := s.Get(t.Context(), bob, job.ID); !errors.Is(err, ErrNotFound) {
		t.Errorf("bob Get: err = %v, want ErrNotFound", err)
	}
	if _, err := s.Cancel(t.Context(), bob, job.ID); !errors.Is(err, ErrNotFound) {
		t.Errorf("bob Cancel: err = %v, want ErrNotFound", err)
	}
	if jobs, _ := s.List(t.Context(), bob, Filter{Limit: 10}); len(jobs) != 0 {
		t.Errorf("bob List: %d jobs, want 0", len(jobs))
	}
	// Keys are per owner: bob's "k" is a different job.
	bobs, created, err := s.Submit(t.Context(), newJob(bob, "k", `{"other":true}`))
	if err != nil || !created || bobs.ID == job.ID {
		t.Errorf("bob same key: created=%v err=%v, want a new job", created, err)
	}
}

// TestCancel covers cancel from every state.
func TestCancel(t *testing.T) {
	tests := []struct {
		from          string
		wantState     string
		wantRequested bool
		wantErr       error
	}{
		{"available", "cancelled", false, nil},
		{"running", "running", true, nil}, // the worker stops on its next heartbeat
		{"cancelled", "cancelled", false, nil},
		{"succeeded", "succeeded", false, ErrNotCancellable},
		{"dead", "dead", false, ErrNotCancellable},
	}
	s, pool := newStore(t)
	owner := uuid.NewV7()
	for _, tt := range tests {
		t.Run(tt.from, func(t *testing.T) {
			job, _, err := s.Submit(t.Context(), newJob(owner, "cancel-"+tt.from, `{}`))
			if err != nil {
				t.Fatal(err)
			}
			setState(t, pool, job.ID, tt.from)

			got, err := s.Cancel(t.Context(), owner, job.ID)
			if !errors.Is(err, tt.wantErr) {
				t.Fatalf("err = %v, want %v", err, tt.wantErr)
			}
			if got.State != tt.wantState || got.CancelRequested != tt.wantRequested {
				t.Errorf("got state=%s cancel_requested=%v, want %s %v",
					got.State, got.CancelRequested, tt.wantState, tt.wantRequested)
			}
			if tt.wantState == "cancelled" && got.FinishedAt == nil {
				t.Error("cancelled job has no finished_at")
			}
		})
	}
}

// setState forces a job into a state the way later weeks' code will, respecting the
// lease invariant. Only tests do this.
func setState(t *testing.T, pool *pgxpool.Pool, id uuid.UUID, state string) {
	t.Helper()
	q := `UPDATE jobs SET state = $2, finished_at = now() WHERE id = $1`
	switch state {
	case "available":
		return
	case "running":
		q = `UPDATE jobs SET state = $2, attempt = 1, lease_token = gen_random_uuid(),
			lease_expires_at = now() + interval '30 seconds', deadline_at = now() + interval '60 seconds'
			WHERE id = $1`
	}
	if _, err := pool.Exec(t.Context(), q, id, state); err != nil {
		t.Fatal(err)
	}
}

// TestConstraintsRefuseIllegalRows: the database itself rejects rows that break the
// state machine's invariants, whatever code writes them.
func TestConstraintsRefuseIllegalRows(t *testing.T) {
	s, pool := newStore(t)
	owner := uuid.NewV7()
	job, _, err := s.Submit(t.Context(), newJob(owner, "k", `{}`))
	if err != nil {
		t.Fatal(err)
	}
	tests := []struct {
		name, sql string
		code      string // SQLSTATE
	}{
		{"unknown state", `UPDATE jobs SET state = 'paused' WHERE id = $1`, "23514"},
		{"running without a lease", `UPDATE jobs SET state = 'running' WHERE id = $1`, "23514"},
		{"lease while not running", `UPDATE jobs SET lease_token = gen_random_uuid(),
			lease_expires_at = now() WHERE id = $1`, "23514"},
		{"max_attempts 0", `UPDATE jobs SET max_attempts = 0 WHERE id = $1`, "23514"},
		{"timeout 0", `UPDATE jobs SET timeout_seconds = 0 WHERE id = $1`, "23514"},
		{"timeout over an hour", `UPDATE jobs SET timeout_seconds = 3601 WHERE id = $1`, "23514"},
		{"unknown affinity", `UPDATE jobs SET affinity = 'gcp' WHERE id = $1`, "23514"},
		{"duplicate key", `INSERT INTO jobs (owner_id, idempotency_key, request, queue, type, payload)
			SELECT owner_id, idempotency_key, request, queue, type, payload FROM jobs WHERE id = $1`, "23505"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := pool.Exec(t.Context(), tt.sql, job.ID)
			var pgErr *pgconn.PgError
			if !errors.As(err, &pgErr) || pgErr.Code != tt.code {
				t.Errorf("err = %v, want SQLSTATE %s", err, tt.code)
			}
		})
	}
}

func TestListKeysetPagination(t *testing.T) {
	s, _ := newStore(t)
	owner := uuid.NewV7()
	var want []uuid.UUID
	for i := range 5 {
		job, _, err := s.Submit(t.Context(), newJob(owner, string(rune('a'+i)), `{}`))
		if err != nil {
			t.Fatal(err)
		}
		want = append([]uuid.UUID{job.ID}, want...) // newest first
	}

	var got []uuid.UUID
	f := Filter{Limit: 2}
	for {
		page, err := s.List(t.Context(), owner, f)
		if err != nil {
			t.Fatal(err)
		}
		for _, j := range page {
			got = append(got, j.ID)
		}
		if len(page) < f.Limit {
			break
		}
		f.After = &page[len(page)-1].ID
	}
	if len(got) != len(want) {
		t.Fatalf("got %d jobs, want %d", len(got), len(want))
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("page order: got %v, want %v", got, want)
		}
	}
}
