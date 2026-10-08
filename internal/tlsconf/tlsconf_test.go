package tlsconf

import (
	"crypto/tls"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/aniruddha81/task-queue/internal/devca"
)

func bundle(t *testing.T, ca *devca.CA, name string) *Bundle {
	t.Helper()
	crt, key, err := ca.Issue(name, name, "localhost", "127.0.0.1")
	if err != nil {
		t.Fatal(err)
	}
	cert, err := tls.X509KeyPair(crt, key)
	if err != nil {
		t.Fatal(err)
	}
	b, err := FromPEM(cert, ca.CertPEM())
	if err != nil {
		t.Fatal(err)
	}
	return b
}

// TestServerRequiresProjectClientCert: an internal service (e.g. dispatch) refuses
// clients with no certificate, or with one from another CA.
func TestServerRequiresProjectClientCert(t *testing.T) {
	ca, _ := devca.New()
	other, _ := devca.New()
	srv := httptest.NewUnstartedServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))
	srv.TLS = bundle(t, ca, "dispatch").Server()
	srv.StartTLS()
	defer srv.Close()

	get := func(cfg *tls.Config) error {
		c := &http.Client{Transport: &http.Transport{TLSClientConfig: cfg}}
		resp, err := c.Get(srv.URL)
		if err == nil {
			resp.Body.Close()
		}
		return err
	}
	client := bundle(t, ca, "worker").Client()
	if err := get(client); err != nil {
		t.Fatalf("worker with a project certificate: %v", err)
	}
	noCert := client.Clone()
	noCert.Certificates = nil
	if err := get(noCert); err == nil {
		t.Error("client without a certificate was accepted")
	}
	foreign := bundle(t, other, "worker").Client()
	foreign.RootCAs = client.RootCAs // trusts our server, but its own cert is from another CA
	if err := get(foreign); err == nil {
		t.Error("client with a certificate from another CA was accepted")
	}
}
