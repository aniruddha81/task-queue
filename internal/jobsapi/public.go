package jobsapi

// The public routes behind the live page (web/app/live), for visitors without an account.
// They need no token; the gateway rate-limits POST /v1/public/try per address and overall.
//
//	GET  /v1/public/stats     workers per cloud, the leader's epoch, and the last 24 hours
//	POST /v1/public/try       {"scenario": "pay" | "freeze" | "flaky" | "later"} runs one demo job
//	GET  /v1/public/try/{id}  a demo job and its attempts
//	POST /v1/public/remind    {email, label, at, tz} books a reminder and emails a confirmation link
//	POST /v1/public/remind/{token}/confirm  turns it into an email.send job at its time
//
// Demo jobs belong to one guest owner and are fixed by the scenario: a visitor picks which
// one, never its type or payload. A reminder emails only its confirmation link, with no text
// from the visitor in it, until someone with the link confirms: so the page can't be used to
// send anything else to someone else's address.

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/mail"
	"strings"
	"sync"
	"time"
	"unicode"
	"unicode/utf8"
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
	mux.HandleFunc("POST /v1/public/remind", a.remind)
	mux.HandleFunc("POST /v1/public/remind/{token}/confirm", a.confirmReminder)
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
	// Anyone with the ID may look, so nothing that names a person: a reminder's payload, and
	// its errors, hold its email address.
	job.Payload = json.RawMessage(`{}`)
	if job.Type == "email.send" {
		job.LastError = nil
		for i := range attempts {
			attempts[i].Error = nil
		}
	}
	a.view(w, map[string]any{"job": job, "attempts": attempts}, err)
}

// parseReminder checks a booking: one plain address, a one-line label, and a time from a
// minute to a week after now. An unknown time zone becomes UTC.
func parseReminder(raw []byte, now time.Time) (queue.Reminder, error) {
	var b struct {
		Email string    `json:"email"`
		Label string    `json:"label"`
		At    time.Time `json:"at"`
		TZ    string    `json:"tz"`
	}
	d := json.NewDecoder(bytes.NewReader(raw))
	d.DisallowUnknownFields()
	if err := d.Decode(&b); err != nil {
		return queue.Reminder{}, errors.New(`want {"email", "label", "at", "tz"}`)
	}
	b.Label = strings.TrimSpace(b.Label)
	addr, err := mail.ParseAddress(b.Email)
	switch {
	case err != nil || addr.Name != "" || len(b.Email) > 254 || strings.ContainsAny(b.Email, "\r\n"):
		return queue.Reminder{}, errors.New("that doesn't look like an email address")
	case b.Label == "" || utf8.RuneCountInString(b.Label) > 80 || strings.ContainsFunc(b.Label, unicode.IsControl):
		return queue.Reminder{}, errors.New("the reminder must be 1 to 80 characters, on one line")
	case b.At.Before(now.Add(time.Minute)) || b.At.After(now.Add(7*24*time.Hour)):
		return queue.Reminder{}, errors.New("pick a time between a minute and a week from now")
	}
	if _, err := time.LoadLocation(b.TZ); err != nil || b.TZ == "" || b.TZ == "Local" {
		b.TZ = "UTC"
	}
	return queue.Reminder{Email: strings.ToLower(addr.Address), Label: b.Label, RemindAt: b.At, TZ: b.TZ}, nil
}

// emailJob is the body of an email.send job that runs at at.
func emailJob(to, subject, body string, at time.Time) []byte {
	b, _ := json.Marshal(map[string]any{"queue": "default", "type": "email.send", "run_at": at,
		"payload": map[string]string{"to": to, "subject": subject, "body": body}})
	return b
}

// when is a reminder's time as its visitor reads it.
func when(r queue.Reminder) string {
	loc, _ := time.LoadLocation(r.TZ) // checked by parseReminder
	return r.RemindAt.In(loc).Format("Mon 2 Jan 2006 at 15:04 MST")
}

