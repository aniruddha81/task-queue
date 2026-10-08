// Command devcerts writes a fresh development CA and one certificate per service into
// deploy/local/certs (git-ignored), plus a random local JWT signing key in secrets.env.
// Run once before `docker compose up`:
//
//	go run ./cmd/devcerts
package main

import (
	"crypto/rand"
	"encoding/base64"
	"flag"
	"log"
	"os"
	"path/filepath"

	"github.com/aniruddha81/task-queue/internal/devca"
)

func main() {
	dir := flag.String("out", "deploy/local/certs", "output directory")
	flag.Parse()

	ca, err := devca.New()
	if err != nil {
		log.Fatal(err)
	}
	if err := os.MkdirAll(*dir, 0o755); err != nil {
		log.Fatal(err)
	}
	write := func(name string, data []byte) {
		// 0644 so every container user can read it; these keys are for local use only.
		if err := os.WriteFile(filepath.Join(*dir, name), data, 0o644); err != nil {
			log.Fatal(err)
		}
	}
	write("ca.crt", ca.CertPEM())
	for _, svc := range []string{"gateway", "auth", "jobs", "dispatch", "scheduler", "worker", "migrate", "postgres", "sinks"} {
		crt, key, err := ca.Issue(svc, svc, "localhost")
		if err != nil {
			log.Fatal(err)
		}
		write(svc+".crt", crt)
		write(svc+".key", key)
	}
	// The local JWT signing key, read by compose's auth service.
	seed := make([]byte, 32)
	rand.Read(seed)
	write("secrets.env", []byte("JWT_PRIVATE_KEY="+base64.StdEncoding.EncodeToString(seed)+"\n"))
	log.Printf("wrote CA, service certificates and secrets.env to %s", *dir)
}
