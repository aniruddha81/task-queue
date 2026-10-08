// Command torture is the chaos test: it loads the local stack with mixed jobs through
// the gateway, injects faults on a seeded timetable, waits for everything to settle,
// and checks guarantees G1–G8 against three independent sources: its own fsynced log of
// acknowledgements, the jobs database, and the sinks (plus Mailpit for email).
//
//	go run ./cmd/devcerts && docker compose -f deploy/local/compose.yml up --build -d
//	go run ./cmd/torture -duration 5m
//
// A guarantee whose fault never fired fails: nothing was proven. The report is written
// to docs/results/, and the exit status is non-zero if any guarantee failed.
package main

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"flag"
	"fmt"
	"log"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"time"
)

type config struct {
	gateway, ca, email, password string
	jobsDB, sinksDB, mailpit     string
	project                      string
	duration, quiesce            time.Duration
	rate                         float64
	seed                         uint64
	faults                       bool
	out                          string
}

func main() {
	var c config
	flag.StringVar(&c.gateway, "gateway", "https://localhost:8443", "gateway URL")
	flag.StringVar(&c.ca, "ca", "deploy/local/certs/ca.crt", "CA certificate for the gateway")
	flag.StringVar(&c.email, "email", "demo@example.com", "user to submit as")
	flag.StringVar(&c.password, "password", "demo-password-1", "that user's password")
	flag.StringVar(&c.jobsDB, "jobs-db", "postgres://postgres:superuser-local-only@localhost:5432,localhost:5434/jobs?sslmode=require&target_session_attrs=read-write", "jobs database (read by the checker)")
	flag.StringVar(&c.sinksDB, "sinks-db", "postgres://postgres:sinks-local-only@localhost:5433/sinks?sslmode=disable", "sinks database")
	flag.StringVar(&c.mailpit, "mailpit", "http://localhost:8025", "Mailpit API")
	flag.StringVar(&c.project, "project", "taskqueue", "docker compose project")
	flag.DurationVar(&c.duration, "duration", 5*time.Minute, "load and fault phase")
	flag.DurationVar(&c.quiesce, "quiesce", 10*time.Minute, "longest wait for every job to finish (G5)")
	flag.Float64Var(&c.rate, "rate", 8, "jobs submitted per second")
	flag.Uint64Var(&c.seed, "seed", uint64(time.Now().UnixNano()), "seed for the job mix and the fault timetable")
	flag.BoolVar(&c.faults, "faults", true, "inject faults")
	flag.StringVar(&c.out, "out", "docs/results", "report directory")
	flag.Parse()

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()
	ok, err := run(ctx, c)
	if err != nil {
		log.Fatal(err)
	}
	if !ok {
		os.Exit(1)
	}
}

func run(ctx context.Context, c config) (bool, error) {
	// The seed fixes the job mix and fault timetable; the start time keeps keys and the
	// schedule name unique when a seed is rerun against the same database.
	runID := fmt.Sprintf("r%dt%d", c.seed, time.Now().Unix())
	log.Printf("run %s: seed %d, %v of load at %.1f jobs/s, faults=%v", runID, c.seed, c.duration, c.rate, c.faults)

	pem, err := os.ReadFile(c.ca)
	if err != nil {
		return false, err
	}
	pool := x509.NewCertPool()
	pool.AppendCertsFromPEM(pem)
	client := &http.Client{Timeout: 15 * time.Second,
		Transport: &http.Transport{TLSClientConfig: &tls.Config{RootCAs: pool}, MaxIdleConnsPerHost: 64}}

	ackPath := filepath.Join(os.TempDir(), "torture-"+runID+".acks")
	l, err := newLoad(c, runID, client, ackPath)
	if err != nil {
		return false, err
	}
	defer l.acks.Close()
	if err := l.setup(ctx); err != nil {
		return false, err
	}
	f := newFaults(c)
	epochBefore, _ := readEpoch(ctx, c.jobsDB)
	start := time.Now()

	loadEnd := start.Add(c.duration)
	faultsDone := make(chan struct{})
	go func() {
		defer close(faultsDone)
		if c.faults {
			f.run(ctx, loadEnd)
		}
	}()
	l.run(ctx, loadEnd)
	<-faultsDone
	f.heal()
	log.Printf("load done: %d submitted, %d acknowledged; quiescing", l.submitted.Load(), l.acked.Load())

	if err := l.stopCron(ctx); err != nil {
		log.Printf("pause schedule: %v", err)
	}
	settled := quiesce(ctx, c, l, runID)
	epochAfter, _ := readEpoch(ctx, c.jobsDB)

	r, err := check(ctx, c, l, runID, start, settled, epochAfter-epochBefore, f.counts())
	if err != nil {
		return false, err
	}
	path, err := r.write(c.out, runID)
	if err != nil {
		return false, err
	}
	fmt.Println(r.markdown())
	log.Printf("report: %s (acks: %s)", path, ackPath)
	return r.passed(), nil
}

// quiesce waits until every job of this run has finished (G5), up to c.quiesce.
func quiesce(ctx context.Context, c config, l *load, runID string) bool {
	deadline := time.Now().Add(c.quiesce)
	for {
		n, err := unfinished(ctx, c.jobsDB, runID, l.scheduleID)
		if err == nil && n == 0 {
			log.Print("all jobs finished")
			return true
		}
		if time.Now().After(deadline) || ctx.Err() != nil {
			log.Printf("quiesce limit reached: %d jobs unfinished (err %v)", n, err)
			return false
		}
		time.Sleep(2 * time.Second)
	}
}
