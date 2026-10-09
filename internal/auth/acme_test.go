package auth

import (
	"bytes"
	"crypto/ed25519"
	"crypto/tls"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/aniruddha81/task-queue/internal/devca"
	"github.com/aniruddha81/task-queue/internal/pgtest"
	"github.com/aniruddha81/task-queue/internal/tlsconf"
)

// Only the gateway's certificate may use the shared ACME cache: it holds the public
// certificate's private key.
func TestACMECacheGatewayOnly(t *testing.T) {
	_, key, _ := ed25519.GenerateKey(nil)
	s, err := New(pgtest.Migrated(t, "auth"), key, slog.New(slog.DiscardHandler))
	if err != nil {
		t.Fatal(err)
	}
	ca, _ := devca.New()
	bundle := func(name string) *tlsconf.Bundle {
		crt, k, _ := ca.Issue(name, name, "localhost", "127.0.0.1")
		pair, _ := tls.X509KeyPair(crt, k)
		b, _ := tlsconf.FromPEM(pair, ca.CertPEM())
		return b
	}
	srv := httptest.NewUnstartedServer(s.Handler())
	srv.TLS = bundle("auth").Server()
	srv.StartTLS()
	defer srv.Close()

	do := func(as, method, body string) (int, string) {
		c := &http.Client{Transport: &http.Transport{TLSClientConfig: bundle(as).Client()}}
		req, _ := http.NewRequest(method, srv.URL+"/internal/acme/tq.example+rsa", bytes.NewBufferString(body))
		resp, err := c.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		defer resp.Body.Close()
		b, _ := io.ReadAll(resp.Body)
		return resp.StatusCode, string(b)
	}
	if code, _ := do("gateway", "GET", ""); code != http.StatusNotFound {
		t.Fatalf("empty cache: got %d, want 404", code)
	}
	if code, _ := do("gateway", "PUT", "cert-v1"); code != http.StatusOK {
		t.Fatalf("put: %d", code)
	}
	do("gateway", "PUT", "cert-v2") // a renewal overwrites
	if code, body := do("gateway", "GET", ""); code != http.StatusOK || body != "cert-v2" {
		t.Fatalf("get: %d %q, want 200 cert-v2", code, body)
	}
	if code, _ := do("worker", "GET", ""); code != http.StatusForbidden {
		t.Fatalf("worker read the gateway's certificate: %d", code)
	}
	do("gateway", "DELETE", "")
	if code, _ := do("gateway", "GET", ""); code != http.StatusNotFound {
		t.Fatalf("after delete: %d", code)
	}
}
