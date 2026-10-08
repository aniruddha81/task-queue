// Package queue is the jobs database: submitting, reading and changing jobs.
// Every query is scoped to the owner, so another owner's job looks exactly like a missing one.
package queue

import (
	"context"
	"encoding/json"
	"errors"
	"time"
	"uuid"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

var (
	ErrNotFound       = errors.New("job not found")
	ErrKeyReused      = errors.New("idempotency key already used with a different request")
	ErrNotCancellable = errors.New("job already finished")
	ErrNotDead        = errors.New("only dead jobs can be redriven")
)

// Job is what users see. The lease token is deliberately absent: it belongs to workers.
type Job struct {
	ID              uuid.UUID       `db:"id" json:"id"`
	Queue           string          `db:"queue" json:"queue"`
	Type            string          `db:"type" json:"type"`
	Payload         json.RawMessage `db:"payload" json:"payload"`
	Priority        int             `db:"priority" json:"priority"`
	RunAt           time.Time       `db:"run_at" json:"run_at"`
	State           string          `db:"state" json:"state"`
	CancelRequested bool            `db:"cancel_requested" json:"cancel_requested"`
	Attempt         int             `db:"attempt" json:"attempt"`
	MaxAttempts     int             `db:"max_attempts" json:"max_attempts"`
	TimeoutSeconds  int             `db:"timeout_seconds" json:"timeout_seconds"`
	Affinity        *string         `db:"affinity" json:"affinity"`
	LastError       *string         `db:"last_error" json:"last_error"`
	CreatedAt       time.Time       `db:"created_at" json:"created_at"`
	UpdatedAt       time.Time       `db:"updated_at" json:"updated_at"`
	FinishedAt      *time.Time      `db:"finished_at" json:"finished_at"`
}

const columns = `id, queue, type, payload, priority, run_at, state, cancel_requested, attempt,
	max_attempts, timeout_seconds, affinity, last_error, created_at, updated_at, finished_at`

// NewJob is a validated submission. Request is the client's original body: a repeat
// with the same key must send the same JSON (key order and whitespace don't matter).
type NewJob struct {
	Owner          uuid.UUID
	Key            string
	Request        json.RawMessage
	Queue, Type    string
	Payload        json.RawMessage
	Priority       int
	RunAt          *time.Time // nil: now, on the database clock
	MaxAttempts    int
	TimeoutSeconds int
	Affinity       *string
}

type Store struct{ db *pgxpool.Pool }

func NewStore(db *pgxpool.Pool) *Store { return &Store{db} }

// Submit inserts a job, or returns the existing one for a repeated key (created=false).
func (s *Store) Submit(ctx context.Context, j NewJob) (job Job, created bool, err error) {
	rows, _ := s.db.Query(ctx, `
		INSERT INTO jobs (owner_id, idempotency_key, request, queue, type, payload, priority,
		                  run_at, max_attempts, timeout_seconds, affinity)
		VALUES ($1, $2, $3::jsonb, $4, $5, $6::jsonb, $7, COALESCE($8, now()), $9, $10, $11)
		ON CONFLICT (owner_id, idempotency_key) DO NOTHING
		RETURNING `+columns,
		j.Owner, j.Key, string(j.Request), j.Queue, j.Type, string(j.Payload), j.Priority,
		j.RunAt, j.MaxAttempts, j.TimeoutSeconds, j.Affinity)
	job, err = pgx.CollectExactlyOneRow(rows, pgx.RowToStructByName[Job])
	if !errors.Is(err, pgx.ErrNoRows) {
		return job, err == nil, err
	}

	// The key exists. If another insert of it was in flight, ON CONFLICT waited for it
	// to commit, and this new statement sees it.
	type existing struct {
		Job
		Same bool `db:"same"`
	}
	rows, _ = s.db.Query(ctx, `SELECT `+columns+`, request = $3::jsonb AS same
		FROM jobs WHERE owner_id = $1 AND idempotency_key = $2`, j.Owner, j.Key, string(j.Request))
	e, err := pgx.CollectExactlyOneRow(rows, pgx.RowToStructByName[existing])
	if err != nil {
		return Job{}, false, err
	}
	if !e.Same {
		return Job{}, false, ErrKeyReused
	}
	return e.Job, false, nil
}

func (s *Store) Get(ctx context.Context, owner, id uuid.UUID) (Job, error) {
	rows, _ := s.db.Query(ctx, `SELECT `+columns+` FROM jobs WHERE id = $1 AND owner_id = $2`, id, owner)
	job, err := pgx.CollectExactlyOneRow(rows, pgx.RowToStructByName[Job])
	if errors.Is(err, pgx.ErrNoRows) {
		return Job{}, ErrNotFound
	}
	return job, err
}

// Filter narrows List. Nil fields don't filter. After is the last ID of the previous page.
type Filter struct {
	State, Queue *string
	After        *uuid.UUID
	Limit        int
}

// List returns the owner's jobs, newest first, using keyset pagination on the UUIDv7 id.
func (s *Store) List(ctx context.Context, owner uuid.UUID, f Filter) ([]Job, error) {
	rows, _ := s.db.Query(ctx, `SELECT `+columns+` FROM jobs
		WHERE owner_id = $1
		  AND ($2::text IS NULL OR state = $2)
		  AND ($3::text IS NULL OR queue = $3)
		  AND ($4::uuid IS NULL OR id < $4)
		ORDER BY id DESC
		LIMIT $5`, owner, f.State, f.Queue, f.After, f.Limit)
	return pgx.CollectRows(rows, pgx.RowToStructByName[Job])
}

// Cancel cancels an available job at once. A running job gets cancel_requested; its
// worker sees that on its next heartbeat. Cancelling a cancelled job is a no-op.
func (s *Store) Cancel(ctx context.Context, owner, id uuid.UUID) (Job, error) {
	rows, _ := s.db.Query(ctx, `
		UPDATE jobs SET
		  state            = CASE WHEN state = 'available' THEN 'cancelled' ELSE state END,
		  cancel_requested = (state = 'running'),
		  finished_at      = CASE WHEN state = 'available' THEN now() END,
		  updated_at       = now()
		WHERE id = $1 AND owner_id = $2 AND state IN ('available', 'running')
		RETURNING `+columns, id, owner)
	job, err := pgx.CollectExactlyOneRow(rows, pgx.RowToStructByName[Job])
	if !errors.Is(err, pgx.ErrNoRows) {
		return job, err
	}
	job, err = s.Get(ctx, owner, id)
	switch {
	case err != nil:
		return Job{}, err
	case job.State == "cancelled":
		return job, nil
	default:
		return job, ErrNotCancellable
	}
}

// Redrive puts a dead job back in the queue with a fresh retry budget (its original
// max_attempts). Attempt numbers keep increasing, so the attempt history stays intact,
// and the effect key is unchanged, so effects from earlier attempts stay deduplicated.
func (s *Store) Redrive(ctx context.Context, owner, id uuid.UUID) (Job, error) {
	rows, _ := s.db.Query(ctx, `
		UPDATE jobs SET state = 'available', run_at = now(), finished_at = NULL, updated_at = now(),
		  max_attempts = attempt + COALESCE((request->>'max_attempts')::int, 5)
		WHERE id = $1 AND owner_id = $2 AND state = 'dead'
		RETURNING `+columns, id, owner)
	job, err := pgx.CollectExactlyOneRow(rows, pgx.RowToStructByName[Job])
	if !errors.Is(err, pgx.ErrNoRows) {
		return job, err
	}
	if job, err = s.Get(ctx, owner, id); err != nil {
		return Job{}, err
	}
	return job, ErrNotDead
}
