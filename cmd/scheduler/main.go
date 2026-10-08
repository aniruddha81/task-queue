// Command scheduler runs the singleton chores, the lease reaper and cron ticks, on
// whichever copy holds the leader lease. Run two or more for failover.
package main

import (
	"cmp"
	"context"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"
	_ "time/tzdata" // cron time zones must work even in images without zoneinfo

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/aniruddha81/task-queue/internal/scheduler"
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

	s := scheduler.New(pool, log, scheduler.Config{})
	done := make(chan struct{})
	go func() { s.Run(ctx); close(done) }()

	mux := http.NewServeMux()
	serve.Health(mux, pool.Ping)
	if err := serve.Run(ctx, log, cmp.Or(os.Getenv("ADDR"), ":8082"), mux); err != nil {
		log.Error("serve", "err", err)
		os.Exit(1)
	}
	<-done
	// Planned shutdown: hand the lease over now instead of after it expires.
	resignCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := s.Resign(resignCtx); err != nil {
		log.Warn("resign", "err", err)
	}
}
