package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"math/rand/v2"
	"net/http"
	"os"
	"strconv"
	"sync"
	"sync/atomic"
	"time"

	"github.com/jackc/pgx/v5"
)

// spec is one planned submission, fully decided by the seed.
type spec struct {
	key, typ string
	body     []byte
	repeat   bool   // submit again with the same key and body (G2: same job back)
	conflict []byte // then submit this different body with the same key (G2: 409)
	delayed  bool   // has a future run_at (G8)
}

type ack struct {
	key, id, typ string
}

type load struct {
	c          config
	runID      string
	client     *http.Client
	token      atomic.Pointer[string]
	acks       *os.File
	scheduleID string
	firstTick  time.Time // the schedule's first expected tick (G6)

	mu        sync.Mutex
	acked     atomic.Int64
	submitted atomic.Int64
	log       []ack           // every acknowledgement, in order
	conflicts int             // expected 409s seen
	bad       []string        // protocol surprises (unexpected 409/400)
	specs     map[string]spec // by key
}

func newLoad(c config, runID string, client *http.Client, ackPath string) (*load, error) {
	f, err := os.OpenFile(ackPath, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0o644)
	if err != nil {
		return nil, err
	}
	return &load{c: c, runID: runID, client: client, acks: f, specs: map[string]spec{}}, nil
}

func (l *load) setup(ctx context.Context) error {
	if err := l.login(ctx); err != nil {
		return err
	}
	// Mailpit is test infrastructure: start each run with an empty mailbox.
	req, _ := http.NewRequestWithContext(ctx, "DELETE", l.c.mailpit+"/api/v1/messages", nil)
	if resp, err := http.DefaultClient.Do(req); err == nil {
		resp.Body.Close()
	}
	// A cron schedule ticking every minute, starting 3 minutes in the past so the leader
	// has catch-up work from the first second.
	body := fmt.Sprintf(`{"name":"torture-%s","cron":"* * * * *","timezone":"UTC","job":{"queue":"default","type":"report.daily"}}`, l.runID)
	// A just-started stack may not reach jobs yet (502), or jobs may not have auth's keys
	// yet (401). Neither creates a schedule, so retrying them can't make a duplicate.
	code, resp, err := l.post(ctx, "/v1/schedules", "", []byte(body))
	for wait := time.Second; err == nil && (code == http.StatusBadGateway || code == http.StatusUnauthorized) && wait < 30*time.Second; wait *= 2 {
		log.Printf("create schedule: %d %v; retrying", code, err)
		time.Sleep(wait)
		code, resp, err = l.post(ctx, "/v1/schedules", "", []byte(body))
	}
	if err != nil || code != http.StatusCreated {
		return fmt.Errorf("create schedule: %d %s %v", code, resp, err)
	}
	var sc struct{ ID string }
	json.Unmarshal(resp, &sc)
	l.scheduleID = sc.ID
	conn, err := pgx.Connect(ctx, l.c.jobsDB)
	if err != nil {
		return err
	}
	defer conn.Close(ctx)
	return conn.QueryRow(ctx, `UPDATE schedules SET next_tick_at = date_trunc('minute', now()) - interval '3 minutes'
		WHERE id = $1 RETURNING next_tick_at`, l.scheduleID).Scan(&l.firstTick)
}

func (l *load) login(ctx context.Context) error {
	body, _ := json.Marshal(map[string]string{"email": l.c.email, "password": l.c.password})
	for wait := time.Second; ; wait = min(2*wait, 10*time.Second) {
		req, _ := http.NewRequestWithContext(ctx, "POST", l.c.gateway+"/v1/auth/login", bytes.NewReader(body))
		resp, err := l.client.Do(req)
		if err == nil {
			var out struct{ Token string }
			json.NewDecoder(resp.Body).Decode(&out)
			resp.Body.Close()
			if resp.StatusCode == http.StatusOK && out.Token != "" {
				l.token.Store(&out.Token)
				return nil
			}
			err = fmt.Errorf("login: %s", resp.Status)
		}
		if ctx.Err() != nil {
			return err
		}
		log.Printf("%v; retrying", err)
		time.Sleep(wait)
	}
}

func (l *load) post(ctx context.Context, path, key string, body []byte) (int, []byte, error) {
	req, err := http.NewRequestWithContext(ctx, "POST", l.c.gateway+path, bytes.NewReader(body))
	if err != nil {
		return 0, nil, err
	}
	req.Header.Set("Authorization", "Bearer "+*l.token.Load())
	if key != "" {
		req.Header.Set("Idempotency-Key", key)
	}
	resp, err := l.client.Do(req)
	if err != nil {
		return 0, nil, err
	}
	defer resp.Body.Close()
	b, _ := io.ReadAll(resp.Body)
	return resp.StatusCode, b, nil
}

// run submits planned jobs at the configured rate until end.
func (l *load) run(ctx context.Context, end time.Time) {
	rng := rand.New(rand.NewPCG(l.c.seed, 1))
	tick := time.NewTicker(time.Duration(float64(time.Second) / l.c.rate))
	defer tick.Stop()
	sem := make(chan struct{}, 64)
	var wg sync.WaitGroup
	for n := 0; time.Now().Before(end) && ctx.Err() == nil; n++ {
		<-tick.C
		s := l.plan(rng, n)
		l.mu.Lock()
		l.specs[s.key] = s
		l.mu.Unlock()
		sem <- struct{}{}
		wg.Go(func() {
			defer func() { <-sem }()
			l.submitSpec(ctx, s, end.Add(3*time.Minute))
		})
	}
	wg.Wait()
}

