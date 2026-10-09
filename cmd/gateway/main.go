// Command gateway is the public entry point (see internal/gateway).
//
// Environment: AUTH_URL, JOBS_URL (https upstreams; comma-separated, nearest first, the
// rest tried when one is down), PUBLIC_ORIGIN (e.g.
// https://localhost:8443), STATIC_DIR (the dashboard export), RATE_LIMIT and RATE_BURST
// (per user; defaults 20/s and 40), TLS_DIR. In the cloud, ACME_DOMAIN (the Traffic
// Manager name) gets a Let's Encrypt certificate, cached in ACME_CACHE (default /acme).
package main

import (
	"cmp"
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"net/url"
	"os"
	"os/signal"
	"strconv"
	"strings"
	"syscall"
	"time"

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
	authURLs, err1 := urlList(os.Getenv("AUTH_URL"))
	jobsURLs, err2 := urlList(os.Getenv("JOBS_URL"))
	if err := errors.Join(err1, err2); err != nil {
		fail("AUTH_URL and JOBS_URL", err)
	}
	limit, _ := strconv.ParseFloat(cmp.Or(os.Getenv("RATE_LIMIT"), "20"), 64)
	burst, _ := strconv.Atoi(cmp.Or(os.Getenv("RATE_BURST"), "40"))
	client := serve.Client(certs)

	verifier := authn.NewVerifier()
	jwks := make([]string, len(authURLs))
	for i, u := range authURLs {
		jwks[i] = u.JoinPath("/.well-known/jwks.json").String()
	}
	go verifier.Watch(ctx, strings.Join(jwks, ","), client, log)
	cache := sharedCache{local: autocert.DirCache(cmp.Or(os.Getenv("ACME_CACHE"), "/acme")), auth: authURLs, client: client}

	h := gateway.New(gateway.Config{
		Auth: authURLs, Jobs: jobsURLs, Transport: client.Transport, Verifier: verifier,
		Origin: os.Getenv("PUBLIC_ORIGIN"),
		Static: http.FileServer(http.Dir(cmp.Or(os.Getenv("STATIC_DIR"), "/static"))),
		Rate:   rate.Limit(limit), Burst: burst, Log: log,
	})
	mux := http.NewServeMux()
	// Ready only when it could serve through its own VM: Traffic Manager returns a gateway in
	// DNS only while /readyz passes, and after a reboot the VM's jobs can be up before it can
	// reach the database (the twin's upstreams are only for failover).
	serve.Health(mux, verifier.Ready, upstreamReady(client, jobsURLs[0]), upstreamReady(client, authURLs[0]))
	mux.Handle("/", h)
	if err := serve.Run(ctx, log, cmp.Or(os.Getenv("ADDR"), ":8443"), mux, publicTLS(certs, os.Getenv("ACME_DOMAIN"), cache)); err != nil {
		fail("serve", err)
	}
}

// publicTLS serves the project certificate, or with a domain, a Let's Encrypt certificate
// for it, obtained and renewed through TLS-ALPN-01 on this same port. Other server names
// (Traffic Manager's health probe, a client dialing the IP) still get the project
// certificate, so the gateway answers before its public certificate exists.
func publicTLS(certs *tlsconf.Bundle, domain string, cache autocert.Cache) *tls.Config {
	cfg := certs.Public()
	if domain == "" {
		return cfg
	}
	m := &autocert.Manager{
		Prompt:     autocert.AcceptTOS,
		HostPolicy: autocert.HostWhitelist(domain),
		Cache:      cache,
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

// urlList parses a comma-separated list of upstream URLs, nearest first.
func urlList(s string) ([]*url.URL, error) {
	var out []*url.URL
	for _, part := range strings.Split(s, ",") {
		u, err := url.Parse(strings.TrimSpace(part))
		if err != nil || u.Host == "" {
			return nil, fmt.Errorf("bad upstream URL %q", part)
		}
		out = append(out, u)
	}
	return out, nil
}

// upstreamReady checks an upstream's own /readyz, briefly: Traffic Manager probes every 10 s.
func upstreamReady(c *http.Client, u *url.URL) func(context.Context) error {
	return func(ctx context.Context) error {
		ctx, cancel := context.WithTimeout(ctx, 2*time.Second)
		defer cancel()
		req, err := http.NewRequestWithContext(ctx, "GET", u.JoinPath("/readyz").String(), nil)
		if err != nil {
			return err
		}
		resp, err := c.Do(req)
		if err != nil {
			return err
		}
		resp.Body.Close()
		if resp.StatusCode != http.StatusOK {
			return fmt.Errorf("%s not ready: %s", u.Host, resp.Status)
		}
		return nil
	}
}
