// Command worker runs the demo job handlers.
//
// Environment: DISPATCHERS (comma-separated URLs, nearest first), CLOUD (aws|azure),
// QUEUES (comma-separated, default "default"), CONCURRENCY (default 4).
package main

import (
	"cmp"
	"context"
	"encoding/json"
	"log/slog"
	"os"
	"os/signal"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/aniruddha81/task-queue/sdk/go/worker"
)

func main() {
	log := slog.New(slog.NewJSONHandler(os.Stdout, nil))
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	host, _ := os.Hostname()
	concurrency, _ := strconv.Atoi(os.Getenv("CONCURRENCY"))
	w := worker.New(worker.Config{
		Dispatchers: strings.Split(os.Getenv("DISPATCHERS"), ","),
		Cloud:       os.Getenv("CLOUD"),
		Queues:      strings.Split(cmp.Or(os.Getenv("QUEUES"), "default"), ","),
		Name:        host,
		Concurrency: concurrency,
		Log:         log,
	})
	w.Handle("chaos.sleep", sleepHandler)

	log.Info("worker started")
	if err := w.Run(ctx); err != nil {
		log.Error("run", "err", err)
		os.Exit(1)
	}
	log.Info("drained, exiting")
}

// sleepHandler sleeps for payload {"ms": N}, stopping early if its context ends.
func sleepHandler(ctx context.Context, job worker.Job) error {
	var p struct{ MS int }
	if err := json.Unmarshal(job.Payload, &p); err != nil {
		return worker.Permanent(err)
	}
	select {
	case <-time.After(time.Duration(p.MS) * time.Millisecond):
		return nil
	case <-ctx.Done():
		return context.Cause(ctx)
	}
}
