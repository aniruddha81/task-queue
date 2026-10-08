// Package tlsconf loads a service's certificates: <dir>/ca.crt plus <dir>/<name>.crt and
// <name>.key, and builds the TLS configs for mutual TLS between services.
package tlsconf

import (
	"crypto/tls"
	"crypto/x509"
	"errors"
	"os"
	"path/filepath"
)

type Bundle struct {
	cert tls.Certificate
	pool *x509.CertPool
}

func Load(dir, name string) (*Bundle, error) {
	cert, err := tls.LoadX509KeyPair(filepath.Join(dir, name+".crt"), filepath.Join(dir, name+".key"))
	if err != nil {
		return nil, err
	}
	ca, err := os.ReadFile(filepath.Join(dir, "ca.crt"))
	if err != nil {
		return nil, err
	}
	return FromPEM(cert, ca)
}

func FromPEM(cert tls.Certificate, caPEM []byte) (*Bundle, error) {
	pool := x509.NewCertPool()
	if !pool.AppendCertsFromPEM(caPEM) {
		return nil, errors.New("no CA certificate found")
	}
	return &Bundle{cert, pool}, nil
}

// Server accepts only clients presenting a certificate from the project CA.
func (b *Bundle) Server() *tls.Config {
	return &tls.Config{
		Certificates: []tls.Certificate{b.cert},
		ClientCAs:    b.pool,
		ClientAuth:   tls.RequireAndVerifyClientCert,
		MinVersion:   tls.VersionTLS13,
	}
}

// Public serves without asking for a client certificate: the gateway's internet side.
func (b *Bundle) Public() *tls.Config {
	return &tls.Config{Certificates: []tls.Certificate{b.cert}, MinVersion: tls.VersionTLS12}
}

// Client trusts only the project CA and presents this service's certificate.
func (b *Bundle) Client() *tls.Config {
	return &tls.Config{
		Certificates: []tls.Certificate{b.cert},
		RootCAs:      b.pool,
		MinVersion:   tls.VersionTLS13,
	}
}
