package queue

import (
	"context"
	"errors"
	"time"
	"uuid"

	"github.com/jackc/pgx/v5"
)

// Attempt is one try at a job, as the dashboard shows it. LateResult is set when the
// attempt's worker reported after losing its lease and the result was discarded (G3).
type Attempt struct {
	Attempt    int        `db:"attempt" json:"attempt"`
	NodeID     uuid.UUID  `db:"node_id" json:"node_id"`
	NodeName   *string    `db:"node_name" json:"node_name"`
	StartedAt  time.Time  `db:"started_at" json:"started_at"`
	FinishedAt *time.Time `db:"finished_at" json:"finished_at"`
	Outcome    *string    `db:"outcome" json:"outcome"`
	Error      *string    `db:"error" json:"error"`
	LateResult *string    `db:"late_result" json:"late_result"`
	LateAt     *time.Time `db:"late_at" json:"late_at"`
}

func (s *Store) Attempts(ctx context.Context, owner, id uuid.UUID) ([]Attempt, error) {
	if _, err := s.Get(ctx, owner, id); err != nil {
		return nil, err // ownership check: another owner's job is "not found"
	}
	rows, _ := s.db.Query(ctx, `
		SELECT a.attempt, a.node_id, n.name AS node_name, a.started_at, a.finished_at, a.outcome,
		       a.error, a.late_result, a.late_at
		FROM job_attempts a LEFT JOIN nodes n ON n.id = a.node_id
		WHERE a.job_id = $1 ORDER BY a.attempt`, id)
	return pgx.CollectRows(rows, pgx.RowToStructByName[Attempt])
}

type QueueCount struct {
	Queue string `db:"queue" json:"queue"`
	State string `db:"state" json:"state"`
	Count int    `db:"count" json:"count"`
}

// QueueCounts counts the owner's jobs by queue and state.
func (s *Store) QueueCounts(ctx context.Context, owner uuid.UUID) ([]QueueCount, error) {
	rows, _ := s.db.Query(ctx, `SELECT queue, state, count(*)::int AS count FROM jobs
		WHERE owner_id = $1 GROUP BY queue, state ORDER BY queue, state`, owner)
	return pgx.CollectRows(rows, pgx.RowToStructByName[QueueCount])
}

// Worker is a node as dispatch last saw it: the latest process per worker name. Live means
// it polled within the last 45 s (an idle worker re-polls at least every 20 s).
type Worker struct {
	ID         uuid.UUID `db:"id" json:"id"`
	Name       string    `db:"name" json:"name"`
	Cloud      string    `db:"cloud" json:"cloud"`
	Queues     []string  `db:"queues" json:"queues"`
	Types      []string  `db:"types" json:"types"`
	Version    string    `db:"version" json:"version"`
	LastSeenAt time.Time `db:"last_seen_at" json:"last_seen_at"`
	Live       bool      `db:"live" json:"live"`
	Running    int       `db:"running" json:"running"`
}

func (s *Store) Workers(ctx context.Context) ([]Worker, error) {
	rows, _ := s.db.Query(ctx, `
		SELECT n.id, n.name, n.cloud, n.queues, n.types, n.version, n.last_seen_at,
		       n.last_seen_at > now() - interval '45 seconds' AS live,
		       (SELECT count(*)::int FROM jobs j WHERE j.node_id = n.id AND j.state = 'running') AS running
		FROM (SELECT DISTINCT ON (name) * FROM nodes
		      WHERE last_seen_at > now() - interval '1 hour'
		      ORDER BY name, last_seen_at DESC) n -- a restarted worker is a new node: show its latest
		ORDER BY n.cloud, n.name`)
	return pgx.CollectRows(rows, pgx.RowToStructByName[Worker])
}

type Leader struct {
	Holder    *uuid.UUID `db:"holder" json:"holder"`
	Epoch     int64      `db:"epoch" json:"epoch"`
	ExpiresAt *time.Time `db:"expires_at" json:"expires_at"` // null: never held, or resigned
	Held      bool       `db:"held" json:"held"`
}

func (s *Store) Leader(ctx context.Context) (Leader, error) {
	rows, _ := s.db.Query(ctx, `SELECT holder, epoch, NULLIF(expires_at, '-infinity') AS expires_at, expires_at > now() AS held
		FROM leader_leases WHERE name = 'scheduler'`)
	l, err := pgx.CollectExactlyOneRow(rows, pgx.RowToStructByName[Leader])
	if errors.Is(err, pgx.ErrNoRows) {
		return Leader{}, nil
	}
	return l, err
}