// remind books a reminder and emails its confirmation link. The token goes only into that
// email, never into the response: opening the link proves the address is the visitor's.
func (a *api) remind(w http.ResponseWriter, r *http.Request) {
	raw, err := io.ReadAll(http.MaxBytesReader(w, r.Body, 2<<10))
	if err != nil {
		writeError(w, http.StatusRequestEntityTooLarge, "body too large")
		return
	}
	rem, err := parseReminder(raw, time.Now())
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	origin := r.Header.Get("X-Public-Origin") // set by the gateway, from its own configuration
	if origin == "" {
		a.log.Error("remind: no X-Public-Origin from the gateway")
		writeError(w, http.StatusInternalServerError, "internal error")
		return
	}
	token, err := a.store.BookReminder(r.Context(), rem) // not retried: a retry could book it twice
	switch {
	case errors.Is(err, queue.ErrTooManyReminders):
		writeError(w, http.StatusTooManyRequests, "too many reminders are waiting for confirmation; confirm one, or try again tomorrow")
		return
	case err != nil:
		a.log.Error("book reminder", "err", err)
		writeError(w, http.StatusServiceUnavailable, "could not book the reminder; try again")
		return
	}
	body := fmt.Sprintf("Someone, hopefully you, asked %s to email you a reminder on %s.\n\n"+
		"To book it, open this link and press Confirm:\n\n%s/live/reminder/?t=%s\n\n"+
		"If it wasn't you, ignore this email: nothing is booked unless you confirm, and the link stops working in a day.\n",
		strings.TrimPrefix(origin, "https://"), when(rem), origin, token)
	j, err := parseSubmit(guest, "confirm:"+token.String(), emailJob(rem.Email, "Confirm your reminder", body, time.Now()))
	if err == nil {
		_, err = pgretry.Do(r.Context(), func(ctx context.Context) (queue.Job, error) { // safe: the key
			job, _, err := a.store.Submit(ctx, j)
			return job, err
		})
	}
	if err != nil {
		a.log.Error("confirmation email", "err", err)
		writeError(w, http.StatusServiceUnavailable, "could not send the confirmation email; try again")
		return
	}
	writeJSON(w, http.StatusCreated, map[string]string{"email": rem.Email})
}

// confirmReminder submits a booked reminder as an email.send job at its time. Its key and ID
// come from the booking, so confirming again returns the same job.
func (a *api) confirmReminder(w http.ResponseWriter, r *http.Request) {
	const gone = "this link was already used, or it expired"
	token, err := uuid.Parse(r.PathValue("token"))
	if err != nil {
		writeError(w, http.StatusNotFound, gone)
		return
	}
	origin := r.Header.Get("X-Public-Origin")
	rem, err := pgretry.Do(r.Context(), func(ctx context.Context) (queue.Reminder, error) { return a.store.WaitingReminder(ctx, token) })
	switch {
	case errors.Is(err, queue.ErrNotFound):
		writeError(w, http.StatusNotFound, gone)
		return
	case err != nil || origin == "":
		a.log.Error("confirm reminder", "err", err, "origin", origin)
		writeError(w, http.StatusServiceUnavailable, "could not confirm; try again")
		return
	}
	body := fmt.Sprintf("Your reminder:\n\n    %s\n\n"+
		"You booked it for %s. Until now it waited as a job in a database copied across AWS Mumbai and "+
		"Azure Central India, and then a worker in one of those clouds sent this email.\n\n"+
		"See how it got here: %s/live/reminder/?job=%s\n\n"+
		"Email can't refuse a duplicate, so if a server crashed right after sending, you might get this twice.\n",
		rem.Label, when(rem), origin, rem.JobID)
	j, err := parseSubmit(guest, "remind:"+token.String(), emailJob(rem.Email, "Reminder: "+rem.Label, body, rem.RemindAt))
	if err != nil {
		a.log.Error("reminder job", "err", err)
		writeError(w, http.StatusInternalServerError, "internal error")
		return
	}
	j.ID = &rem.JobID
	job, err := pgretry.Do(r.Context(), func(ctx context.Context) (queue.Job, error) {
		job, _, err := a.store.Submit(ctx, j)
		if errors.Is(err, queue.ErrKeyReused) { // confirmed before, through the site's other name
			return a.store.Get(ctx, guest, rem.JobID)
		}
		return job, err
	})
	if err != nil {
		a.log.Error("submit reminder", "err", err)
		writeError(w, http.StatusServiceUnavailable, "could not confirm; try again")
		return
	}
	// Best effort: a row left behind expires in a day, and confirming it again finds this job.
	if err := a.store.ForgetReminder(r.Context(), token); err != nil {
		a.log.Warn("forget reminder", "err", err)
	}
	job.Payload = json.RawMessage(`{}`) // the address, as in tryGet
	writeJSON(w, http.StatusCreated, job)
}
