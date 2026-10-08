// Command gateway is the public entry point (see internal/gateway).
//
// Environment: AUTH_URL, JOBS_URL (https upstreams), PUBLIC_ORIGIN (e.g.
// https://localhost:8443), STATIC_DIR (the dashboard export), RATE_LIMIT and RATE_BURST
// (per user; defaults 20/s and 40), TLS_DIR. In the cloud, ACME_DOMAIN (the Traffic
// Manager name) gets a Let's Encrypt certificate, cached in ACME_CACHE (default /acme).
package main

import (
	"cmp"
	"context"
	"crypto/tls"
	"log/slog"
	"net/http"
	"net/url"
	"os"
	"os/signal"
	"strconv"
	"syscall"

	"golang.org/x/crypto/acme/autocert"
	"golang.org/x/time/rate"

	"github.com/aniruddha81/task-queue/internal/authn"
	"github.com/aniruddha81/task-queue/internal/gateway"
	"github.com/aniruddha81/task-queue/internal/serve"
	"github.com/aniruddha81/task-queue/internal/tlsconf"
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
	if err := serve.Run(ctx, log, cmp.Or(os.Getenv("ADDR"), ":8443"), mux, publicTLS(certs, os.Getenv("ACME_DOMAIN"), cmp.Or(os.Getenv("ACME_CACHE"), "/acme"))); err != nil {
		fail("serve", err)
	}
}

// publicTLS serves the project certificate, or with a domain, a Let's Encrypt certificate
// for it, obtained and renewed through TLS-ALPN-01 on this same port. Other server names
// (Traffic Manager's health probe, a client dialing the IP) still get the project
// certificate, so the gateway answers before its public certificate exists.
// ponytail: autocert's directory cache is per gateway; two gateways each get their own
// certificate, which is fine until Let's Encrypt's rate limit for the name matters.
func publicTLS(certs *tlsconf.Bundle, domain, cacheDir string) *tls.Config {
	cfg := certs.Public()
	if domain == "" {
		return cfg
	}
	m := &autocert.Manager{
		Prompt:     autocert.AcceptTOS,
		HostPolicy: autocert.HostWhitelist(domain),
		Cache:      autocert.DirCache(cacheDir),
	}
	acme := m.TLSConfig()
	acme.MinVersion = tls.VersionTLS12
	project := cfg.Certificates[0]
	acme.GetCertificate = func(hello *tls.ClientHelloInfo) (*tls.Certificate, error) {
		if hello.ServerName != domain {
			return &project, nil
		}
		return m.GetCertificate(hello)
	}
	return acme
}
