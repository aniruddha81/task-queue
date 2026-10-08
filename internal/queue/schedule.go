package queue

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"
	"uuid"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/robfig/cron/v3"
)

var ErrNameTaken = errors.New("a schedule with this name already exists")

// Cron is a parsed schedule: a standard 5-field expression in an IANA time zone.
//
// Ticks are local wall-clock times, and each fires at most once. When clocks fall back,
// the repeated hour's times don't fire a second time (a daily 01:30 job runs once). When
// clocks spring forward, times inside the skipped hour don't exist, so they don't fire
// that day. Zones without DST, such as Asia/Kolkata, are unaffected.
type Cron struct {
	sched cron.Schedule
	loc   *time.Location
}

func ParseCron(expr, tz string) (*Cron, error) {
	if strings.Contains(expr, "TZ=") {
		return nil, errors.New("set the time zone in the timezone field, not in the expression")
	}
	s, err := cron.ParseStandard(expr)
	if err != nil {
		return nil, fmt.Errorf("invalid cron expression: %w", err)
	}
	loc, err := time.LoadLocation(tz)
	if err != nil || tz == "" || tz == "Local" {
		return nil, errors.New("timezone must be an IANA name such as Asia/Kolkata")
	}
	return &Cron{s, loc}, nil
}

// Next returns the first tick after `after` whose wall-clock time is later than after's.
func (c *Cron) Next(after time.Time) time.Time {
	prev := after.In(c.loc)
	t := c.sched.Next(prev)
	for !wall(t).After(wall(prev)) { // a repeat of an already-fired wall-clock time
		t = c.sched.Next(t)
	}
	return t
}

// wall is t's local date and clock reading, comparable across UTC offsets.
func wall(t time.Time) time.Time {
	y, mo, d := t.Date()
	h, mi, s := t.Clock()
	return time.Date(y, mo, d, h, mi, s, 0, time.UTC)
}

type Schedule struct {
	ID         uuid.UUID       `db:"id" json:"id"`
	Name       string          `db:"name" json:"name"`
	Cron       string          `db:"cron" json:"cron"`
	Timezone   string          `db:"timezone" json:"timezone"`
	Template   json.RawMessage `db:"template" json:"job"`
	Paused     bool            `db:"paused" json:"paused"`
	NextTickAt time.Time       `db:"next_tick_at" json:"next_tick_at"`
	CreatedAt  time.Time       `db:"created_at" json:"created_at"`
}

const scheduleColumns = `id, name, cron, timezone, template, paused, next_tick_at, created_at`

// CreateSchedule stores a validated schedule; its first tick is the first one after now.
func (s *Store) CreateSchedule(ctx context.Context, owner uuid.UUID, name string, c *Cron, expr, tz string, template json.RawMessage) (Schedule, error) {
	now, err := s.now(ctx)
	if err != nil {
		return Schedule{}, err
	}
	rows, _ := s.db.Query(ctx, `INSERT INTO schedules (owner_id, name, cron, timezone, template, next_tick_at)
		VALUES ($1, $2, $3, $4, $5::jsonb, $6) RETURNING `+scheduleColumns,
		owner, name, expr, tz, string(template), c.Next(now))
	sc, err := pgx.CollectExactlyOneRow(rows, pgx.RowToStructByName[Schedule])
	var pgErr *pgconn.PgError
	if errors.As(err, &pgErr) && pgErr.Code == "23505" {
		return Schedule{}, ErrNameTaken
	}
	return sc, err
}

func (s *Store) GetSchedule(ctx context.Context, owner, id uuid.UUID) (Schedule, error) {
	rows, _ := s.db.Query(ctx, `SELECT `+scheduleColumns+` FROM schedules WHERE id = $1 AND owner_id = $2`, id, owner)
	sc, err := pgx.CollectExactlyOneRow(rows, pgx.RowToStructByName[Schedule])
	if errors.Is(err, pgx.ErrNoRows) {
		return Schedule{}, ErrNotFound
	}
	return sc, err
}

