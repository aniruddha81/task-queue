// Command auth serves login, admin-only user creation and the JWKS.
//
// Environment: DATABASE_URL (the auth database), JWT_PRIVATE_KEY (base64 Ed25519 seed),
// SEED_USERS (optional, "email:password[:admin],..."), TLS_DIR.
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

	"github.com/aniruddha81/task-queue/internal/auth"
	"github.com/aniruddha81/task-queue/internal/authn"
	"github.com/aniruddha81/task-queue/internal/serve"
)

func main() {
	log := slog.New(slog.NewJSONHandler(os.Stdout, nil))
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	fail := func(what string, err error) { log.Error(what, "err", err); os.Exit(1) }

	certs, err := serve.Certs("auth")
	if err != nil {
		fail("certificates", err)
	}
	key, err := authn.ParsePrivateKey(os.Getenv("JWT_PRIVATE_KEY"))
	if err != nil {
		fail("JWT_PRIVATE_KEY", err)
	}
	pool, err := pgxpool.New(ctx, os.Getenv("DATABASE_URL"))
	if err != nil {
		fail("database config", err)
	}
	defer pool.Close()

	svc, err := auth.New(pool, key, log)
	if err != nil {
		fail("auth", err)
	}
	if seed := os.Getenv("SEED_USERS"); seed != "" {
		if err := svc.Seed(ctx, seed); err != nil {
			fail("seed users", err)
		}
	}

	mux := http.NewServeMux()
	serve.Health(mux, pool.Ping)
	mux.Handle("/", svc.Handler())
	if err := serve.Run(ctx, log, cmp.Or(os.Getenv("ADDR"), ":8083"), mux, certs.Server()); err != nil {
		fail("serve", err)
	}
}
