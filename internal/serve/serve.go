// Package serve runs a service the same way everywhere: TLS only (mTLS between services),
// health endpoints, and a graceful drain on shutdown.
package serve

import (
	"context"
	"crypto/tls"
	"errors"
	"log/slog"
	"net/http"
	"os"
	"time"

	"github.com/aniruddha81/task-queue/internal/tlsconf"
)

// Certs loads this service's certificates from $TLS_DIR (ca.crt, <name>.crt, <name>.key).
// There is no plaintext fallback: a service without certificates doesn't start.
func Certs(name string) (*tlsconf.Bundle, error) {
	dir := os.Getenv("TLS_DIR")
	if dir == "" {
		return nil, errors.New("TLS_DIR is not set")
	}
	return tlsconf.Load(dir, name)
}

// Health adds GET /healthz (the process is alive) and GET /readyz (every check passes).
func Health(mux *http.ServeMux, ready ...func(context.Context) error) {
	mux.HandleFunc("GET /healthz", func(http.ResponseWriter, *http.Request) {})
	mux.HandleFunc("GET /readyz", func(w http.ResponseWriter, r *http.Request) {
		for _, check := range ready {
			if err := check(r.Context()); err != nil {
				http.Error(w, "not ready: "+err.Error(), http.StatusServiceUnavailable)
				return
			}
		}
	})
}

// Run serves h over TLS on addr until ctx ends, then stops accepting and finishes
// in-flight requests. HTTP/2 is negotiated over TLS, which gRPC needs.
func Run(ctx context.Context, log *slog.Logger, addr string, h http.Handler, tlsCfg *tls.Config) error {
	srv := &http.Server{Addr: addr, Handler: h, TLSConfig: tlsCfg, ReadHeaderTimeout: 5 * time.Second}
	errc := make(chan error, 1)
	go func() { errc <- srv.ListenAndServeTLS("", "") }()
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

// Transport calls other services over mTLS and HTTP/2, with connection health checks:
// after a network cut, a connection can be silently dead, and without pings HTTP/2 keeps
// sending requests into it until the OS gives up, which can take many minutes (found by
// the chaos test: workers stopped claiming after a partition healed).
func Transport(b *tlsconf.Bundle) *http.Transport {
	return &http.Transport{
		TLSClientConfig:   b.Client(),
		ForceAttemptHTTP2: true,
		HTTP2:             &http.HTTP2Config{SendPingTimeout: 10 * time.Second, PingTimeout: 5 * time.Second},
	}
}

// Client is an HTTP client for calling other services over mTLS.
func Client(b *tlsconf.Bundle) *http.Client {
	return &http.Client{Transport: Transport(b), Timeout: 30 * time.Second}
}
