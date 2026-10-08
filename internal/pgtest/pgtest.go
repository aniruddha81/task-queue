// Package pgtest gives each test its own throwaway PostgreSQL database.
package pgtest

import (
	"database/sql"
	"net/url"
	"os"
	"strings"
	"testing"
	"uuid"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/jackc/pgx/v5/stdlib"

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
	cfg, err := pgx.ParseConfig(server)
	if err != nil {
		t.Fatal(err)
	}
	admin := stdlib.OpenDB(*cfg)
	// Random, not time-based: parallel test packages, and a coarse clock (Windows), collide.
	name := "test_" + strings.ReplaceAll(uuid.NewV4().String(), "-", "")
	if _, err := admin.Exec("CREATE DATABASE " + name); err != nil {
		admin.Close()
		t.Fatal(err)
	}
	t.Cleanup(func() {
		admin.Exec("DROP DATABASE " + name + " WITH (FORCE)")
		admin.Close()
	})
	u, err := url.Parse(server)
	if err != nil {
		t.Fatal(err)
	}
	u.Path = "/" + name
	return u.String()
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
