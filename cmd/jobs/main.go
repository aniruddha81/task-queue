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

	certs, err := serve.Certs("jobs")
	if err != nil {
		log.Error("certificates", "err", err)
		os.Exit(1)
	}
	pool, err := pgxpool.New(ctx, os.Getenv("DATABASE_URL"))
	if err != nil {
		log.Error("database config", "err", err)
		os.Exit(1)
	}
	defer pool.Close()
	// Fails closed: every request is refused until auth's keys have loaded.
	verifier := authn.NewVerifier()
	go verifier.Watch(ctx, os.Getenv("JWKS_URL"), serve.Client(certs), log)

	mux := http.NewServeMux()
	serve.Health(mux, pool.Ping, verifier.Ready)
	mux.Handle("/v1/", jobsapi.New(queue.NewStore(pool), verifier, log))
	if err := serve.Run(ctx, log, cmp.Or(os.Getenv("ADDR"), ":8080"), mux, certs.Server()); err != nil {
		log.Error("serve", "err", err)
		os.Exit(1)
	}
}
