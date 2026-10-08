// Command bench measures the local stack: drain throughput (jobs/s) and submit-to-start
// latency against worker count, with synchronous replication on and off. It writes
// docs/results/load-local.md. Run it against an otherwise idle stack:
//
//	go run ./cmd/bench -workers 1,2,4,8
//
// Jobs are inserted straight into the database (the API isn't what's being measured) as
// chaos.sleep with 0 ms, so the numbers are the scheduler's own overhead: claim, heartbeat,
// complete, and the synchronous commit each of them waits for.
package main

import (
	"context"
	"flag"
	"fmt"
	"log"
	"os"
	"os/exec"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/jackc/pgx/v5"
)

type result struct {
	workers       int
	sync          bool
	drainN        int
	drainSeconds  float64
	p50, p95, p99 float64 // steady-load submit-to-start latency, seconds
	steadyRate    int
	deadTuples    int64
	waits         map[string]int // sampled wait events during the drain
}

func main() {
	db := flag.String("db", "postgres://postgres:superuser-local-only@localhost:5432,localhost:5434/jobs?sslmode=require&target_session_attrs=read-write", "jobs database")
	project := flag.String("project", "taskqueue", "compose project")
	compose := flag.String("compose", "deploy/local/compose.yml", "compose file")
	workerList := flag.String("workers", "1,2,4,8", "worker counts to measure")
	drainN := flag.Int("drain", 10000, "backlog size for the throughput test")
	rate := flag.Int("rate", 50, "jobs per second for the latency test")
	steady := flag.Duration("steady", 30*time.Second, "length of the latency test")
	out := flag.String("out", "docs/results/load-local.md", "report")
	flag.Parse()
	ctx := context.Background()

	var results []result
	for _, sync := range []bool{true, false} {
		setSync(*project, sync)
		for _, w := range strings.Split(*workerList, ",") {
			n, _ := strconv.Atoi(w)
			run(exec.Command("docker", "compose", "-f", *compose, "up", "-d", "--no-recreate", "--scale", "worker="+w, "worker"))
			time.Sleep(8 * time.Second) // workers start and begin long-polling
			r := result{workers: n, sync: sync, drainN: *drainN, steadyRate: *rate}
			measure(ctx, *db, &r, *steady)
			log.Printf("workers=%d sync=%v: %.0f jobs/s, latency p50 %.0f ms p95 %.0f ms", n, sync,
				float64(r.drainN)/r.drainSeconds, r.p50*1000, r.p95*1000)
			results = append(results, r)
		}
	}
	setSync(*project, true) // leave the cluster as configured
	run(exec.Command("docker", "compose", "-f", *compose, "up", "-d", "--no-recreate", "--scale", "worker=2", "worker"))
	if err := os.WriteFile(*out, []byte(report(results)), 0o644); err != nil {
		log.Fatal(err)
	}
	log.Printf("wrote %s", *out)
}

func measure(ctx context.Context, url string, r *result, steady time.Duration) {
	conn, err := pgx.Connect(ctx, url)
	if err != nil {
		log.Fatal(err)
	}
	defer conn.Close(ctx)
	tag := fmt.Sprintf("bench-%d", time.Now().UnixNano())

	// Throughput: insert a backlog in one statement, then time the drain on the DB clock.
	if _, err := conn.Exec(ctx, `INSERT INTO jobs (owner_id, idempotency_key, request, queue, type, payload)
		SELECT gen_random_uuid(), $1 || '-d-' || i, '{}', 'default', 'chaos.sleep', '{"ms":0}'
		FROM generate_series(1, $2) i`, tag, r.drainN); err != nil {
		log.Fatal(err)
	}
	stopSampling := sampleWaits(url, &r.waits)
	waitDone(ctx, conn, tag+"-d-%")
	stopSampling()
	if err := conn.QueryRow(ctx, `SELECT EXTRACT(EPOCH FROM max(finished_at) - min(created_at))::float8 FROM jobs
		WHERE idempotency_key LIKE $1`, tag+"-d-%").Scan(&r.drainSeconds); err != nil {
		log.Fatal("drain query: ", err)
	}

	// Latency: a steady trickle, measuring run_at -> first attempt's start.
	tick := time.NewTicker(time.Second / 10)
	per := max(r.steadyRate/10, 1)
	for i, end := 0, time.Now().Add(steady); time.Now().Before(end); i++ {
		<-tick.C
		if _, err := conn.Exec(ctx, `INSERT INTO jobs (owner_id, idempotency_key, request, queue, type, payload)
			SELECT gen_random_uuid(), $1 || '-s-' || $2::text || '-' || j, '{}', 'default', 'chaos.sleep', '{"ms":0}'
			FROM generate_series(1, $3::int) j`, tag, strconv.Itoa(i), per); err != nil {
			log.Fatal("steady insert: ", err)
		}
	}
	tick.Stop()
	waitDone(ctx, conn, tag+"-s-%")
	err = conn.QueryRow(ctx, `SELECT
		percentile_cont(0.5)  WITHIN GROUP (ORDER BY s), percentile_cont(0.95) WITHIN GROUP (ORDER BY s),
		percentile_cont(0.99) WITHIN GROUP (ORDER BY s)
		FROM (SELECT EXTRACT(EPOCH FROM a.started_at - j.run_at)::float8 AS s FROM jobs j
		      JOIN job_attempts a ON a.job_id = j.id AND a.attempt = 1 WHERE j.idempotency_key LIKE $1) x`,
		tag+"-s-%").Scan(&r.p50, &r.p95, &r.p99)
	if err != nil {
		log.Fatal("latency query: ", err)
	}
	conn.QueryRow(ctx, `SELECT n_dead_tup FROM pg_stat_user_tables WHERE relname = 'jobs'`).Scan(&r.deadTuples)
}

