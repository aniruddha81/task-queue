// Command scheduler runs background chores. So far: the lease reaper, which returns jobs
// held by crashed, paused or partitioned workers (and passed deadlines) to the queue.
// It is safe with any number of copies running; week 5 adds leader election.
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

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/aniruddha81/task-queue/internal/queue"
	"github.com/aniruddha81/task-queue/internal/serve"
)

const reapBatch = 500

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
	store := queue.NewStore(pool)

	go func() {
		for ctx.Err() == nil {
			n, err := store.Reap(ctx, reapBatch)
			switch {
			case err != nil && ctx.Err() == nil:
				log.Error("reap", "err", err)
			case n > 0:
				log.Info("reaped expired leases", "jobs", n)
			}
			if n < reapBatch { // a full batch means more may be waiting: go again at once
				select {
				case <-ctx.Done():
				case <-time.After(time.Second):
				}
			}
		}
	}()

	mux := http.NewServeMux()
	serve.Health(mux, pool.Ping)
	if err := serve.Run(ctx, log, cmp.Or(os.Getenv("ADDR"), ":8082"), mux); err != nil {
		log.Error("serve", "err", err)
		os.Exit(1)
	}
}
