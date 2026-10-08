package queue

import (
	"context"
	"errors"
	"time"
	"uuid"

	"github.com/jackc/pgx/v5"
)

// ErrLeaseLost means the caller's lease token is no longer current: someone else may own
// the job now, so the caller's result is discarded. This is the fencing behind G3.
var ErrLeaseLost = errors.New("lease lost")

// Claimed is a job handed to a worker. Durations, not timestamps: workers' clocks are not trusted.
type Claimed struct {
	ID              uuid.UUID `db:"id"`
	Owner           uuid.UUID `db:"owner_id"`
	Key             string    `db:"idempotency_key"`
	Queue           string    `db:"queue"`
	Type            string    `db:"type"`
	Payload         []byte    `db:"payload"`
	Attempt         int       `db:"attempt"`
	LeaseToken      uuid.UUID `db:"lease_token"`
	LeaseSeconds    float64   `db:"lease_seconds"`
	DeadlineSeconds float64   `db:"deadline_seconds"`
}

// EffectKey identifies the job's effect across retries and resubmissions (G4).
func (c Claimed) EffectKey() string { return c.Owner.String() + ":" + c.Key }

type ClaimParams struct {
	Node       uuid.UUID
	Cloud      string
	Queues     []string
	Types      []string // only types the worker has handlers for
	Max        int
	Lease      time.Duration
	StealAfter time.Duration // another cloud's worker may take an affinity job after this
}

// Claim takes up to p.Max available jobs, highest priority first, in one statement.
// SKIP LOCKED lets any number of dispatchers claim concurrently; the state re-check in the
// UPDATE makes a double claim impossible under any query plan.
func (s *Store) Claim(ctx context.Context, p ClaimParams) ([]Claimed, error) {
	rows, _ := s.db.Query(ctx, `
		WITH next AS MATERIALIZED (
		  SELECT id FROM jobs
		  WHERE state = 'available' AND run_at <= now()
		    AND queue = ANY($1) AND type = ANY($2)
		    AND (affinity IS NULL OR affinity = $3 OR run_at <= now() - $4::interval)
		  ORDER BY priority DESC, run_at, id
		  LIMIT $5
		  FOR UPDATE SKIP LOCKED
		), claimed AS (
		  UPDATE jobs j
		  SET state = 'running', attempt = j.attempt + 1, lease_token = gen_random_uuid(),
		      node_id = $6, updated_at = now(),
		      deadline_at      = now() + make_interval(secs => j.timeout_seconds),
		      lease_expires_at = now() + LEAST($7::interval, make_interval(secs => j.timeout_seconds))
		  FROM next
		  WHERE j.id = next.id AND j.state = 'available'
		  RETURNING j.*
		), logged AS (
		  INSERT INTO job_attempts (job_id, attempt, lease_token, node_id, started_at)
		  SELECT id, attempt, lease_token, node_id, now() FROM claimed
		)
		SELECT id, owner_id, idempotency_key, queue, type, payload, attempt, lease_token,
		       EXTRACT(EPOCH FROM lease_expires_at - now())::float8 AS lease_seconds,
		       EXTRACT(EPOCH FROM deadline_at - now())::float8      AS deadline_seconds
		FROM claimed`,
		p.Queues, p.Types, p.Cloud, p.StealAfter, p.Max, p.Node, p.Lease)
	return pgx.CollectRows(rows, pgx.RowToStructByName[Claimed])
}

// Lease identifies one held job.
type Lease struct {
	JobID uuid.UUID
	Token uuid.UUID
}

type LeaseState int

const (
	Held LeaseState = iota
	Lost
	CancelRequested
)

// Heartbeat extends many leases in one statement, never past each job's deadline.
// The result has one state per input lease, in order.
func (s *Store) Heartbeat(ctx context.Context, leases []Lease, lease time.Duration) ([]LeaseState, error) {
	ids := make([]uuid.UUID, len(leases))
	tokens := make([]uuid.UUID, len(leases))
	for i, l := range leases {
		ids[i], tokens[i] = l.JobID, l.Token
	}
	rows, _ := s.db.Query(ctx, `
		UPDATE jobs j SET lease_expires_at = LEAST(now() + $3::interval, j.deadline_at), updated_at = now()
		FROM unnest($1::uuid[], $2::uuid[]) AS l(id, token)
		WHERE j.id = l.id AND j.lease_token = l.token AND j.state = 'running' AND j.deadline_at > now()
		RETURNING j.id, j.cancel_requested`, ids, tokens, lease)
	held := map[uuid.UUID]bool{} // id -> cancel_requested
	var id uuid.UUID
	var cancel bool
	_, err := pgx.ForEachRow(rows, []any{&id, &cancel}, func() error { held[id] = cancel; return nil })
	if err != nil {
		return nil, err
	}
	states := make([]LeaseState, len(leases))
	for i, l := range leases {
		cancel, ok := held[l.JobID]
		switch {
		case !ok:
			states[i] = Lost
		case cancel:
			states[i] = CancelRequested
		}
	}
	return states, nil
}

