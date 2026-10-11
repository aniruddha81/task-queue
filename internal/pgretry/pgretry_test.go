package pgretry

import (
	"context"
	"errors"
	"io"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgconn"
)

func TestTransient(t *testing.T) {
	for name, c := range map[string]struct {
		err  error
		want bool
	}{
		"connection reset":        {io.ErrUnexpectedEOF, true},
		"admin shutdown (57P01)":  {&pgconn.PgError{Code: "57P01"}, true},
		"cannot connect now":      {&pgconn.PgError{Code: "57P03"}, true},
		"read-only (old primary)": {&pgconn.PgError{Code: "25006"}, true},
		"unique violation":        {&pgconn.PgError{Code: "23505"}, false},
		"check violation":         {&pgconn.PgError{Code: "23514"}, false},
		"domain error":            {errors.New("job not found"), false},
	} {
		if got := Transient(c.err); got != c.want {
			t.Errorf("%s: Transient = %v, want %v", name, got, c.want)
		}
	}
}

func TestDoRetriesOnlyTransientErrors(t *testing.T) {
	calls := 0
	v, err := Do(context.Background(), func(context.Context) (int, error) {
		calls++
		if calls < 3 {
			return 0, io.ErrUnexpectedEOF // the primary went away
		}
		return 42, nil
	})
	if v != 42 || err != nil || calls != 3 {
		t.Errorf("got %d, %v after %d calls; want 42 after 3", v, err, calls)
	}

	calls = 0
	_, err = Do(context.Background(), func(context.Context) (int, error) { calls++; return 0, &pgconn.PgError{Code: "23505"} })
	if calls != 1 || err == nil {
		t.Errorf("a constraint violation was retried %d times", calls)
	}
}

// A primary that vanished answers nothing: the first try hangs until its own deadline, then
// the retry (a fresh connection, to the new primary) succeeds.
func TestDoBoundsEachAttempt(t *testing.T) {
	calls := 0
	start := time.Now()
	v, err := Do(context.Background(), func(ctx context.Context) (int, error) {
		calls++
		if calls == 1 {
			<-ctx.Done()
			return 0, errors.New("query canceled") // what pgx returns is not always typed
		}
		return 7, nil
	})
	if v != 7 || err != nil || calls != 2 {
		t.Errorf("got %d, %v after %d calls; want 7 after 2", v, err, calls)
	}
	if d := time.Since(start); d < Attempt || d > Attempt+time.Second {
		t.Errorf("took %v; want about Attempt (%v)", d, Attempt)
	}
}
