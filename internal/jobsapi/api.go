// Package jobsapi is the public REST API for jobs:
//
//	POST /v1/jobs              submit (Idempotency-Key header required)
//	GET  /v1/jobs              list, newest first (?state=&queue=&limit=&after=)
//	GET  /v1/jobs/{id}         get
//	POST /v1/jobs/{id}/cancel  cancel
//	POST /v1/jobs/{id}/redrive retry a dead job with a fresh attempt budget
//	POST /v1/jobs/redrive      redrive every dead job, or one queue's ({"queue": "..."}), up to 1000 a call
//
// and the schedule routes in schedules.go, and the token-free ones for the live page in public.go.
package jobsapi

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"strconv"
	"strings"
	"time"
	"uuid"

	"github.com/aniruddha81/task-queue/internal/authn"
	"github.com/aniruddha81/task-queue/internal/mutant"
	"github.com/aniruddha81/task-queue/internal/pgretry"
	"github.com/aniruddha81/task-queue/internal/queue"
)

const maxBody = 64 << 10

var (
	states = map[string]bool{"available": true, "running": true, "succeeded": true, "dead": true, "cancelled": true}
)

type api struct {
	store *queue.Store
	auth  *authn.Verifier
	log   *slog.Logger
}

// New returns the API handler. Every route requires a valid token, except /v1/public/.
func New(store *queue.Store, auth *authn.Verifier, log *slog.Logger) http.Handler {
	a := &api{store, auth, log}
	mux := http.NewServeMux()
	mux.HandleFunc("POST /v1/jobs", a.authed(a.submit))
	mux.HandleFunc("GET /v1/jobs", a.authed(a.list))
	mux.HandleFunc("GET /v1/jobs/{id}", a.authed(a.get))
	mux.HandleFunc("POST /v1/jobs/{id}/cancel", a.authed(a.cancel))
	mux.HandleFunc("POST /v1/jobs/{id}/redrive", a.authed(a.redrive))
	mux.HandleFunc("POST /v1/jobs/redrive", a.authed(a.redriveAll))
	a.scheduleRoutes(mux)
	a.publicRoutes(mux)
	mux.HandleFunc("GET /v1/jobs/{id}/attempts", a.authed(a.attempts))
	mux.HandleFunc("GET /v1/queues", a.authed(a.queues))
	// Operational views: system-wide, read-only, for any signed-in user.
	mux.HandleFunc("GET /v1/workers", a.authed(func(w http.ResponseWriter, r *http.Request, _ uuid.UUID) {
		v, err := a.store.Workers(r.Context())
		a.view(w, map[string]any{"workers": v}, err)
	}))
	mux.HandleFunc("GET /v1/leader", a.authed(func(w http.ResponseWriter, r *http.Request, _ uuid.UUID) {
		v, err := a.store.Leader(r.Context())
		a.view(w, v, err)
	}))
	return mux
}

type handler func(w http.ResponseWriter, r *http.Request, owner uuid.UUID)

func (a *api) authed(h handler) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		id, err := a.auth.FromRequest(r)
		if err != nil {
			writeError(w, http.StatusUnauthorized, "invalid or missing token")
			return
		}
		h(w, r, id.Owner)
	}
}

func (a *api) submit(w http.ResponseWriter, r *http.Request, owner uuid.UUID) {
	raw, err := io.ReadAll(http.MaxBytesReader(w, r.Body, maxBody))
	if err != nil {
		writeError(w, http.StatusRequestEntityTooLarge, "body larger than 64 KB")
		return
	}
	j, err := parseSubmit(owner, r.Header.Get("Idempotency-Key"), raw)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	if mutant.AckFirst { // broken on purpose: acknowledge, then commit a second later
		id := uuid.NewV7()
		j.ID = &id
		go func() {
			time.Sleep(time.Second)
			a.store.Submit(context.Background(), j)
		}()
		writeJSON(w, http.StatusCreated, queue.Job{ID: id, State: "available"})
		return
	}
	type result struct {
		job     queue.Job
		created bool
	}
	res, err := pgretry.Do(r.Context(), func(ctx context.Context) (result, error) { // safe: the key makes it idempotent
		job, created, err := a.store.Submit(ctx, j)
		return result{job, created}, err
	})
	job, created := res.job, res.created
	switch {
	case errors.Is(err, queue.ErrKeyReused):
		writeError(w, http.StatusConflict, err.Error())
	case err != nil:
		// The commit may or may not have happened; a retry with the same key is always safe.
		a.log.Error("submit", "err", err)
		writeError(w, http.StatusServiceUnavailable, "outcome unknown: retry with the same Idempotency-Key")
	case created:
		writeJSON(w, http.StatusCreated, job)
	default:
		writeJSON(w, http.StatusOK, job)
	}
}

// parseSubmit validates a submission: the body via queue.ParseJob, plus the key.
func parseSubmit(owner uuid.UUID, key string, raw []byte) (queue.NewJob, error) {
	j, err := queue.ParseJob(raw)
	switch {
	case err != nil:
		return queue.NewJob{}, err
	case key == "" || len(key) > 200:
		return queue.NewJob{}, errors.New("Idempotency-Key header is required (1-200 characters)")
	case strings.HasPrefix(key, "cron:"):
		return queue.NewJob{}, errors.New(`Idempotency-Key may not start with "cron:"`)
	}
	j.Owner, j.Key = owner, key
	return j, nil
}

