// Package pgtest gives each test its own throwaway PostgreSQL database.
package pgtest

import (
	"fmt"
	"net/url"
	"os"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/stdlib"
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
	name := fmt.Sprintf("test_%d", time.Now().UnixNano())
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
