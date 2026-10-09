package main

import (
	"crypto/tls"
	"testing"

	"github.com/aniruddha81/task-queue/internal/devca"
	"github.com/aniruddha81/task-queue/internal/tlsconf"
	"golang.org/x/crypto/acme/autocert"
)

// A probe or client that doesn't ask for the public name gets the project certificate,
// without touching Let's Encrypt.
func TestPublicTLSFallsBackToProjectCert(t *testing.T) {
	ca, _ := devca.New()
	crt, key, _ := ca.Issue("gateway", "gateway")
	pair, _ := tls.X509KeyPair(crt, key)
	certs, err := tlsconf.FromPEM(pair, ca.CertPEM())
	if err != nil {
		t.Fatal(err)
	}
	if cfg := publicTLS(certs, "", autocert.DirCache(t.TempDir())); cfg.GetCertificate != nil || len(cfg.Certificates) != 1 {
		t.Fatal("without a domain the project certificate is served directly")
	}
	cfg := publicTLS(certs, "tq-test.trafficmanager.net", autocert.DirCache(t.TempDir()))
	for _, sni := range []string{"", "10.0.0.1", "other.example.com"} {
		got, err := cfg.GetCertificate(&tls.ClientHelloInfo{ServerName: sni})
		if err != nil || string(got.Certificate[0]) != string(pair.Certificate[0]) {
			t.Errorf("SNI %q: want the project certificate, got err %v", sni, err)
		}
	}
}
