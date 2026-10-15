// Command worker runs the demo job handlers (see internal/handlers).
//
// Environment: DISPATCHERS (comma-separated URLs, nearest first), CLOUD (aws|azure),
// QUEUES (comma-separated, default "default"), CONCURRENCY (default 4), SINKS_URL,
// SMTP_ADDR (Mailpit), SMTP_RELAY, SMTP_USER, SMTP_PASS (real mail, such as SES on port 587),
// MAIL_FROM ("Name <address>"), WEBHOOK_ALLOW (comma-separated URL prefixes), TLS_DIR.
package main

import (
	"cmp"
	"context"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"strconv"
	"strings"
	"syscall"

	"github.com/aniruddha81/task-queue/internal/handlers"
	"github.com/aniruddha81/task-queue/internal/serve"
	"github.com/aniruddha81/task-queue/sdk/go/worker"
)

func main() {
	log := slog.New(slog.NewJSONHandler(os.Stdout, nil))
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	certs, err := serve.Certs("worker")
	if err != nil {
		log.Error("certificates", "err", err)
		os.Exit(1)
	}
	host, _ := os.Hostname()
	concurrency, _ := strconv.Atoi(os.Getenv("CONCURRENCY"))
	w := worker.New(worker.Config{
		Dispatchers: strings.Split(os.Getenv("DISPATCHERS"), ","),
		Cloud:       os.Getenv("CLOUD"),
		Queues:      strings.Split(cmp.Or(os.Getenv("QUEUES"), "default"), ","),
		Name:        host,
		Concurrency: concurrency,
		Log:         log,
		// mTLS to dispatch; no client timeout: claims are long polls with their own deadlines.
		HTTPClient: &http.Client{Transport: serve.Transport(certs)},
	})
	handlers.Register(w, handlers.Config{
		Sinks:        os.Getenv("SINKS_URL"),
		SMTP:         os.Getenv("SMTP_ADDR"),
		Relay:        os.Getenv("SMTP_RELAY"),
		RelayUser:    os.Getenv("SMTP_USER"),
		RelayPass:    os.Getenv("SMTP_PASS"),
		From:         os.Getenv("MAIL_FROM"),
		WebhookAllow: strings.Split(os.Getenv("WEBHOOK_ALLOW"), ","),
		Client:       serve.Client(certs),
	})

	log.Info("worker started")
	if err := w.Run(ctx); err != nil {
		log.Error("run", "err", err)
		os.Exit(1)
	}
	log.Info("drained, exiting")
}
