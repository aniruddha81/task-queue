package jobsapi

import (
	"crypto/ed25519"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
	"uuid"

	"github.com/aniruddha81/task-queue/internal/authn"
	"github.com/aniruddha81/task-queue/internal/pgtest"
	"github.com/aniruddha81/task-queue/internal/queue"
)

func TestParseSubmit(t *testing.T) {
	owner := uuid.NewV7()
	tests := []struct {
		name, key, body, wantErr string
	}{
		{"minimal, defaults applied", "k", `{"queue":"q","type":"t"}`, ""},
		{"missing key", "", `{"queue":"q","type":"t"}`, "Idempotency-Key header is required"},
		{"reserved cron: key", "cron:x", `{"queue":"q","type":"t"}`, `may not start with "cron:"`},
		{"bad queue name", "k", `{"queue":"Q!","type":"t"}`, "queue must match"},
		{"missing type", "k", `{"queue":"q"}`, "type must match"},
		{"unknown field", "k", `{"queue":"q","type":"t","prio":1}`, "unknown field"},
		{"not JSON", "k", `queue=q`, "invalid JSON"},
		{"max_attempts 0", "k", `{"queue":"q","type":"t","max_attempts":0}`, "max_attempts"},
		{"timeout too long", "k", `{"queue":"q","type":"t","timeout_seconds":3601}`, "timeout_seconds"},
		{"unknown affinity", "k", `{"queue":"q","type":"t","affinity":"gcp"}`, "affinity"},
		{"priority out of range", "k", `{"queue":"q","type":"t","priority":5000}`, "priority"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			j, err := parseSubmit(owner, tt.key, []byte(tt.body))
			if tt.wantErr == "" {
				if err != nil {
					t.Fatal(err)
				}
				if j.MaxAttempts != 5 || j.TimeoutSeconds != 60 || string(j.Payload) != "{}" {
					t.Errorf("defaults not applied: %+v", j)
				}
				return
			}
			if err == nil || !strings.Contains(err.Error(), tt.wantErr) {
				t.Errorf("err = %v, want it to contain %q", err, tt.wantErr)
			}
		})
	}
}

// TestHTTP drives the real handler against a real database.
func TestHTTP(t *testing.T) {
	pub, key, _ := ed25519.GenerateKey(nil)
	srv := httptest.NewServer(newAPI(t, pub))
	defer srv.Close()

	alice, bob := uuid.NewV7(), uuid.NewV7()
	aliceTok, _ := authn.Sign(key, authn.Identity{Owner: alice}, time.Hour)
	bobTok, _ := authn.Sign(key, authn.Identity{Owner: bob}, time.Hour)

	t.Run("bad tokens get 401", func(t *testing.T) { // the full list is in package authn
		_, otherKey, _ := ed25519.GenerateKey(nil)
		wrongKey, _ := authn.Sign(otherKey, authn.Identity{Owner: alice}, time.Hour)
		for name, tok := range map[string]string{"missing": "", "wrong key": wrongKey} {
			if code, _ := do(t, srv, "GET", "/v1/jobs", tok, "", ""); code != http.StatusUnauthorized {
				t.Errorf("%s token: status %d, want 401", name, code)
			}
		}
	})

	body := `{"queue":"default","type":"email.send","payload":{"to":"a@example.com"}}`
	var job queue.Job

	t.Run("submit, repeat, conflict", func(t *testing.T) {
		code, resp := do(t, srv, "POST", "/v1/jobs", aliceTok, "order-1", body)
		if code != http.StatusCreated {
			t.Fatalf("first submit: %d %s", code, resp)
		}
		json.Unmarshal(resp, &job)
		if job.State != "available" {
			t.Errorf("state = %s, want available", job.State)
		}

		code, resp = do(t, srv, "POST", "/v1/jobs", aliceTok, "order-1", body)
		var again queue.Job
		json.Unmarshal(resp, &again)
		if code != http.StatusOK || again.ID != job.ID {
			t.Errorf("repeat: %d id=%v, want 200 and the same job", code, again.ID)
		}

		changed := strings.Replace(body, "a@example.com", "b@example.com", 1)
		if code, _ := do(t, srv, "POST", "/v1/jobs", aliceTok, "order-1", changed); code != http.StatusConflict {
			t.Errorf("changed body: %d, want 409", code)
		}
		if code, _ := do(t, srv, "POST", "/v1/jobs", aliceTok, "", body); code != http.StatusBadRequest {
			t.Errorf("no key: %d, want 400", code)
		}
	})

	t.Run("another user gets 404", func(t *testing.T) {
		for _, req := range [][2]string{{"GET", "/v1/jobs/" + job.ID.String()}, {"POST", "/v1/jobs/" + job.ID.String() + "/cancel"}} {
			if code, _ := do(t, srv, req[0], req[1], bobTok, "", ""); code != http.StatusNotFound {
				t.Errorf("bob %s %s: %d, want 404", req[0], req[1], code)
			}
		}
		if code, _ := do(t, srv, "GET", "/v1/jobs/"+job.ID.String(), aliceTok, "", ""); code != http.StatusOK {
			t.Errorf("alice GET: %d, want 200", code)
		}
	})

	t.Run("list and cancel", func(t *testing.T) {
		code, resp := do(t, srv, "GET", "/v1/jobs?state=available", aliceTok, "", "")
		var page struct {
			Jobs []queue.Job `json:"jobs"`
		}
		json.Unmarshal(resp, &page)
		if code != http.StatusOK || len(page.Jobs) != 1 {
			t.Errorf("list: %d with %d jobs, want 200 and 1", code, len(page.Jobs))
		}
		for range 2 { // cancelling twice is fine
			code, resp = do(t, srv, "POST", "/v1/jobs/"+job.ID.String()+"/cancel", aliceTok, "", "")
			var got queue.Job
			json.Unmarshal(resp, &got)
			if code != http.StatusOK || got.State != "cancelled" {
				t.Errorf("cancel: %d state=%s, want 200 cancelled", code, got.State)
			}
		}
	})
}

