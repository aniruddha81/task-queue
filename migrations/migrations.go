// Package migrations embeds the SQL migrations for each database (jobs, later auth and sinks).
package migrations

import (
	"database/sql"
	"embed"
	"io/fs"

	"github.com/pressly/goose/v3"
)

//go:embed */*.sql
var fsys embed.FS

// Provider returns a goose provider for one database's migrations, e.g. "jobs".
func Provider(db *sql.DB, database string) (*goose.Provider, error) {
	sub, err := fs.Sub(fsys, database)
	if err != nil {
		return nil, err
	}
	return goose.NewProvider(goose.DialectPostgres, db, sub)
}
