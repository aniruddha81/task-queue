package queue

import (
	"errors"
	"testing"
	"time"
	"uuid"

	"github.com/jackc/pgx/v5/pgxpool"
)

func claimOne(t *testing.T, s *Store, node uuid.UUID, types ...string) Claimed {
	t.Helper()
	if types == nil {
		types = []string{"email.send"}
	}
	got, err := s.Claim(t.Context(), ClaimParams{
		Node: node, Cloud: "aws", Queues: []string{"default"}, Types: types,
		Max: 1, Lease: 30 * time.Second, StealAfter: 30 * time.Second,
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 {
		t.Fatalf("claimed %d jobs, want 1", len(got))
	}
	return got[0]
}

// requeue does what the lease reaper (week 4) will do when a lease expires.
func requeue(t *testing.T, pool *pgxpool.Pool, id uuid.UUID) {
	t.Helper()
	_, err := pool.Exec(t.Context(), `UPDATE jobs SET state = 'available', lease_token = NULL,
		lease_expires_at = NULL, deadline_at = NULL WHERE id = $1`, id)
	if err != nil {
		t.Fatal(err)
	}
}

func TestStaleTokenIsFenced(t *testing.T) {
	s, pool := newStore(t)
	owner := uuid.NewV7()
	job, _, _ := s.Submit(t.Context(), newJob(owner, "k", `{}`))

	a := claimOne(t, s, uuid.NewV7())
	requeue(t, pool, job.ID) // a's lease "expired" while it was paused
	b := claimOne(t, s, uuid.NewV7())
	if a.ID != b.ID || a.LeaseToken == b.LeaseToken || b.Attempt != 2 {
		t.Fatalf("second claim: %+v, want the same job, a new token, attempt 2", b)
	}

	stale := Lease{a.ID, a.LeaseToken}
	if err := s.Complete(t.Context(), stale); !errors.Is(err, ErrLeaseLost) {
		t.Errorf("stale complete: %v, want ErrLeaseLost", err)
	}
	if err := s.Fail(t.Context(), stale, "x", false); !errors.Is(err, ErrLeaseLost) {
		t.Errorf("stale fail: %v, want ErrLeaseLost", err)
	}
	if st, _ := s.Heartbeat(t.Context(), []Lease{stale}, 30*time.Second); st[0] != Lost {
		t.Errorf("stale heartbeat: %v, want Lost", st[0])
	}
	if err := s.Complete(t.Context(), Lease{b.ID, b.LeaseToken}); err != nil {
		t.Errorf("current holder's complete: %v", err)
	}
	if got, _ := s.Get(t.Context(), owner, job.ID); got.State != "succeeded" {
		t.Errorf("state = %s, want succeeded", got.State)
	}
}

func TestRetriedResultsAreIdempotent(t *testing.T) {
	s, _ := newStore(t)
	owner := uuid.NewV7()
	s.Submit(t.Context(), newJob(owner, "ok", `{}`))
	c := claimOne(t, s, uuid.NewV7())
	l := Lease{c.ID, c.LeaseToken}
	for i := range 2 { // the first response was "lost"; the worker retries
		if err := s.Complete(t.Context(), l); err != nil {
			t.Fatalf("complete #%d: %v", i+1, err)
		}
	}
	if err := s.Fail(t.Context(), l, "late", false); !errors.Is(err, ErrLeaseLost) {
		t.Errorf("fail after complete: %v, want ErrLeaseLost", err)
	}

	s.Submit(t.Context(), newJob(owner, "bad", `{}`))
	c = claimOne(t, s, uuid.NewV7())
	l = Lease{c.ID, c.LeaseToken}
	for i := range 2 {
		if err := s.Fail(t.Context(), l, "boom", true); err != nil {
			t.Fatalf("fail #%d: %v", i+1, err)
		}
	}
	if got, _ := s.Get(t.Context(), owner, c.ID); got.State != "dead" || *got.LastError != "boom" {
		t.Errorf("permanent failure: state=%s, want dead", got.State)
	}
}

func TestHeartbeat(t *testing.T) {
	s, pool := newStore(t)
	owner := uuid.NewV7()
	for _, k := range []string{"a", "b"} {
		s.Submit(t.Context(), newJob(owner, k, `{}`))
	}
	a, b := claimOne(t, s, uuid.NewV7()), claimOne(t, s, uuid.NewV7())
	if _, err := s.Cancel(t.Context(), owner, b.ID); err != nil {
		t.Fatal(err)
	}
	unknown := Lease{uuid.NewV7(), uuid.NewV7()}

	st, err := s.Heartbeat(t.Context(), []Lease{{a.ID, a.LeaseToken}, {b.ID, b.LeaseToken}, unknown}, 30*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	if want := []LeaseState{Held, CancelRequested, Lost}; st[0] != want[0] || st[1] != want[1] || st[2] != want[2] {
		t.Errorf("states = %v, want %v", st, want)
	}

	// A heartbeat never extends a lease past the job's deadline, and stops once it passes.
	pool.Exec(t.Context(), `UPDATE jobs SET deadline_at = now() + interval '1 second' WHERE id = $1`, a.ID)
	s.Heartbeat(t.Context(), []Lease{{a.ID, a.LeaseToken}}, time.Hour)
	var capped bool
	pool.QueryRow(t.Context(), `SELECT lease_expires_at <= deadline_at FROM jobs WHERE id = $1`, a.ID).Scan(&capped)
	if !capped {
		t.Error("lease extended past the deadline")
	}
	pool.Exec(t.Context(), `UPDATE jobs SET deadline_at = now() - interval '1 second' WHERE id = $1`, a.ID)
	if st, _ := s.Heartbeat(t.Context(), []Lease{{a.ID, a.LeaseToken}}, time.Hour); st[0] != Lost {
		t.Errorf("past deadline: %v, want Lost", st[0])
	}
}

func TestClaimOnlyRegisteredTypesAndCancelOnFail(t *testing.T) {
	s, _ := newStore(t)
	owner := uuid.NewV7()
	j := newJob(owner, "k", `{}`)
	j.Type = "image.resize"
	s.Submit(t.Context(), j)

	got, _ := s.Claim(t.Context(), ClaimParams{Node: uuid.NewV7(), Cloud: "aws", Queues: []string{"default"},
		Types: []string{"email.send"}, Max: 10, Lease: time.Minute})
	if len(got) != 0 {
		t.Fatalf("claimed a type the worker can't run: %+v", got)
	}

	c := claimOne(t, s, uuid.NewV7(), "image.resize")
	s.Cancel(t.Context(), owner, c.ID) // running: only requests the cancel
	if err := s.Fail(t.Context(), Lease{c.ID, c.LeaseToken}, "stopped", false); err != nil {
		t.Fatal(err)
	}
	if got, _ := s.Get(t.Context(), owner, c.ID); got.State != "cancelled" {
		t.Errorf("state = %s, want cancelled", got.State)
	}
}
