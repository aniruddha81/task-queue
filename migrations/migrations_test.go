package migrations

import (
	"database/sql"
	"io/fs"
	"testing"

	_ "github.com/jackc/pgx/v5/stdlib"

	"github.com/aniruddha81/task-queue/internal/pgtest"
)

// TestUpDownUp applies every database's migrations, rolls them all back, and applies them
// again, in a throwaway database so it never touches dev data.
func TestUpDownUp(t *testing.T) {
	dirs, err := fs.ReadDir(fsys, ".")
	if err != nil {
		t.Fatal(err)
	}
	for _, dir := range dirs {
		t.Run(dir.Name(), func(t *testing.T) {
			db, err := sql.Open("pgx", pgtest.New(t))
			if err != nil {
				t.Fatal(err)
			}
			defer db.Close()
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