func (s *Store) ListSchedules(ctx context.Context, owner uuid.UUID) ([]Schedule, error) {
	rows, _ := s.db.Query(ctx, `SELECT `+scheduleColumns+` FROM schedules WHERE owner_id = $1 ORDER BY name`, owner)
	return pgx.CollectRows(rows, pgx.RowToStructByName[Schedule])
}

// SetPaused pauses or resumes a schedule. Ticks during a pause never fire: resuming
// starts from the first tick after now.
func (s *Store) SetPaused(ctx context.Context, owner, id uuid.UUID, paused bool) (Schedule, error) {
	sc, err := s.GetSchedule(ctx, owner, id)
	if err != nil {
		return Schedule{}, err
	}
	next := sc.NextTickAt
	if !paused && sc.Paused {
		c, err := ParseCron(sc.Cron, sc.Timezone)
		if err != nil {
			return Schedule{}, err
		}
		now, err := s.now(ctx)
		if err != nil {
			return Schedule{}, err
		}
		next = c.Next(now)
	}
	rows, _ := s.db.Query(ctx, `UPDATE schedules SET paused = $3, next_tick_at = $4
		WHERE id = $1 AND owner_id = $2 RETURNING `+scheduleColumns, id, owner, paused, next)
	return pgx.CollectExactlyOneRow(rows, pgx.RowToStructByName[Schedule])
}

func (s *Store) DeleteSchedule(ctx context.Context, owner, id uuid.UUID) error {
	tag, err := s.db.Exec(ctx, `DELETE FROM schedules WHERE id = $1 AND owner_id = $2`, id, owner)
	if err == nil && tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return err
}

// TickSchedules submits the job for every due tick, oldest first, up to max ticks, and
// advances each schedule. Missed ticks are all caught up. Run it in one transaction: the
// job, its tick row and the schedule's next_tick_at commit together, and the tick row's
// primary key makes a second job for the same tick impossible (G6).
func (s *Store) TickSchedules(ctx context.Context, max int) (int, error) {
	now, err := s.now(ctx)
	if err != nil {
		return 0, err
	}
	type due struct {
		ID       uuid.UUID       `db:"id"`
		Owner    uuid.UUID       `db:"owner_id"`
		Cron     string          `db:"cron"`
		Timezone string          `db:"timezone"`
		Template json.RawMessage `db:"template"`
		Next     time.Time       `db:"next_tick_at"`
	}
	rows, _ := s.db.Query(ctx, `SELECT id, owner_id, cron, timezone, template, next_tick_at FROM schedules
		WHERE NOT paused AND next_tick_at <= $1 ORDER BY next_tick_at LIMIT 50
		FOR UPDATE SKIP LOCKED`, now)
	schedules, err := pgx.CollectRows(rows, pgx.RowToStructByName[due])
	if err != nil {
		return 0, err
	}
	n := 0
	for _, sc := range schedules {
		c, err := ParseCron(sc.Cron, sc.Timezone)
		if err != nil {
			return n, fmt.Errorf("schedule %s: %w", sc.ID, err)
		}
		tick := sc.Next
		for !tick.After(now) && n < max {
			j, err := ParseJob(sc.Template)
			if err != nil {
				return n, fmt.Errorf("schedule %s template: %w", sc.ID, err)
			}
			at := tick
			j.Owner, j.RunAt = sc.Owner, &at
			j.Key = "cron:" + sc.ID.String() + ":" + tick.UTC().Format(time.RFC3339)
			job, _, err := s.Submit(ctx, j)
			if err != nil {
				return n, err
			}
			if _, err := s.db.Exec(ctx, `INSERT INTO schedule_ticks (schedule_id, tick_at, job_id)
				VALUES ($1, $2, $3)`, sc.ID, tick, job.ID); err != nil {
				return n, err
			}
			tick = c.Next(tick)
			n++
		}
		if _, err := s.db.Exec(ctx, `UPDATE schedules SET next_tick_at = $2 WHERE id = $1`, sc.ID, tick); err != nil {
			return n, err
		}
	}
	return n, nil
}

// now is the database's clock, the only clock schedules use.
func (s *Store) now(ctx context.Context) (time.Time, error) {
	var t time.Time
	err := s.db.QueryRow(ctx, `SELECT now()`).Scan(&t)
	return t, err
}
