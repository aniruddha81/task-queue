// Command dispatch serves the worker API: claim, heartbeat, complete, fail.
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
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promhttp"

	"github.com/aniruddha81/task-queue/gen/taskqueue/worker/v1/workerv1connect"
	"github.com/aniruddha81/task-queue/internal/dispatch"
	"github.com/aniruddha81/task-queue/internal/queue"
	"github.com/aniruddha81/task-queue/internal/serve"
)

func main() {
	log := slog.New(slog.NewJSONHandler(os.Stdout, nil))
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	certs, err := serve.Certs("dispatch")
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

	srv := dispatch.New(queue.NewStore(pool), log)
	prometheus.MustRegister(dispatch.QueueDepth(pool))
	go srv.Listen(ctx, pool)

	mux := http.NewServeMux()
	serve.Health(mux, pool.Ping)
	mux.Handle("/metrics", promhttp.Handler())
	mux.Handle(workerv1connect.NewWorkerServiceHandler(srv))
	if err := serve.Run(ctx, log, cmp.Or(os.Getenv("ADDR"), ":8081"), mux, certs.Server()); err != nil {
		log.Error("serve", "err", err)
		os.Exit(1)
	}
}
