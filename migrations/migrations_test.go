package migrations

import (
	"database/sql"
	"fmt"
	"io/fs"
	"os"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/stdlib"
)

// TestUpDownUp applies every database's migrations, rolls them all back, and applies them
// again, in a throwaway database so it never touches dev data.
func TestUpDownUp(t *testing.T) {
	url := os.Getenv("TEST_DATABASE_URL")
	if url == "" {
		t.Skip("set TEST_DATABASE_URL to a PostgreSQL 18 server to run")
	}
	dirs, err := fs.ReadDir(fsys, ".")
	if err != nil {
		t.Fatal(err)
	}
	for _, dir := range dirs {
		t.Run(dir.Name(), func(t *testing.T) {
			db := freshDB(t, url)
			p, err := Provider(db, dir.Name())
			if err != nil {
				t.Fatal(err)
			}
			if _, err := p.Up(t.Context()); err != nil {
				t.Fatal("up:", err)
			}
			if _, err := p.DownTo(t.Context(), 0); err != nil {
				t.Fatal("down:", err)
			}
			if _, err := p.Up(t.Context()); err != nil {
				t.Fatal("up again:", err)
			}
		})
	}
}

func freshDB(t *testing.T, url string) *sql.DB {
	cfg, err := pgx.ParseConfig(url)
	if err != nil {
		t.Fatal(err)
	}
	admin := stdlib.OpenDB(*cfg)
	t.Cleanup(func() { admin.Close() })
	name := fmt.Sprintf("test_%d", time.Now().UnixNano())
	if _, err := admin.Exec("CREATE DATABASE " + name); err != nil {
		t.Fatal(err)
	}
	cfg.Database = name
	db := stdlib.OpenDB(*cfg)
	t.Cleanup(func() {
		db.Close()
		admin.Exec("DROP DATABASE " + name + " WITH (FORCE)")
	})
	return db
}
