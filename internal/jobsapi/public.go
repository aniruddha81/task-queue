package jobsapi

// The public routes behind the live page (web/app/live), for visitors without an account.
// They need no token; the gateway rate-limits POST /v1/public/try per address and overall.
//
//	GET  /v1/public/stats     workers per cloud, the leader's epoch, and the last 24 hours
//	POST /v1/public/try       {"scenario": "pay" | "freeze" | "flaky" | "later"} runs one demo job
//	GET  /v1/public/try/{id}  a demo job and its attempts
//
// Demo jobs belong to one guest owner and are fixed by the scenario: a visitor picks which
// one, never its type or payload.

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"sync"
	"time"
	"uuid"

	"github.com/aniruddha81/task-queue/internal/pgretry"
	"github.com/aniruddha81/task-queue/internal/queue"
)

// guest owns every demo job. No user has this ID, so no one's dashboard shows them.
var guest = uuid.MustParse("00000000-0000-7000-8000-000000000001")

var scenarios = map[string]func() string{
	// A plain payment: one attempt, exactly once.
	"pay": func() string {
		return `{"queue":"default","type":"ledger.transfer","payload":{"from":"visitor","to":"shop","amount":100}}`
	},
	// The same payment, but the first worker freezes after paying, past its 3 s deadline.
	"freeze": func() string {
		return `{"queue":"default","type":"demo.freeze","payload":{"from":"visitor","to":"shop","amount":100},"timeout_seconds":3,"max_attempts":3}`
	},
	// A service that fails half the time: retried with backoff until it works.
	"flaky": func() string {
		return `{"queue":"default","type":"chaos.fail","payload":{"p":0.5},"max_attempts":10}`
	},
	// Work booked for 20 seconds from now.
	"later": func() string {
		return fmt.Sprintf(`{"queue":"default","type":"chaos.sleep","payload":{"ms":300},"run_at":%q}`,
			time.Now().Add(20*time.Second).Format(time.RFC3339Nano))
	},
}

type cloudStat struct {
	Cloud   string `json:"cloud"`
	Workers int    `json:"workers"` // live now
	Running int    `json:"running"` // jobs running on them
}

type publicStats struct {
	Clouds []cloudStat `json:"clouds"`
	Epoch  int64       `json:"epoch"` // the leader's epoch: it goes up with every change of leader
	Leader bool        `json:"leader"`
	Day    queue.Day   `json:"day"`
}

// statsCache keeps the last stats for 5 s: every open live page polls them.
type statsCache struct {
	mu sync.Mutex
	at time.Time
	v  publicStats
}

func (a *api) publicRoutes(mux *http.ServeMux) {
	var cache statsCache
	mux.HandleFunc("GET /v1/public/stats", func(w http.ResponseWriter, r *http.Request) {
		cache.mu.Lock() // held through the query, so concurrent visitors share one
		defer cache.mu.Unlock()
		if time.Since(cache.at) > 5*time.Second {
			v, err := a.stats(r.Context())
			if err != nil {
				a.view(w, nil, err)
				return
			}
			cache.v, cache.at = v, time.Now()
		}
		writeJSON(w, http.StatusOK, cache.v)
	})
	mux.HandleFunc("POST /v1/public/try", a.try)
	mux.HandleFunc("GET /v1/public/try/{id}", a.tryGet)
}

func (a *api) stats(ctx context.Context) (publicStats, error) {
	workers, err1 := a.store.Workers(ctx)
	leader, err2 := a.store.Leader(ctx)
	day, err3 := a.store.Day(ctx)
	if err := errors.Join(err1, err2, err3); err != nil {
		return publicStats{}, err
	}
	clouds := []cloudStat{{Cloud: "aws"}, {Cloud: "azure"}} // both, even when one has no workers
	for _, w := range workers {
		for i := range clouds {
			if clouds[i].Cloud == w.Cloud && w.Live {
				clouds[i].Workers++
				clouds[i].Running += w.Running
			}
		}
	}
	return publicStats{Clouds: clouds, Epoch: leader.Epoch, Leader: leader.Held, Day: day}, nil
}

func (a *api) try(w http.ResponseWriter, r *http.Request) {
	var b struct {
		Scenario string `json:"scenario"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<10)).Decode(&b); err != nil || scenarios[b.Scenario] == nil {
		writeError(w, http.StatusBadRequest, `want {"scenario": "pay", "freeze", "flaky" or "later"}`)
		return
	}
	j, err := parseSubmit(guest, "try:"+uuid.NewV7().String(), []byte(scenarios[b.Scenario]()))
	if err != nil {
		a.log.Error("demo scenario", "scenario", b.Scenario, "err", err)
		writeError(w, http.StatusInternalServerError, "internal error")
		return
	}
	job, err := pgretry.Do(r.Context(), func(ctx context.Context) (queue.Job, error) {
		job, _, err := a.store.Submit(ctx, j)
		return job, err
	})
	if err != nil {
		a.log.Error("demo submit", "err", err)
		writeError(w, http.StatusServiceUnavailable, "could not start the demo; try again")
		return
	}
	writeJSON(w, http.StatusCreated, job)
}

func (a *api) tryGet(w http.ResponseWriter, r *http.Request) {
	id, err := uuid.Parse(r.PathValue("id"))
	if err != nil {
		writeError(w, http.StatusNotFound, queue.ErrNotFound.Error())
		return
	}
	job, err := pgretry.Do(r.Context(), func(ctx context.Context) (queue.Job, error) { return a.store.Get(ctx, guest, id) })
	if err != nil {
		a.respond(w, job, err)
		return
	}
	attempts, err := a.store.Attempts(r.Context(), guest, id)
	a.view(w, map[string]any{"job": job, "attempts": attempts}, err)
}
