// Command sinks runs the test destinations (see internal/sinks). It plays external
// systems, so it has its own database and is never faulted by the chaos harness.
package main

import (
	"cmp"
	"context"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/aniruddha81/task-queue/internal/serve"
	"github.com/aniruddha81/task-queue/internal/sinks"
)

func main() {
	log := slog.New(slog.NewJSONHandler(os.Stdout, nil))
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	certs, err := serve.Certs("sinks")
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

	mux := http.NewServeMux()
	serve.Health(mux, pool.Ping)
	mux.Handle("/", sinks.New(pool, log).Handler())
	if err := serve.Run(ctx, log, cmp.Or(os.Getenv("ADDR"), ":8090"), mux, certs.Server()); err != nil {
		log.Error("serve", "err", err)
		os.Exit(1)
	}
}
