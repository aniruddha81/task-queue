package jobsapi

import (
	"crypto/ed25519"
	"database/sql"
	"encoding/base64"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
	"uuid"

	"github.com/golang-jwt/jwt/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	_ "github.com/jackc/pgx/v5/stdlib"

	"github.com/aniruddha81/task-queue/internal/authn"
	"github.com/aniruddha81/task-queue/internal/pgtest"
	"github.com/aniruddha81/task-queue/internal/queue"
	"github.com/aniruddha81/task-queue/migrations"
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
	key := authn.DevKey()
	pub := key.Public().(ed25519.PublicKey)
	srv := httptest.NewServer(newAPI(t, pub))
	defer srv.Close()

	alice, bob := uuid.NewV7(), uuid.NewV7()
	aliceTok, _ := authn.Sign(key, alice, time.Hour)
	bobTok, _ := authn.Sign(key, bob, time.Hour)

	t.Run("bad tokens get 401", func(t *testing.T) {
		_, otherKey, _ := ed25519.GenerateKey(nil)
		wrongKey, _ := authn.Sign(otherKey, alice, time.Hour)
		expired, _ := authn.Sign(key, alice, -2*time.Minute) // beyond the 60 s leeway
		none, _ := jwt.NewWithClaims(jwt.SigningMethodNone, jwt.RegisteredClaims{
			Subject: alice.String(), ExpiresAt: jwt.NewNumericDate(time.Now().Add(time.Hour)),
		}).SignedString(jwt.UnsafeAllowNoneSignatureType)
		hmacWithPub, _ := jwt.NewWithClaims(jwt.SigningMethodHS256, jwt.RegisteredClaims{
			Subject: alice.String(), ExpiresAt: jwt.NewNumericDate(time.Now().Add(time.Hour)),
		}).SignedString([]byte(pub)) // classic algorithm-confusion attack
		noExp, _ := jwt.NewWithClaims(jwt.SigningMethodEdDSA, jwt.RegisteredClaims{
			Subject: alice.String(),
		}).SignedString(key)
		notUUID, _ := jwt.NewWithClaims(jwt.SigningMethodEdDSA, jwt.RegisteredClaims{
			Subject: "alice", ExpiresAt: jwt.NewNumericDate(time.Now().Add(time.Hour)),
		}).SignedString(key)

		for name, tok := range map[string]string{
			"missing": "", "wrong key": wrongKey, "expired": expired, "alg none": none,
			"HS256 with public key": hmacWithPub, "no exp": noExp, "sub not a UUID": notUUID,
		} {
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
	url := pgtest.New(t)
	db, err := sql.Open("pgx", url)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	p, err := migrations.Provider(db, "jobs")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := p.Up(t.Context()); err != nil {
		t.Fatal(err)
	}
	pool, err := pgxpool.New(t.Context(), url)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(pool.Close)
	v, err := authn.NewVerifier(base64.StdEncoding.EncodeToString(pub))
	if err != nil {
		t.Fatal(err)
	}
	return New(queue.NewStore(pool), v, slog.New(slog.DiscardHandler))
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