func (l *load) plan(rng *rand.Rand, n int) spec {
	s := spec{key: fmt.Sprintf("%s-%d", l.runID, n)}
	job := map[string]any{"queue": "default", "priority": rng.IntN(11) - 5}
	acct := func() string { return fmt.Sprintf("%s:a%d", l.runID, rng.IntN(10)) }
	switch r := rng.Float64(); {
	case r < 0.35:
		from, to := acct(), acct()
		for to == from {
			to = acct()
		}
		job["type"], job["payload"] = "ledger.transfer", map[string]any{"from": from, "to": to, "amount": 1 + rng.IntN(100)}
		if rng.Float64() < 0.1 {
			s.delayed = true
			job["run_at"] = time.Now().Add(time.Duration(5+rng.IntN(15)) * time.Second).UTC().Format(time.RFC3339Nano)
		}
	case r < 0.55:
		job["type"], job["payload"] = "webhook.deliver", map[string]any{"url": "https://sinks:8090/webhook", "body": map[string]int{"n": n}}
	case r < 0.63:
		job["type"], job["payload"] = "email.send", map[string]any{"to": fmt.Sprintf("user%d@example.com", n), "subject": l.runID + " #" + strconv.Itoa(n)}
	case r < 0.83:
		job["type"], job["payload"] = "chaos.sleep", map[string]any{"ms": 50 + rng.IntN(1500)}
	case r < 0.91:
		job["type"], job["payload"] = "chaos.fail", map[string]any{"p": 0.5}
	case r < 0.98:
		// Overruns its 2 s deadline: its completion arrives after the lease is gone (G3).
		job["type"], job["payload"], job["timeout_seconds"], job["max_attempts"] = "chaos.overrun", map[string]any{"ms": 4000}, 2, 2
	default:
		job["type"], job["payload"], job["max_attempts"] = "chaos.crash", map[string]any{"p": 0.3}, 3
	}
	s.typ = job["type"].(string)
	s.body, _ = json.Marshal(job)
	s.repeat = rng.Float64() < 0.10
	if rng.Float64() < 0.02 {
		job["priority"] = 99 // any change makes it a different request
		s.conflict, _ = json.Marshal(job)
	}
	return s
}

func (l *load) submitSpec(ctx context.Context, s spec, giveUp time.Time) {
	l.submitted.Add(1)
	if s.repeat { // the same request twice at once, as a confused client might
		var wg sync.WaitGroup
		wg.Go(func() { l.submit(ctx, s, s.body, giveUp, false) })
		l.submit(ctx, s, s.body, giveUp, false)
		wg.Wait()
	} else {
		l.submit(ctx, s, s.body, giveUp, false)
	}
	if s.conflict != nil {
		l.submit(ctx, s, s.conflict, giveUp, true)
	}
}

// submit retries with the same key until the gateway answers definitively. Retrying is
// always safe: that is what the idempotency key is for (G1, G2).
func (l *load) submit(ctx context.Context, s spec, body []byte, giveUp time.Time, wantConflict bool) {
	for wait := 250 * time.Millisecond; time.Now().Before(giveUp) && ctx.Err() == nil; wait = min(2*wait, 5*time.Second) {
		code, resp, err := l.post(ctx, "/v1/jobs", s.key, body)
		switch {
		case err != nil || code >= 500:
			// No answer, or "outcome unknown": retry with the same key.
		case code == http.StatusTooManyRequests:
			time.Sleep(time.Second)
			continue
		case code == http.StatusUnauthorized:
			l.login(ctx)
			continue
		case code == http.StatusConflict:
			l.mu.Lock()
			if wantConflict {
				l.conflicts++
			} else {
				l.bad = append(l.bad, fmt.Sprintf("%s: unexpected 409 %s", s.key, resp))
			}
			l.mu.Unlock()
			return
		case code == http.StatusOK || code == http.StatusCreated:
			if wantConflict {
				l.mu.Lock()
				l.bad = append(l.bad, fmt.Sprintf("%s: changed body accepted (%d)", s.key, code))
				l.mu.Unlock()
				return
			}
			var job struct{ ID string }
			json.Unmarshal(resp, &job)
			l.recordAck(ack{s.key, job.ID, s.typ})
			return
		default:
			l.mu.Lock()
			l.bad = append(l.bad, fmt.Sprintf("%s: %d %s", s.key, code, resp))
			l.mu.Unlock()
			return
		}
		time.Sleep(wait)
	}
}

// recordAck makes an acknowledgement durable before counting it, so the checker's view of
// "acknowledged" can't be lost with the harness.
func (l *load) recordAck(a ack) {
	l.mu.Lock()
	defer l.mu.Unlock()
	fmt.Fprintf(l.acks, "%s %s %s\n", a.key, a.id, a.typ)
	l.acks.Sync()
	l.log = append(l.log, a)
	l.acked.Add(1)
}

// stopCron pauses the run's schedule so quiescing has a fixed end.
func (l *load) stopCron(ctx context.Context) error {
	for i := 0; i < 30; i++ {
		code, _, err := l.post(ctx, "/v1/schedules/"+l.scheduleID+"/pause", "", nil)
		if err == nil && code == http.StatusOK {
			return nil
		}
		if code == http.StatusUnauthorized {
			l.login(ctx)
		}
		time.Sleep(2 * time.Second)
	}
	return fmt.Errorf("could not pause schedule %s", l.scheduleID)
}
