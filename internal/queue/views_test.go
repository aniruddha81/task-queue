package queue

import (
	"errors"
	"testing"
	"uuid"
)

// TestAttemptHistoryShowsRejectedLateResult: what the dashboard shows for a worker that
// was paused past its lease and then tried to complete.
func TestAttemptHistoryShowsRejectedLateResult(t *testing.T) {
	s, pool := newStore(t)
	owner := uuid.NewV7()
	job, _, _ := s.Submit(t.Context(), newJob(owner, "k", `{}`))
	paused := uuid.NewV7()
	s.SeenNode(t.Context(), Node{ID: paused, Name: "worker-a", Cloud: "aws", Queues: []string{"default"}, Types: []string{"email.send"}, Version: "v1"})

	a := claimOne(t, s, paused)
	requeue(t, pool, job.ID) // its lease expired while paused
	b := claimOne(t, s, uuid.NewV7())
	s.Complete(t.Context(), Lease{a.ID, a.LeaseToken}) // the paused worker wakes up: rejected
	s.Complete(t.Context(), Lease{b.ID, b.LeaseToken})

	got, err := s.Attempts(t.Context(), owner, job.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 {
		t.Fatalf("%d attempts, want 2", len(got))
	}
	if got[0].LateResult == nil || *got[0].LateResult != "complete" || got[0].NodeName == nil || *got[0].NodeName != "worker-a" {
		t.Errorf("attempt 1 = %+v, want a rejected late complete from worker-a", got[0])
	}
	if got[1].Outcome == nil || *got[1].Outcome != "succeeded" || got[1].LateResult != nil {
		t.Errorf("attempt 2 = %+v, want succeeded", got[1])
	}
	if _, err := s.Attempts(t.Context(), uuid.NewV7(), job.ID); !errors.Is(err, ErrNotFound) {
		t.Errorf("another owner's attempts: %v, want ErrNotFound", err)
	}
}

func TestViews(t *testing.T) {
	s, _ := newStore(t)
	owner := uuid.NewV7()
	for _, k := range []string{"a", "b", "c"} {
		s.Submit(t.Context(), newJob(owner, k, `{}`))
	}
	node := uuid.NewV7()
	s.SeenNode(t.Context(), Node{ID: node, Name: "w", Cloud: "azure", Queues: []string{"default"}, Types: []string{"email.send"}, Version: "v1"})
	claimOne(t, s, node)

	counts, _ := s.QueueCounts(t.Context(), owner)
	byState := map[string]int{}
	for _, c := range counts {
		byState[c.State] += c.Count
	}
	if byState["available"] != 2 || byState["running"] != 1 {
		t.Errorf("queue counts %v, want 2 available and 1 running", byState)
	}
	if other, _ := s.QueueCounts(t.Context(), uuid.NewV7()); len(other) != 0 {
		t.Errorf("another owner sees counts: %v", other)
	}

	// The same worker restarted: a new node ID under the same name. Only the latest shows.
	s.SeenNode(t.Context(), Node{ID: uuid.NewV7(), Name: "w", Cloud: "azure", Queues: []string{"default"}, Types: []string{"email.send"}, Version: "v2"})
	ws, err := s.Workers(t.Context())
	if err != nil || len(ws) != 1 || !ws[0].Live || ws[0].Version != "v2" || ws[0].Cloud != "azure" {
		t.Errorf("workers = %+v, %v", ws, err)
	}
	l, err := s.Leader(t.Context())
	if err != nil || l.Held || l.ExpiresAt != nil {
		t.Errorf("leader before any election = %+v, %v; want not held", l, err)
	}
}
