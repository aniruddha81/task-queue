// Package pgtest gives each test its own throwaway PostgreSQL database.
package pgtest

import (
	"context"
	"database/sql"
	"os"
	"strings"
	"testing"
	"uuid"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	_ "github.com/jackc/pgx/v5/stdlib"

	"github.com/aniruddha81/task-queue/migrations"
)

// New creates an empty database on the server at TEST_DATABASE_URL (a postgres:// URL),
// drops it when the test ends, and returns its URL. It skips the test if the variable is unset.
func New(t *testing.T) string {
	t.Helper()
	server := os.Getenv("TEST_DATABASE_URL")
	if server == "" {
		t.Skip("set TEST_DATABASE_URL to a PostgreSQL 18 server to run")
	}
	ctx := context.Background()
	admin, err := pgx.Connect(ctx, server) // with several hosts, this lands on the primary
	if err != nil {
		t.Fatal(err)
	}
	// Random, not time-based: parallel test packages, and a coarse clock (Windows), collide.
	name := "test_" + strings.ReplaceAll(uuid.NewV4().String(), "-", "")
	if _, err := admin.Exec(ctx, "CREATE DATABASE "+name); err != nil {
		admin.Close(ctx)
		t.Fatal(err)
	}
	t.Cleanup(func() {
		admin.Exec(ctx, "DROP DATABASE "+name+" WITH (FORCE)")
		admin.Close(ctx)
	})
	// Point the test at the node that created the database: a standby may not have
	// replayed CREATE DATABASE yet, and pgx won't fall back to another host after
	// "database does not exist".
	return withHost(withDatabase(server, name), admin.PgConn().Conn().RemoteAddr().String())
}

// Migrated returns a pool on a fresh database with one database's migrations applied.
func Migrated(t *testing.T, database string) *pgxpool.Pool {
	t.Helper()
	url := New(t)
	db, err := sql.Open("pgx", url)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	p, err := migrations.Provider(db, database)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := p.Up(t.Context()); err != nil {
		t.Fatal(err)
	}
	pool, err := pgxpool.New(t.Context(), url)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(pool.Close)
	return pool
}

// withHost replaces the host list in a postgres:// URL (which must have a /database).
func withHost(url, hostPort string) string {
	start := strings.Index(url, "://") + 3
	end := start + strings.IndexByte(url[start:], '/')
	if at := strings.LastIndexByte(url[start:end], '@'); at >= 0 {
		start += at + 1 // keep user:password@
	}
	return url[:start] + hostPort + url[end:]
}

// withDatabase swaps the database name in a postgres:// URL. Not url.Parse: it rejects
// multi-host URLs such as postgres://u:p@h1:5432,h2:5432/db, which pgx accepts.
func withDatabase(server, name string) string {
	query := ""
	if i := strings.IndexByte(server, '?'); i >= 0 {
		server, query = server[:i], server[i:]
	}
	if i := strings.Index(server, "://"); i >= 0 {
		if j := strings.IndexByte(server[i+3:], '/'); j >= 0 {
			server = server[:i+3+j]
		}
	}
	return server + "/" + name + query
}