func newAPI(t *testing.T, pub ed25519.PublicKey) http.Handler {
	return New(queue.NewStore(pgtest.Migrated(t, "jobs")), authn.NewVerifier(pub), slog.New(slog.DiscardHandler))
}

func do(t *testing.T, srv *httptest.Server, method, path, token, key, body string) (int, []byte) {
	t.Helper()
	req, _ := http.NewRequest(method, srv.URL+path, strings.NewReader(body))
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	if key != "" {
		req.Header.Set("Idempotency-Key", key)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	b, _ := io.ReadAll(resp.Body)
	return resp.StatusCode, b
}

func TestSchedulesHTTP(t *testing.T) {
	pub, key, _ := ed25519.GenerateKey(nil)
	srv := httptest.NewServer(newAPI(t, pub))
	defer srv.Close()
	alice, bob := uuid.NewV7(), uuid.NewV7()
	at, _ := authn.Sign(key, authn.Identity{Owner: alice}, time.Hour)
	bt, _ := authn.Sign(key, authn.Identity{Owner: bob}, time.Hour)

	body := `{"name":"daily-report","cron":"0 9 * * *","timezone":"Asia/Kolkata","job":{"queue":"reports","type":"report.daily"}}`
	code, resp := do(t, srv, "POST", "/v1/schedules", at, "", body)
	if code != http.StatusCreated {
		t.Fatalf("create: %d %s", code, resp)
	}
	var sc queue.Schedule
	json.Unmarshal(resp, &sc)
	if !sc.NextTickAt.After(time.Now()) || sc.NextTickAt.In(time.UTC).Format("15:04") != "03:30" {
		t.Errorf("first tick %v, want the next 09:00 IST (03:30 UTC)", sc.NextTickAt)
	}

	for name, b := range map[string]string{
		"duplicate name":  body,
		"bad cron":        `{"name":"x","cron":"every day","timezone":"UTC","job":{"queue":"q","type":"t"}}`,
		"bad timezone":    `{"name":"x","cron":"* * * * *","timezone":"Mars/Base","job":{"queue":"q","type":"t"}}`,
		"bad job":         `{"name":"x","cron":"* * * * *","timezone":"UTC","job":{"queue":"Q!","type":"t"}}`,
		"job with run_at": `{"name":"x","cron":"* * * * *","timezone":"UTC","job":{"queue":"q","type":"t","run_at":"2030-01-01T00:00:00Z"}}`,
	} {
		want := http.StatusBadRequest
		if name == "duplicate name" {
			want = http.StatusConflict
		}
		if code, _ := do(t, srv, "POST", "/v1/schedules", at, "", b); code != want {
			t.Errorf("%s: %d, want %d", name, code, want)
		}
	}

	path := "/v1/schedules/" + sc.ID.String()
	if code, _ := do(t, srv, "GET", path, bt, "", ""); code != http.StatusNotFound {
		t.Errorf("bob GET: %d, want 404", code)
	}
	code, resp = do(t, srv, "POST", path+"/pause", at, "", "")
	json.Unmarshal(resp, &sc)
	if code != http.StatusOK || !sc.Paused {
		t.Errorf("pause: %d paused=%v", code, sc.Paused)
	}
	code, resp = do(t, srv, "POST", path+"/resume", at, "", "")
	json.Unmarshal(resp, &sc)
	if code != http.StatusOK || sc.Paused || !sc.NextTickAt.After(time.Now()) {
		t.Errorf("resume: %d paused=%v next=%v", code, sc.Paused, sc.NextTickAt)
	}
	if code, _ := do(t, srv, "DELETE", path, at, "", ""); code != http.StatusNoContent {
		t.Errorf("delete: %d, want 204", code)
	}
	if code, _ := do(t, srv, "GET", path, at, "", ""); code != http.StatusNotFound {
		t.Errorf("GET after delete: %d, want 404", code)
	}
}

// TestPublicHTTP: the live page's routes work without a token, run only the fixed scenarios,
// and show only demo jobs.
func TestPublicHTTP(t *testing.T) {
	pub, key, _ := ed25519.GenerateKey(nil)
	srv := httptest.NewServer(newAPI(t, pub))
	defer srv.Close()

	code, resp := do(t, srv, "POST", "/v1/public/try", "", "", `{"scenario":"freeze"}`)
	var job queue.Job
	json.Unmarshal(resp, &job)
	if code != http.StatusCreated || job.Type != "demo.freeze" || job.TimeoutSeconds != 3 {
		t.Fatalf("try freeze: %d %s", code, resp)
	}
	for _, body := range []string{`{"scenario":"rm -rf"}`, `{"type":"chaos.crash"}`, `not json`} {
		if code, _ := do(t, srv, "POST", "/v1/public/try", "", "", body); code != http.StatusBadRequest {
			t.Errorf("try %s: %d, want 400", body, code)
		}
	}
	if code, resp := do(t, srv, "GET", "/v1/public/try/"+job.ID.String(), "", "", ""); code != http.StatusOK ||
		!strings.Contains(string(resp), `"attempts":[]`) {
		t.Errorf("get demo job: %d %s", code, resp)
	}
	if code, resp := do(t, srv, "GET", "/v1/public/stats", "", "", ""); code != http.StatusOK ||
		!strings.Contains(string(resp), `"cloud":"azure"`) {
		t.Errorf("stats: %d %s", code, resp)
	}

	// A user's own job is not a demo job.
	tok, _ := authn.Sign(key, authn.Identity{Owner: uuid.NewV7()}, time.Hour)
	_, resp = do(t, srv, "POST", "/v1/jobs", tok, "mine", `{"queue":"default","type":"chaos.sleep"}`)
	json.Unmarshal(resp, &job)
	if code, _ := do(t, srv, "GET", "/v1/public/try/"+job.ID.String(), "", "", ""); code != http.StatusNotFound {
		t.Errorf("a user's job through the public route: %d, want 404", code)
	}
}

func TestScenariosAreValidJobs(t *testing.T) {
	for name, body := range scenarios {
		if _, err := parseSubmit(guest, "try:x", []byte(body())); err != nil {
			t.Errorf("scenario %s: %v", name, err)
		}
	}
}

func TestParseReminder(t *testing.T) {
	now := time.Date(2026, 10, 22, 10, 0, 0, 0, time.UTC)
	at := func(d time.Duration) string { return now.Add(d).Format(time.RFC3339) }
	for body, wantErr := range map[string]string{
		`{"email":"A@Example.com","label":" call mum ","at":"` + at(time.Hour) + `","tz":"Asia/Kolkata"}`:  "",
		`{"email":"a@example.com","label":"x","at":"` + at(time.Hour) + `","tz":"Mars/Base"}`:              "", // UTC
		`{"email":"Bob <a@example.com>","label":"x","at":"` + at(time.Hour) + `"}`:                         "email address",
		`{"email":"a@example.com\r\nBcc: b@example.com","label":"x","at":"` + at(time.Hour) + `"}`:         "email address",
		`{"email":"nope","label":"x","at":"` + at(time.Hour) + `"}`:                                        "email address",
		`{"email":"a@example.com","label":"","at":"` + at(time.Hour) + `"}`:                                "1 to 80",
		`{"email":"a@example.com","label":"a\nb","at":"` + at(time.Hour) + `"}`:                            "1 to 80",
		`{"email":"a@example.com","label":"` + strings.Repeat("x", 81) + `","at":"` + at(time.Hour) + `"}`: "1 to 80",
		`{"email":"a@example.com","label":"x","at":"` + at(30*time.Second) + `"}`:                          "between a minute and a week",
		`{"email":"a@example.com","label":"x","at":"` + at(8*24*time.Hour) + `"}`:                          "between a minute and a week",
		`{"email":"a@example.com","label":"x","at":"` + at(time.Hour) + `","body":"spam"}`:                 "want {",
	} {
		r, err := parseReminder([]byte(body), now)
		if wantErr == "" {
			if err != nil || r.Email != strings.ToLower(r.Email) || r.Label != strings.TrimSpace(r.Label) || (r.TZ != "Asia/Kolkata" && r.TZ != "UTC") {
				t.Errorf("%s: %+v, %v", body, r, err)
			}
			continue
		}
		if err == nil || !strings.Contains(err.Error(), wantErr) {
			t.Errorf("%s: err %v, want it to contain %q", body, err, wantErr)
		}
	}
}

// TestRemindHTTP: booking emails a link and nothing else; the link's token confirms the reminder
// into one job at its time, however often it is used; and the public view hides the address.
func TestRemindHTTP(t *testing.T) {
	pub, _, _ := ed25519.GenerateKey(nil)
	pool := pgtest.Migrated(t, "jobs")
	srv := httptest.NewServer(New(queue.NewStore(pool), authn.NewVerifier(pub), slog.New(slog.DiscardHandler)))
	defer srv.Close()
	post := func(path, origin, body string) (int, []byte) {
		req, _ := http.NewRequest("POST", srv.URL+path, strings.NewReader(body))
		req.Header.Set("X-Public-Origin", origin)
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		defer resp.Body.Close()
		b, _ := io.ReadAll(resp.Body)
		return resp.StatusCode, b
	}
	at := time.Now().Add(time.Hour).UTC().Truncate(time.Second)
	booking := `{"email":"you@example.com","label":"water the plants","at":"` + at.Format(time.RFC3339) + `","tz":"Asia/Kolkata"}`
	if code, _ := post("/v1/public/remind", "", booking); code != http.StatusInternalServerError {
		t.Errorf("without the gateway's origin: %d, want 500", code)
	}
	code, resp := post("/v1/public/remind", "https://tq.example", booking)
	if code != http.StatusCreated || strings.Contains(string(resp), "/live/reminder/") {
		t.Fatalf("book: %d %s (the token must not be in the response)", code, resp)
	}

	var body string
	pool.QueryRow(t.Context(), `SELECT payload->>'body' FROM jobs WHERE idempotency_key LIKE 'confirm:%'`).Scan(&body)
	_, link, ok := strings.Cut(body, "https://tq.example/live/reminder/?t=")
	if !ok || strings.Contains(body, "water the plants") {
		t.Fatalf("confirmation email %q: want a link, and none of the visitor's text", body)
	}
	token := strings.Fields(link)[0]

	var first queue.Job
	for i := range 2 {
		code, resp := post("/v1/public/remind/"+token+"/confirm", "https://tq.example", "")
		var job queue.Job
		json.Unmarshal(resp, &job)
		if i == 0 {
			first = job
			if code != http.StatusCreated || job.Type != "email.send" || !job.RunAt.Equal(at) || strings.Contains(string(resp), "you@example.com") {
				t.Fatalf("confirm: %d %s, want an email.send job at %v, without the address", code, resp, at)
			}
		} else if code != http.StatusNotFound {
			t.Errorf("confirming again: %d %s, want 404 (the link is used)", code, resp)
		}
	}
	if code, _ := post("/v1/public/remind/"+uuid.NewV4().String()+"/confirm", "https://tq.example", ""); code != http.StatusNotFound {
		t.Errorf("an unknown token: %d, want 404", code)
	}
	code, resp = do(t, srv, "GET", "/v1/public/try/"+first.ID.String(), "", "", "")
	if code != http.StatusOK || strings.Contains(string(resp), "you@example.com") {
		t.Errorf("public view of the reminder: %d %s, want it without the address", code, resp)
	}
}