func (a *api) get(w http.ResponseWriter, r *http.Request, owner uuid.UUID) {
	id, err := uuid.Parse(r.PathValue("id"))
	if err != nil {
		writeError(w, http.StatusNotFound, queue.ErrNotFound.Error())
		return
	}
	job, err := pgretry.Do(r.Context(), func(ctx context.Context) (queue.Job, error) { return a.store.Get(ctx, owner, id) })
	a.respond(w, job, err)
}

func (a *api) cancel(w http.ResponseWriter, r *http.Request, owner uuid.UUID) {
	id, err := uuid.Parse(r.PathValue("id"))
	if err != nil {
		writeError(w, http.StatusNotFound, queue.ErrNotFound.Error())
		return
	}
	job, err := pgretry.Do(r.Context(), func(ctx context.Context) (queue.Job, error) { return a.store.Cancel(ctx, owner, id) })
	a.respond(w, job, err)
}

func (a *api) redrive(w http.ResponseWriter, r *http.Request, owner uuid.UUID) {
	id, err := uuid.Parse(r.PathValue("id"))
	if err != nil {
		writeError(w, http.StatusNotFound, queue.ErrNotFound.Error())
		return
	}
	job, err := pgretry.Do(r.Context(), func(ctx context.Context) (queue.Job, error) { return a.store.Redrive(ctx, owner, id) })
	a.respond(w, job, err)
}

func (a *api) redriveAll(w http.ResponseWriter, r *http.Request, owner uuid.UUID) {
	var b struct {
		Queue string `json:"queue"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 4<<10)).Decode(&b); err != nil && !errors.Is(err, io.EOF) {
		writeError(w, http.StatusBadRequest, "invalid JSON body")
		return
	}
	if b.Queue != "" && !queue.ValidName(b.Queue) {
		writeError(w, http.StatusBadRequest, "unknown queue name")
		return
	}
	n, err := pgretry.Do(r.Context(), func(ctx context.Context) (int, error) { return a.store.RedriveAll(ctx, owner, b.Queue, 1000) })
	if err != nil {
		a.log.Error("redrive all", "err", err)
		writeError(w, http.StatusInternalServerError, "internal error")
		return
	}
	writeJSON(w, http.StatusOK, map[string]int{"redriven": n})
}

func (a *api) attempts(w http.ResponseWriter, r *http.Request, owner uuid.UUID) {
	id, err := uuid.Parse(r.PathValue("id"))
	if err != nil {
		writeError(w, http.StatusNotFound, queue.ErrNotFound.Error())
		return
	}
	v, err := a.store.Attempts(r.Context(), owner, id)
	if errors.Is(err, queue.ErrNotFound) {
		writeError(w, http.StatusNotFound, err.Error())
		return
	}
	a.view(w, map[string]any{"attempts": v}, err)
}

func (a *api) queues(w http.ResponseWriter, r *http.Request, owner uuid.UUID) {
	v, err := a.store.QueueCounts(r.Context(), owner)
	a.view(w, map[string]any{"queues": v}, err)
}

// view writes a read-only result, or a 500.
func (a *api) view(w http.ResponseWriter, v any, err error) {
	if err != nil {
		a.log.Error("view", "err", err)
		writeError(w, http.StatusInternalServerError, "internal error")
		return
	}
	writeJSON(w, http.StatusOK, v)
}

func (a *api) respond(w http.ResponseWriter, job queue.Job, err error) {
	switch {
	case errors.Is(err, queue.ErrNotFound):
		writeError(w, http.StatusNotFound, err.Error())
	case errors.Is(err, queue.ErrNotCancellable):
		writeError(w, http.StatusConflict, "job already "+job.State)
	case errors.Is(err, queue.ErrNotDead):
		writeError(w, http.StatusConflict, err.Error()+"; this job is "+job.State)
	case err != nil:
		a.log.Error("query", "err", err)
		writeError(w, http.StatusInternalServerError, "internal error")
	default:
		writeJSON(w, http.StatusOK, job)
	}
}

func (a *api) list(w http.ResponseWriter, r *http.Request, owner uuid.UUID) {
	q := r.URL.Query()
	f := queue.Filter{Limit: 50}
	if s := q.Get("state"); s != "" {
		if !states[s] {
			writeError(w, http.StatusBadRequest, "unknown state")
			return
		}
		f.State = &s
	}
	if s := q.Get("queue"); s != "" {
		f.Queue = &s
	}
	if s := q.Get("after"); s != "" {
		id, err := uuid.Parse(s)
		if err != nil {
			writeError(w, http.StatusBadRequest, "after must be a job ID")
			return
		}
		f.After = &id
	}
	if s := q.Get("limit"); s != "" {
		n, err := strconv.Atoi(s)
		if err != nil || n < 1 || n > 200 {
			writeError(w, http.StatusBadRequest, "limit must be between 1 and 200")
			return
		}
		f.Limit = n
	}
	jobs, err := pgretry.Do(r.Context(), func(ctx context.Context) ([]queue.Job, error) { return a.store.List(ctx, owner, f) })
	if err != nil {
		a.log.Error("list", "err", err)
		writeError(w, http.StatusInternalServerError, "internal error")
		return
	}
	var next *uuid.UUID // a full page may have more after it
	if len(jobs) == f.Limit {
		next = &jobs[len(jobs)-1].ID
	}
	writeJSON(w, http.StatusOK, map[string]any{"jobs": jobs, "next": next})
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(v)
}

func writeError(w http.ResponseWriter, status int, msg string) {
	writeJSON(w, status, map[string]string{"error": msg})
}
