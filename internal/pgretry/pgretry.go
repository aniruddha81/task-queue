// Package pgretry rides out a database failover: it retries an operation while the
// database is briefly unreachable or read-only, for up to 30 seconds.
//
// Only use it for idempotent operations. A submit is idempotent because it carries its key,
// so retrying after an unknown outcome (the connection dropped during COMMIT) is safe.
package pgretry

import (
	"context"
	"errors"
	"io"
	"net"
	"time"

	"github.com/jackc/pgx/v5/pgconn"
)

// Limit is how long Do keeps retrying. A Patroni failover finishes well within it.
const Limit = 30 * time.Second

// Do runs op until it succeeds, fails for a reason retrying can't fix, ctx ends, or Limit passes.
func Do[T any](ctx context.Context, op func() (T, error)) (T, error) {
	deadline := time.Now().Add(Limit)
	for wait := 100 * time.Millisecond; ; wait = min(wait*2, 2*time.Second) {
		v, err := op()
		if err == nil || !Transient(err) || time.Now().Add(wait).After(deadline) {
			return v, err
		}
		select {
		case <-ctx.Done():
			return v, err
		case <-time.After(wait):
		}
	}
}

// Transient reports whether err means "the database isn't available right now", as
// opposed to a problem with the request itself (a constraint, bad data, not found).
func Transient(err error) bool {
	var pgErr *pgconn.PgError
	if errors.As(err, &pgErr) {
		switch pgErr.Code[:2] {
		case "08", // connection exception
			"53", // insufficient resources (too many connections)
			"57", // operator intervention: shutdown, crash recovery, cannot connect now
			"58": // system error
			return true
		}
		return pgErr.Code == "25006" // read-only transaction: we reached the old primary as it demoted
	}
	var connErr *pgconn.ConnectError
	var netErr net.Error
	return pgconn.SafeToRetry(err) || errors.As(err, &connErr) || errors.As(err, &netErr) ||
		errors.Is(err, io.EOF) || errors.Is(err, io.ErrUnexpectedEOF) || pgconn.Timeout(err)
}
