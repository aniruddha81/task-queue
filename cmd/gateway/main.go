// Command gateway is the public entry point (see internal/gateway).
//
// Environment: AUTH_URL, JOBS_URL (https upstreams), PUBLIC_ORIGIN (e.g.
// https://localhost:8443), STATIC_DIR (the dashboard export), RATE_LIMIT and RATE_BURST
// (per user; defaults 20/s and 40), TLS_DIR.
package main

import (
	"cmp"
	"context"
	"log/slog"
	"net/http"
	"net/url"
	"os"
	"os/signal"
	"strconv"
	"syscall"

	"golang.org/x/time/rate"

	"github.com/aniruddha81/task-queue/internal/authn"
	"github.com/aniruddha81/task-queue/internal/gateway"
	"github.com/aniruddha81/task-queue/internal/serve"
)

func main() {
	log := slog.New(slog.NewJSONHandler(os.Stdout, nil))
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	fail := func(what string, err error) { log.Error(what, "err", err); os.Exit(1) }

	certs, err := serve.Certs("gateway")
	if err != nil {
		fail("certificates", err)
	}
	authURL, err1 := url.Parse(os.Getenv("AUTH_URL"))
	jobsURL, err2 := url.Parse(os.Getenv("JOBS_URL"))
	if err1 != nil || err2 != nil || authURL.Host == "" || jobsURL.Host == "" {
		fail("AUTH_URL and JOBS_URL", err1)
	}
	limit, _ := strconv.ParseFloat(cmp.Or(os.Getenv("RATE_LIMIT"), "20"), 64)
	burst, _ := strconv.Atoi(cmp.Or(os.Getenv("RATE_BURST"), "40"))
	client := serve.Client(certs)

	verifier := authn.NewVerifier()
	go verifier.Watch(ctx, authURL.JoinPath("/.well-known/jwks.json").String(), client, log)

	h := gateway.New(gateway.Config{
		Auth: authURL, Jobs: jobsURL, Transport: client.Transport, Verifier: verifier,
		Origin: os.Getenv("PUBLIC_ORIGIN"),
		Static: http.FileServer(http.Dir(cmp.Or(os.Getenv("STATIC_DIR"), "/static"))),
		Rate:   rate.Limit(limit), Burst: burst, Log: log,
	})
	mux := http.NewServeMux()
	serve.Health(mux, verifier.Ready)
	mux.Handle("/", h)
	if err := serve.Run(ctx, log, cmp.Or(os.Getenv("ADDR"), ":8443"), mux, certs.Public()); err != nil {
		fail("serve", err)
	}
}