// Complete records success. A retry of a complete that already committed returns nil.
func (s *Store) Complete(ctx context.Context, l Lease) error {
	tag, err := s.db.Exec(ctx, `
		WITH done AS (
		  UPDATE jobs SET state = 'succeeded', finished_at = now(), updated_at = now(),
		                  lease_token = NULL, lease_expires_at = NULL
		  WHERE id = $1 AND lease_token = $2 AND state = 'running'
		  RETURNING id, attempt
		)
		UPDATE job_attempts a SET outcome = 'succeeded', finished_at = now()
		FROM done WHERE a.job_id = done.id AND a.attempt = done.attempt`, l.JobID, l.Token)
	if err != nil || tag.RowsAffected() == 1 {
		return err
	}
	return s.alreadyRecorded(ctx, l, "succeeded")
}

// Fail records a failed attempt. The job is cancelled if a cancel was requested, dead if it
// is out of attempts or the error is permanent, and otherwise available again after backoff.
func (s *Store) Fail(ctx context.Context, l Lease, msg string, permanent bool, backoff time.Duration) error {
	tag, err := s.db.Exec(ctx, `
		WITH f AS (
		  UPDATE jobs SET
		    state = CASE WHEN cancel_requested THEN 'cancelled'
		                 WHEN $4 OR attempt >= max_attempts THEN 'dead'
		                 ELSE 'available' END,
		    finished_at = CASE WHEN cancel_requested OR $4 OR attempt >= max_attempts THEN now() END,
		    run_at = now() + $5::interval,
		    lease_token = NULL, lease_expires_at = NULL, deadline_at = NULL,
		    last_error = $3, updated_at = now()
		  WHERE id = $1 AND lease_token = $2 AND state = 'running'
		  RETURNING id, attempt, state
		)
		UPDATE job_attempts a
		SET outcome = CASE WHEN f.state = 'cancelled' THEN 'cancelled' ELSE 'failed' END,
		    finished_at = now(), error = $3
		FROM f WHERE a.job_id = f.id AND a.attempt = f.attempt`,
		l.JobID, l.Token, msg, permanent, backoff)
	if err != nil || tag.RowsAffected() == 1 {
		return err
	}
	return s.alreadyRecorded(ctx, l, "failed", "cancelled")
}

// alreadyRecorded tells a retry of a committed result (nil) from a stale token (ErrLeaseLost).
func (s *Store) alreadyRecorded(ctx context.Context, l Lease, outcomes ...string) error {
	var outcome *string
	err := s.db.QueryRow(ctx, `SELECT outcome FROM job_attempts WHERE job_id = $1 AND lease_token = $2`,
		l.JobID, l.Token).Scan(&outcome)
	if errors.Is(err, pgx.ErrNoRows) {
		return ErrLeaseLost
	}
	if err != nil {
		return err
	}
	for _, o := range outcomes {
		if outcome != nil && *outcome == o {
			return nil
		}
	}
	return ErrLeaseLost
}

// Node is a worker as dispatch last saw it.
type Node struct {
	ID            uuid.UUID
	Name, Cloud   string
	Queues, Types []string
	Version       string
}

func (s *Store) SeenNode(ctx context.Context, n Node) error {
	_, err := s.db.Exec(ctx, `
		INSERT INTO nodes (id, name, cloud, queues, types, version) VALUES ($1, $2, $3, $4, $5, $6)
		ON CONFLICT (id) DO UPDATE SET name = $2, cloud = $3, queues = $4, types = $5, version = $6,
		                               last_seen_at = now()`,
		n.ID, n.Name, n.Cloud, n.Queues, n.Types, n.Version)
	return err
}
