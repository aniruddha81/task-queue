// Package scheduler runs the singleton chores (the lease reaper and cron ticks) on
// whichever scheduler process holds the leader lease.
//
// Safety never depends on the lease: the reaper is safe with many copies running, and the
// tick table's primary key makes duplicate cron jobs impossible. The lease (G7) only
// stops wasted work, and every chore transaction is fenced on the leader's epoch, so a
// paused or partitioned ex-leader's writes are rejected.
package scheduler

import (
	"context"
	"errors"
	"log/slog"
	"time"
	"uuid"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/aniruddha81/task-queue/internal/queue"
)

var ErrNotLeader = errors.New("not the leader")

type Config struct {
	TTL       time.Duration // leader lease length; default 15 s
	Every     time.Duration // pause between chore rounds; default 1 s
	TickBatch int           // cron ticks per round; default 500
	ReapBatch int           // expired leases per round; default 500
}

type Scheduler struct {
	pool *pgxpool.Pool
	log  *slog.Logger
	cfg  Config
	me   uuid.UUID // a new identity per process start
}

func New(pool *pgxpool.Pool, log *slog.Logger, cfg Config) *Scheduler {
	if cfg.TTL <= 0 {
		cfg.TTL = 15 * time.Second
	}
	if cfg.Every <= 0 {
		cfg.Every = time.Second
	}
	if cfg.TickBatch <= 0 {
		cfg.TickBatch = 500
	}
	if cfg.ReapBatch <= 0 {
		cfg.ReapBatch = 500
	}
	return &Scheduler{pool: pool, log: log, cfg: cfg, me: uuid.NewV7()}
}

// Run campaigns for the lease and runs chores while holding it, until ctx ends.
func (s *Scheduler) Run(ctx context.Context) {
	for ctx.Err() == nil {
		epoch, err := s.acquire(ctx)
		if err != nil && ctx.Err() == nil {
			s.log.Warn("campaign", "err", err)
		}
		if epoch == 0 {
			sleep(ctx, s.cfg.TTL/3)
			continue
		}
		s.log.Info("became leader", "epoch", epoch)
		s.lead(ctx, epoch)
		s.log.Info("stepped down", "epoch", epoch)
	}
}

// Resign gives up the lease at once, so a planned shutdown hands over without waiting
// for the lease to expire. A crash skips this; the lease then expires on its own.
func (s *Scheduler) Resign(ctx context.Context) error {
	_, err := s.pool.Exec(ctx, `UPDATE leader_leases SET expires_at = '-infinity'
		WHERE name = 'scheduler' AND holder = $1`, s.me)
	return err
}

// acquire takes the lease if it has expired, bumping the epoch. It returns 0 if it didn't.
func (s *Scheduler) acquire(ctx context.Context) (int64, error) {
	var epoch int64
	err := s.pool.QueryRow(ctx, `UPDATE leader_leases SET holder = $1, epoch = epoch + 1,
		expires_at = now() + $2::interval WHERE name = 'scheduler' AND expires_at < now()
		RETURNING epoch`, s.me, s.cfg.TTL).Scan(&epoch)
	if errors.Is(err, pgx.ErrNoRows) {
		return 0, nil
	}
	return epoch, err
}

// renew extends the lease. false means it was lost: expired, or taken over.
func (s *Scheduler) renew(ctx context.Context, epoch int64) (bool, error) {
	tag, err := s.pool.Exec(ctx, `UPDATE leader_leases SET expires_at = now() + $3::interval
		WHERE name = 'scheduler' AND holder = $1 AND epoch = $2 AND expires_at > now()`,
		s.me, epoch, s.cfg.TTL)
	return tag.RowsAffected() == 1, err
}

func (s *Scheduler) lead(ctx context.Context, epoch int64) {
	renewed := time.Now() // monotonic
	for ctx.Err() == nil {
		if time.Since(renewed) >= s.cfg.TTL/3 {
			held, err := s.renew(ctx, epoch)
			switch {
			case err == nil && !held:
				return
			case err == nil:
				renewed = time.Now()
			case time.Since(renewed) > s.cfg.TTL/2:
				return // can't reach the database: assume someone else will lead
			}
		}
		for _, c := range []struct {
			name string
			run  func(*queue.Store) (int, error)
		}{
			{"reap", func(st *queue.Store) (int, error) { return st.Reap(ctx, s.cfg.ReapBatch) }},
			{"cron", func(st *queue.Store) (int, error) { return st.TickSchedules(ctx, s.cfg.TickBatch) }},
		} {
			n, err := s.chore(ctx, epoch, c.name, c.run)
			if errors.Is(err, ErrNotLeader) {
				return
			}
			if err != nil && ctx.Err() == nil {
				s.log.Error("chore", "chore", c.name, "err", err)
			}
			if n > 0 {
				s.log.Info("chore", "chore", c.name, "changed", n)
			}
		}
		sleep(ctx, s.cfg.Every)
	}
}

// chore runs one chore in a transaction fenced on the leader's epoch. The FOR SHARE lock
// on the lease row makes any takeover wait until this transaction ends, so chores of two
// epochs never overlap; a stale epoch finds no row and changes nothing.
func (s *Scheduler) chore(ctx context.Context, epoch int64, name string, run func(*queue.Store) (int, error)) (int, error) {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return 0, err
	}
	defer tx.Rollback(context.Background())
	var one int
	err = tx.QueryRow(ctx, `SELECT 1 FROM leader_leases
		WHERE name = 'scheduler' AND holder = $1 AND epoch = $2 AND expires_at > now()
		FOR SHARE`, s.me, epoch).Scan(&one)
	if errors.Is(err, pgx.ErrNoRows) {
		return 0, ErrNotLeader
	}
	if err != nil {
		return 0, err
	}
	n, err := run(queue.NewStore(tx))
	if err != nil {
		return 0, err
	}
	if n > 0 { // evidence for G7: which epoch changed what, and when
		if _, err := tx.Exec(ctx, `INSERT INTO chore_runs (epoch, chore, started_at, committed_at)
			VALUES ($1, $2, now(), clock_timestamp())`, epoch, name); err != nil {
			return 0, err
		}
	}
	return n, tx.Commit(ctx)
}

func sleep(ctx context.Context, d time.Duration) {
	select {
	case <-ctx.Done():
	case <-time.After(d):
	}
}
