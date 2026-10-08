// Package serve runs an HTTP service the same way everywhere: health endpoints,
// HTTP/1 plus unencrypted HTTP/2 (gRPC needs HTTP/2; mTLS arrives in week 6),
// and a graceful drain on shutdown.
package serve

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"time"
)

// Health adds GET /healthz (the process is alive) and GET /readyz (ready() returns nil).
func Health(mux *http.ServeMux, ready func(context.Context) error) {
	mux.HandleFunc("GET /healthz", func(http.ResponseWriter, *http.Request) {})
	mux.HandleFunc("GET /readyz", func(w http.ResponseWriter, r *http.Request) {
		if err := ready(r.Context()); err != nil {
			http.Error(w, "not ready: "+err.Error(), http.StatusServiceUnavailable)
		}
	})
}

// Run serves h on addr until ctx ends, then stops accepting and finishes in-flight requests.
func Run(ctx context.Context, log *slog.Logger, addr string, h http.Handler) error {
	var protocols http.Protocols
	protocols.SetHTTP1(true)
	protocols.SetUnencryptedHTTP2(true)
	srv := &http.Server{Addr: addr, Handler: h, Protocols: &protocols, ReadHeaderTimeout: 5 * time.Second}

	errc := make(chan error, 1)
	go func() { errc <- srv.ListenAndServe() }()
	log.Info("listening", "addr", addr)
	select {
	case err := <-errc:
		return err
	case <-ctx.Done():
	}
	shutdownCtx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	if err := srv.Shutdown(shutdownCtx); err != nil && !errors.Is(err, http.ErrServerClosed) {
		return err
	}
	return nil
}
