package pgretry

import (
	"context"
	"errors"
	"io"
	"testing"

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
	v, err := Do(context.Background(), func() (int, error) {
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
	_, err = Do(context.Background(), func() (int, error) { calls++; return 0, &pgconn.PgError{Code: "23505"} })
	if calls != 1 || err == nil {
		t.Errorf("a constraint violation was retried %d times", calls)
	}
}