func waitDone(ctx context.Context, conn *pgx.Conn, like string) {
	for {
		var left int
		conn.QueryRow(ctx, `SELECT count(*) FROM jobs WHERE idempotency_key LIKE $1 AND state <> 'succeeded'`, like).Scan(&left)
		if left == 0 {
			return
		}
		time.Sleep(250 * time.Millisecond)
	}
}

// sampleWaits counts what active backends are waiting on, every 200 ms, until stopped.
func sampleWaits(url string, into *map[string]int) (stop func()) {
	ctx, cancel := context.WithCancel(context.Background())
	var wg sync.WaitGroup
	counts := map[string]int{}
	wg.Go(func() {
		conn, err := pgx.Connect(ctx, url)
		if err != nil {
			return
		}
		defer conn.Close(context.Background())
		for ctx.Err() == nil {
			rows, _ := conn.Query(ctx, `SELECT coalesce(wait_event_type || ':' || wait_event, 'CPU') FROM pg_stat_activity
				WHERE state = 'active' AND backend_type = 'client backend' AND pid <> pg_backend_pid()`)
			for rows.Next() {
				var w string
				rows.Scan(&w)
				counts[w]++
			}
			time.Sleep(200 * time.Millisecond)
		}
	})
	return func() { cancel(); wg.Wait(); *into = counts }
}

// setSync turns Patroni's synchronous mode on or off and waits for it to take effect.
func setSync(project string, on bool) {
	for _, node := range []string{"pg1", "pg2"} {
		cmd := exec.Command("docker", "exec", project+"-"+node+"-1", "patronictl", "-c", "/etc/patroni.yml",
			"edit-config", "--force", "--set", "synchronous_mode="+strconv.FormatBool(on))
		if cmd.Run() == nil {
			time.Sleep(10 * time.Second) // Patroni applies it on its next loop
			return
		}
	}
	log.Fatal("could not change synchronous_mode on either node")
}

func run(cmd *exec.Cmd) {
	if out, err := cmd.CombinedOutput(); err != nil {
		log.Fatalf("%v: %v\n%s", cmd.Args, err, out)
	}
}

func report(rs []result) string {
	var b strings.Builder
	b.WriteString("# Load baseline (local)\n\n")
	fmt.Fprintf(&b, "Measured %s on one laptop (Docker Desktop, Windows), with the full local stack: two Patroni nodes with 5.5 ms of simulated cross-cloud round trip between them, one dispatch, workers with 8 slots each. Jobs are `chaos.sleep` with 0 ms, so this measures the scheduler's own overhead. Absolute numbers depend on the machine; the shape is the point.\n\n", time.Now().Format("2006-01-02"))
	b.WriteString("| Sync replication | Workers | Drain throughput | Latency p50 | p95 | p99 | Dead tuples after |\n| --- | --- | --- | --- | --- | --- | --- |\n")
	for _, r := range rs {
		mode := "off"
		if r.sync {
			mode = "on"
		}
		fmt.Fprintf(&b, "| %s | %d | %.0f jobs/s | %.0f ms | %.0f ms | %.0f ms | %d |\n", mode, r.workers,
			float64(r.drainN)/r.drainSeconds, r.p50*1000, r.p95*1000, r.p99*1000, r.deadTuples)
	}
	b.WriteString("\nThroughput is a backlog of jobs drained; latency is from `run_at` to the first attempt's start, under a steady load.\n\n## What the database waited on during the drains\n\nSampled every 200 ms from `pg_stat_activity` (active client backends):\n\n")
	for _, r := range rs {
		type kv struct {
			k string
			v int
		}
		var top []kv
		total := 0
		for k, v := range r.waits {
			top = append(top, kv{k, v})
			total += v
		}
		sort.Slice(top, func(i, j int) bool { return top[i].v > top[j].v })
		mode := "sync"
		if !r.sync {
			mode = "async"
		}
		parts := []string{}
		for i, x := range top {
			if i == 4 {
				break
			}
			parts = append(parts, fmt.Sprintf("%s %d%%", x.k, x.v*100/max(total, 1)))
		}
		fmt.Fprintf(&b, "- %s, %d workers: %s\n", mode, r.workers, strings.Join(parts, ", "))
	}
	return b.String()
}
