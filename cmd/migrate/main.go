// Command migrate applies one database's migrations, then exits: migrate <jobs|auth|sinks>.
// It runs once per release as a one-shot job; services never migrate on startup.
package main

import (
	"context"
	"database/sql"
	"log"
	"os"

	_ "github.com/jackc/pgx/v5/stdlib"

	"github.com/aniruddha81/task-queue/migrations"
)

func main() {
	if len(os.Args) != 2 {
		log.Fatal("usage: migrate <database>   (DATABASE_URL must be set)")
	}
	db, err := sql.Open("pgx", os.Getenv("DATABASE_URL"))
	if err != nil {
		log.Fatal(err)
	}
	defer db.Close()

	p, err := migrations.Provider(db, os.Args[1])
	if err != nil {
		log.Fatal(err)
	}
	results, err := p.Up(context.Background())
	for _, r := range results {
		log.Print(r)
	}
	if err != nil {
		log.Fatal(err)
	}
}
