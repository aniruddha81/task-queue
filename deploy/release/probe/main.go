// Command probe is the evidence for "zero downtime": during a rollout it sends about 10
// requests a second through the public name, from outside both clouds, and never retries.
// Each request opens a new connection, so DNS is resolved afresh and a gateway that
// Traffic Manager has stopped returning stops receiving traffic. It mixes a public read
// (/version), an authenticated read and an idempotent submit. On SIGTERM it prints a
// summary and exits 1 if any request failed (or after -for).
//
//	PROBE_PASSWORD=... go run ./deploy/release/probe -url https://tq-x.trafficmanager.net
package main

import (
	"bytes"
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"os"
	"os/signal"
	"sync"
	"sync/atomic"
	"syscall"
	"time"
)

func main() {
	base := flag.String("url", "", "the public URL")
	email := flag.String("email", "smoke@example.com", "user to sign in as (PROBE_PASSWORD has the password)")
	rate := flag.Float64("rate", 10, "requests per second")
	duration := flag.Duration("for", 0, "stop after this long (0: until SIGTERM)")
	flag.Parse()
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	if *duration > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, *duration)
		defer cancel()
	}

	client := &http.Client{
		Timeout: 10 * time.Second,
		Transport: &http.Transport{
			DisableKeepAlives: true, // a new connection, and a fresh DNS answer, every request
			// Several addresses: each gets part of the timeout, so a dead gateway's address
			// falls through to the next one, as a browser does.
			DialContext: (&net.Dialer{Timeout: 6 * time.Second}).DialContext,
		},
	}
	token, err := login(ctx, client, *base, *email, os.Getenv("PROBE_PASSWORD"))
	if err != nil {
		log.Fatalf("probe: sign-in before the rollout: %v", err)
	}
	renewed := time.Now()

	// Each request runs on its own, so a slow one never lowers the rate. In-flight requests
	// finish (with their own timeout) before the summary.
	var sent, failed atomic.Int64
	var wg sync.WaitGroup
	start := time.Now()
	tick := time.NewTicker(time.Duration(float64(time.Second) / *rate))
	defer tick.Stop()
	for n := 0; ; n++ {
		select {
		case <-ctx.Done():
			wg.Wait()
			fmt.Printf("probe: %d requests in %v, %d failed\n", sent.Load(), time.Since(start).Round(time.Second), failed.Load())
			if failed.Load() > 0 {
				os.Exit(1)
			}
			return
		case <-tick.C:
		}
		if time.Since(renewed) > 20*time.Minute { // tokens last 60 minutes
			if t, err := login(ctx, client, *base, *email, os.Getenv("PROBE_PASSWORD")); err == nil {
				token, renewed = t, time.Now()
			}
		}
		var req *http.Request
		switch n % 3 {
		case 0:
			req, _ = http.NewRequest("GET", *base+"/version", nil)
		case 1:
			req, _ = http.NewRequest("GET", *base+"/v1/jobs?limit=1", nil)
			req.Header.Set("Authorization", "Bearer "+token)
		case 2:
			body := `{"queue":"default","type":"chaos.sleep","payload":{"ms":10}}`
			req, _ = http.NewRequest("POST", *base+"/v1/jobs", bytes.NewBufferString(body))
			req.Header.Set("Authorization", "Bearer "+token)
			req.Header.Set("Idempotency-Key", fmt.Sprintf("probe-%d-%d", start.Unix(), n))
		}
		sent.Add(1)
		wg.Go(func() {
			resp, err := client.Do(req)
			if err == nil {
				io.Copy(io.Discard, resp.Body)
				resp.Body.Close()
				if resp.StatusCode/100 != 2 {
					err = fmt.Errorf("%s", resp.Status)
				}
			}
			if err != nil {
				failed.Add(1)
				fmt.Printf("probe: FAILED %s %s at %s: %v\n", req.Method, req.URL.Path, time.Now().UTC().Format("15:04:05.000"), err)
			}
		})
	}
}

func login(ctx context.Context, c *http.Client, base, email, password string) (string, error) {
	body, _ := json.Marshal(map[string]string{"email": email, "password": password})
	req, _ := http.NewRequestWithContext(ctx, "POST", base+"/v1/auth/login", bytes.NewReader(body))
	resp, err := c.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	var out struct{ Token string }
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil || resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("login: %s", resp.Status)
	}
	return out.Token, nil
}
