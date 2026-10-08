// Command jobs serves the public job API (see internal/jobsapi).
package main

import (
	"cmp"
	"context"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	_ "time/tzdata" // schedule time zones must work even in images without zoneinfo

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/aniruddha81/task-queue/internal/authn"
	"github.com/aniruddha81/task-queue/internal/jobsapi"
	"github.com/aniruddha81/task-queue/internal/queue"
	"github.com/aniruddha81/task-queue/internal/serve"
)

func main() {
	log := slog.New(slog.NewJSONHandler(os.Stdout, nil))
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	pool, err := pgxpool.New(ctx, os.Getenv("DATABASE_URL"))
	if err != nil {
		log.Error("database config", "err", err)
		os.Exit(1)
	}
	defer pool.Close()
	// Fail closed: no key, no service. Locally, compose passes the dev key's public half.
	auth, err := authn.NewVerifier(os.Getenv("JWT_PUBLIC_KEY"))
	if err != nil {
		log.Error("JWT_PUBLIC_KEY", "err", err)
		os.Exit(1)
	}

	mux := http.NewServeMux()
	serve.Health(mux, pool.Ping)
	mux.Handle("/v1/", jobsapi.New(queue.NewStore(pool), auth, log))
	if err := serve.Run(ctx, log, cmp.Or(os.Getenv("ADDR"), ":8080"), mux); err != nil {
		log.Error("serve", "err", err)
		os.Exit(1)
	}
}
