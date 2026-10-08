package jobsapi

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"uuid"

	"github.com/aniruddha81/task-queue/internal/queue"
)

// POST   /v1/schedules              create {name, cron, timezone, job}
// GET    /v1/schedules              list
// GET    /v1/schedules/{id}         get
// POST   /v1/schedules/{id}/pause   pause (ticks while paused never fire)
// POST   /v1/schedules/{id}/resume  resume from the next tick after now
// DELETE /v1/schedules/{id}         delete (jobs already created stay)
func (a *api) scheduleRoutes(mux *http.ServeMux) {
	mux.HandleFunc("POST /v1/schedules", a.authed(a.createSchedule))
	mux.HandleFunc("GET /v1/schedules", a.authed(a.listSchedules))
	mux.HandleFunc("GET /v1/schedules/{id}", a.authed(a.withSchedule(a.store.GetSchedule)))
	mux.HandleFunc("POST /v1/schedules/{id}/pause", a.authed(a.withSchedule(func(ctx context.Context, o, id uuid.UUID) (queue.Schedule, error) {
		return a.store.SetPaused(ctx, o, id, true)
	})))
	mux.HandleFunc("POST /v1/schedules/{id}/resume", a.authed(a.withSchedule(func(ctx context.Context, o, id uuid.UUID) (queue.Schedule, error) {
		return a.store.SetPaused(ctx, o, id, false)
	})))
	mux.HandleFunc("DELETE /v1/schedules/{id}", a.authed(a.deleteSchedule))
}

func (a *api) createSchedule(w http.ResponseWriter, r *http.Request, owner uuid.UUID) {
	raw, err := io.ReadAll(http.MaxBytesReader(w, r.Body, maxBody))
	if err != nil {
		writeError(w, http.StatusRequestEntityTooLarge, "body larger than 64 KB")
		return
	}
	var b struct {
		Name     string          `json:"name"`
		Cron     string          `json:"cron"`
		Timezone string          `json:"timezone"`
		Job      json.RawMessage `json:"job"`
	}
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&b); err != nil {
		writeError(w, http.StatusBadRequest, "invalid JSON body: "+err.Error())
		return
	}
	if !queue.ValidName(b.Name) {
		writeError(w, http.StatusBadRequest, "name must match ^[a-z0-9._-]{1,64}$")
		return
	}
	c, err := queue.ParseCron(b.Cron, b.Timezone)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	j, err := queue.ParseJob(b.Job)
	switch {
	case err != nil:
		writeError(w, http.StatusBadRequest, "job: "+err.Error())
		return
	case j.RunAt != nil:
		writeError(w, http.StatusBadRequest, "job: run_at is set by the schedule")
		return
	}
	sc, err := a.store.CreateSchedule(r.Context(), owner, b.Name, c, b.Cron, b.Timezone, b.Job)
	switch {
	case errors.Is(err, queue.ErrNameTaken):
		writeError(w, http.StatusConflict, err.Error())
	case err != nil:
		a.log.Error("create schedule", "err", err)
		writeError(w, http.StatusInternalServerError, "internal error")
	default:
		writeJSON(w, http.StatusCreated, sc)
	}
}

func (a *api) listSchedules(w http.ResponseWriter, r *http.Request, owner uuid.UUID) {
	scs, err := a.store.ListSchedules(r.Context(), owner)
	if err != nil {
		a.log.Error("list schedules", "err", err)
		writeError(w, http.StatusInternalServerError, "internal error")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"schedules": scs})
}

func (a *api) deleteSchedule(w http.ResponseWriter, r *http.Request, owner uuid.UUID) {
	id, err := uuid.Parse(r.PathValue("id"))
	if err == nil {
		err = a.store.DeleteSchedule(r.Context(), owner, id)
	} else {
		err = queue.ErrNotFound
	}
	switch {
	case errors.Is(err, queue.ErrNotFound):
		writeError(w, http.StatusNotFound, "schedule not found")
	case err != nil:
		a.log.Error("delete schedule", "err", err)
		writeError(w, http.StatusInternalServerError, "internal error")
	default:
		w.WriteHeader(http.StatusNoContent)
	}
}

func (a *api) withSchedule(op func(context.Context, uuid.UUID, uuid.UUID) (queue.Schedule, error)) handler {
	return func(w http.ResponseWriter, r *http.Request, owner uuid.UUID) {
		id, err := uuid.Parse(r.PathValue("id"))
		var sc queue.Schedule
		if err == nil {
			sc, err = op(r.Context(), owner, id)
		} else {
			err = queue.ErrNotFound
		}
		switch {
		case errors.Is(err, queue.ErrNotFound):
			writeError(w, http.StatusNotFound, "schedule not found")
		case err != nil:
			a.log.Error("schedule", "err", err)
			writeError(w, http.StatusInternalServerError, "internal error")
		default:
			writeJSON(w, http.StatusOK, sc)
		}
	}
}
