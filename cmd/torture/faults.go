package main

import (
	"context"
	"fmt"
	"log"
	"math/rand/v2"
	"os/exec"
	"strings"
	"sync"
	"time"
)

// A fault is applied to one container and reverted after its duration by this harness.
// heal() reverts everything at the end, and restart policies cover a harness crash.
type fault struct {
	name    string
	targets []string // compose service-index names, e.g. worker-1
	min     time.Duration
	max     time.Duration
	apply   func(container string) error
	revert  func(container string) error
}

type faults struct {
	c       config
	catalog []fault
	mu      sync.Mutex
	busy    map[string]bool
	count   map[string]int
}

func newFaults(c config) *faults {
	f := &faults{c: c, busy: map[string]bool{}, count: map[string]int{}}
	start := func(ct string) error { return docker("start", ct) }
	network := c.project + "_default"
	f.catalog = []fault{
		{"kill", []string{"worker-1", "worker-2", "dispatch-1", "scheduler-1", "scheduler-2", "jobs-1", "gateway-1", "auth-1"},
			3 * time.Second, 10 * time.Second, func(ct string) error { return docker("kill", "-s", "KILL", ct) }, start},
		{"pause-worker", []string{"worker-1", "worker-2"}, 35 * time.Second, 45 * time.Second, // past the 30 s lease
			func(ct string) error { return docker("pause", ct) }, func(ct string) error { return docker("unpause", ct) }},
		{"pause-dispatch", []string{"dispatch-1"}, 12 * time.Second, 18 * time.Second, // past the 10 s idle-transaction limit
			func(ct string) error { return docker("pause", ct) }, func(ct string) error { return docker("unpause", ct) }},
		{"pause-scheduler", []string{"scheduler-1", "scheduler-2"}, 18 * time.Second, 25 * time.Second, // past the 15 s leader lease
			func(ct string) error { return docker("pause", ct) }, func(ct string) error { return docker("unpause", ct) }},
		{"crash-postgres", []string{"postgres-1"}, 2 * time.Second, 5 * time.Second,
			func(ct string) error { return docker("kill", "-s", "KILL", ct) }, start},
		{"cut-network", []string{"worker-1", "worker-2", "dispatch-1", "scheduler-1", "scheduler-2"}, 10 * time.Second, 30 * time.Second,
			func(ct string) error { return docker("network", "disconnect", network, ct) },
			func(ct string) error { return reconnect(network, ct) }},
	}
	return f
}

// run starts a fault every 4–10 s (seeded) until end; faults may overlap, never on the
// same container.
func (f *faults) run(ctx context.Context, end time.Time) {
	rng := rand.New(rand.NewPCG(f.c.seed, 2))
	var wg sync.WaitGroup
	for time.Now().Before(end) && ctx.Err() == nil {
		time.Sleep(time.Duration(4+rng.IntN(7)) * time.Second)
		ft := f.catalog[rng.IntN(len(f.catalog))]
		ct := f.c.project + "-" + ft.targets[rng.IntN(len(ft.targets))]
		hold := ft.min + time.Duration(rng.Int64N(int64(ft.max-ft.min)+1))
		f.mu.Lock()
		if f.busy[ct] {
			f.mu.Unlock()
			continue
		}
		f.busy[ct] = true
		f.mu.Unlock()
		wg.Go(func() {
			defer func() { f.mu.Lock(); delete(f.busy, ct); f.mu.Unlock() }()
			if err := ft.apply(ct); err != nil {
				log.Printf("fault %s %s: %v", ft.name, ct, err)
				return
			}
			f.mu.Lock()
			f.count[ft.name]++
			f.mu.Unlock()
			log.Printf("fault: %s %s for %v", ft.name, ct, hold.Round(time.Second))
			time.Sleep(hold)
			if err := ft.revert(ct); err != nil {
				log.Printf("revert %s %s: %v", ft.name, ct, err)
			}
		})
	}
	wg.Wait()
}

// heal undoes anything still applied: unpause, reconnect, start every container of the
// project. Errors mean "already fine" and are ignored.
func (f *faults) heal() {
	out, _ := exec.Command("docker", "ps", "-a", "--filter", "label=com.docker.compose.project="+f.c.project,
		"--format", "{{.Names}}").Output()
	for _, ct := range strings.Fields(string(out)) {
		if strings.Contains(ct, "migrate") {
			continue
		}
		docker("unpause", ct)
		reconnect(f.c.project+"_default", ct)
		docker("start", ct)
	}
}

func (f *faults) counts() map[string]int {
	f.mu.Lock()
	defer f.mu.Unlock()
	out := map[string]int{}
	for k, v := range f.count {
		out[k] = v
	}
	return out
}

// reconnect re-attaches a container under its compose service name too: a plain
// "docker network connect" restores only the container name, and then nothing can find
// the service by name (found the hard way: workers lost dispatch for good).
func reconnect(network, container string) error {
	out, err := exec.Command("docker", "inspect", "--format",
		`{{index .Config.Labels "com.docker.compose.service"}}`, container).Output()
	if err != nil {
		return err
	}
	return docker("network", "connect", "--alias", strings.TrimSpace(string(out)), network, container)
}

func docker(args ...string) error {
	out, err := exec.Command("docker", args...).CombinedOutput()
	if err != nil {
		return fmt.Errorf("docker %s: %v: %s", strings.Join(args, " "), err, strings.TrimSpace(string(out)))
	}
	return nil
}
