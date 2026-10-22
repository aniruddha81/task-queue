package queue

import (
	"context"
	"errors"
	"time"
	"uuid"

	"github.com/jackc/pgx/v5"
)

// ErrTooManyReminders: the address, or everyone together, has too many waiting for confirmation.
var ErrTooManyReminders = errors.New("too many reminders waiting for confirmation")

// Reminder is one booked on the live page, waiting for its address to be confirmed.
type Reminder struct {
	Token     uuid.UUID `db:"token"`
	Email     string    `db:"email"`
	Label     string    `db:"label"`
	RemindAt  time.Time `db:"remind_at"`
	TZ        string    `db:"tz"`
	JobID     uuid.UUID `db:"job_id"`
	CreatedAt time.Time `db:"created_at"`
}

// BookReminder stores r until its address is confirmed and returns its token. Each one sends a
// confirmation email to an address no one has proven is theirs, so an address gets at most 3
// waiting, and everyone together 300 (a day's worth: older ones are forgotten first).
func (s *Store) BookReminder(ctx context.Context, r Reminder) (uuid.UUID, error) {
	if _, err := s.db.Exec(ctx, `DELETE FROM reminders WHERE created_at < now() - interval '1 day'`); err != nil {
		return uuid.UUID{}, err
	}
	token := uuid.NewV4()
	// ponytail: the counts are not locked, so concurrent bookings can pass them by a few.
	tag, err := s.db.Exec(ctx, `INSERT INTO reminders (token, email, label, remind_at, tz)
		SELECT $1, $2, $3, $4, $5
		WHERE (SELECT count(*) FROM reminders WHERE email = $2) < 3
		  AND (SELECT count(*) FROM reminders) < 300`, token, r.Email, r.Label, r.RemindAt, r.TZ)
	switch {
	case err != nil:
		return uuid.UUID{}, err
	case tag.RowsAffected() == 0:
		return uuid.UUID{}, ErrTooManyReminders
	}
	return token, nil
}

// WaitingReminder returns a reminder booked in the last day and not yet confirmed.
func (s *Store) WaitingReminder(ctx context.Context, token uuid.UUID) (Reminder, error) {
	rows, _ := s.db.Query(ctx, `SELECT token, email, label, remind_at, tz, job_id, created_at FROM reminders
		WHERE token = $1 AND created_at > now() - interval '1 day'`, token)
	r, err := pgx.CollectExactlyOneRow(rows, pgx.RowToStructByName[Reminder])
	if errors.Is(err, pgx.ErrNoRows) {
		return Reminder{}, ErrNotFound
	}
	return r, err
}

// ForgetReminder deletes a reminder once its job exists.
func (s *Store) ForgetReminder(ctx context.Context, token uuid.UUID) error {
	_, err := s.db.Exec(ctx, `DELETE FROM reminders WHERE token = $1`, token)
	return err
}
