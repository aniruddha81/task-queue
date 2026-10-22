package gateway

import (
	"crypto/ed25519"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"
	"uuid"

	"github.com/aniruddha81/task-queue/internal/auth"
	"github.com/aniruddha81/task-queue/internal/authn"
)

const origin = "https://dash.example"

type seen struct {
	calls         int
	authorization string
	cookie        string
}

func setup(t *testing.T) (*httptest.Server, *seen, ed25519.PrivateKey) {
	pub, key, _ := ed25519.GenerateKey(nil)
	var got seen
	jobs := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		got.calls++
		got.authorization, got.cookie = r.Header.Get("Authorization"), r.Header.Get("Cookie")
		io.Copy(io.Discard, r.Body)
	}))
	authSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte("auth:" + r.URL.Path + "|" + r.Header.Get("Authorization")))
	}))
	t.Cleanup(jobs.Close)
	t.Cleanup(authSrv.Close)
	ju, _ := url.Parse(jobs.URL)
	au, _ := url.Parse(authSrv.URL)
	gw := httptest.NewServer(New(Config{
		Auth: []*url.URL{au}, Jobs: []*url.URL{ju}, Verifier: authn.NewVerifier(pub), Origins: []string{"https://other.example", origin},
		Static: http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.Write([]byte("dashboard")) }),
		Rate:   1, Burst: 3, Log: slog.New(slog.DiscardHandler),
	}))
	t.Cleanup(gw.Close)
	return gw, &got, key
}

func call(t *testing.T, gw *httptest.Server, method, path string, hdr map[string]string, body string) (int, string) {
	t.Helper()
	req, _ := http.NewRequest(method, gw.URL+path, strings.NewReader(body))
	for k, v := range hdr {
		req.Header.Set(k, v)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	b, _ := io.ReadAll(resp.Body)
	return resp.StatusCode, string(b)
}

func TestGatewayAuth(t *testing.T) {
	gw, got, key := setup(t)
	tok, _ := authn.Sign(key, authn.Identity{Owner: uuid.NewV7()}, time.Hour)

	if code, _ := call(t, gw, "GET", "/v1/jobs", nil, ""); code != http.StatusUnauthorized || got.calls != 0 {
		t.Errorf("no token: %d, upstream calls %d; want 401 and none", code, got.calls)
	}
	if code, _ := call(t, gw, "GET", "/v1/jobs", map[string]string{"Authorization": "Bearer " + tok}, ""); code != 200 {
		t.Errorf("bearer: %d", code)
	}

	cookie := map[string]string{"Cookie": auth.CookieName + "=" + tok}
	if code, _ := call(t, gw, "GET", "/v1/jobs", cookie, ""); code != 200 {
		t.Errorf("cookie GET: %d", code)
	}
	if got.authorization != "Bearer "+tok || got.cookie != "" {
		t.Errorf("upstream saw Authorization=%q Cookie=%q; want the bearer token and no cookie", got.authorization, got.cookie)
	}
	cookie["Origin"] = origin
	if code, _ := call(t, gw, "POST", "/v1/jobs", cookie, "{}"); code == http.StatusForbidden {
		t.Errorf("cookie POST from the second listed origin: %d, want it let through", code)
	}
	cookie["Origin"] = "https://evil.example"
	if code, _ := call(t, gw, "POST", "/v1/jobs", cookie, "{}"); code != http.StatusForbidden {
		t.Errorf("cookie POST from another origin: %d, want 403", code)
	}
	delete(cookie, "Origin")
	if code, _ := call(t, gw, "POST", "/v1/jobs", cookie, "{}"); code != http.StatusForbidden {
		t.Errorf("cookie POST without Origin: %d, want 403", code)
	}
}

func TestGatewayLimits(t *testing.T) {
	gw, _, key := setup(t)
	tok, _ := authn.Sign(key, authn.Identity{Owner: uuid.NewV7()}, time.Hour)
	h := map[string]string{"Authorization": "Bearer " + tok}

	if code, _ := call(t, gw, "POST", "/v1/jobs", h, strings.Repeat("x", 65<<10)); code != http.StatusRequestEntityTooLarge {
		t.Errorf("65 KB body: %d, want 413", code)
	}
	limited := false
	for range 6 { // burst is 3
		if code, _ := call(t, gw, "GET", "/v1/jobs", h, ""); code == http.StatusTooManyRequests {
			limited = true
		}
	}
	if !limited {
		t.Error("6 requests at once with burst 3 were never rate-limited")
	}
}

func TestGatewayRoutes(t *testing.T) {
	gw, _, _ := setup(t)
	if code, body := call(t, gw, "POST", "/v1/auth/login", nil, "{}"); code != 200 || body != "auth:/v1/auth/login|" {
		t.Errorf("login route: %d %q (no token needed)", code, body)
	}
	// Account routes need the caller: a browser session's cookie becomes the bearer token,
	// with the same Origin check as any other write.
	gw, _, key := setup(t)
	var session map[string]string
	for _, r := range [][2]string{{"GET", "/v1/auth/me"}, {"POST", "/v1/auth/password"}, {"GET", "/v1/auth/users"}, {"DELETE", "/v1/auth/users/x"}, {"POST", "/v1/auth/keys"}, {"DELETE", "/v1/auth/keys/x"}} {
		tok, _ := authn.Sign(key, authn.Identity{Owner: uuid.NewV7()}, time.Hour) // one user per route: the test burst is 3
		session = map[string]string{"Cookie": auth.CookieName + "=" + tok, "Origin": origin}
		if code, body := call(t, gw, r[0], r[1], session, "{}"); code != 200 || body != "auth:"+r[1]+"|Bearer "+tok {
			t.Errorf("%s %s with a session: %d %q", r[0], r[1], code, body)
		}
		if code, _ := call(t, gw, r[0], r[1], nil, "{}"); code != http.StatusUnauthorized {
			t.Errorf("%s %s without a session: %d, want 401", r[0], r[1], code)
		}
	}
	session["Origin"] = "https://evil.example"
	if code, _ := call(t, gw, "POST", "/v1/auth/password", session, "{}"); code != http.StatusForbidden {
		t.Errorf("password change from another origin: %d, want 403", code)
	}
	// The key exchange carries the key as its bearer token, untouched.
	if code, body := call(t, gw, "POST", "/v1/auth/token", map[string]string{"Authorization": "Bearer tq_k"}, ""); code != 200 || body != "auth:/v1/auth/token|Bearer tq_k" {
		t.Errorf("key exchange: %d %q", code, body)
	}
	if code, body := call(t, gw, "GET", "/", nil, ""); code != 200 || body != "dashboard" {
		t.Errorf("dashboard: %d %q", code, body)
	}
}

// TestGatewayPublic: the live page's routes need no token, only reads and starting a demo are
// public, and one address gets a handful of demos at once.
func TestGatewayPublic(t *testing.T) {
	gw, got, _ := setup(t)
	if code, _ := call(t, gw, "GET", "/v1/public/stats", nil, ""); code != 200 || got.calls != 1 {
		t.Errorf("stats without a token: %d, upstream calls %d", code, got.calls)
	}
	if code, _ := call(t, gw, "POST", "/v1/public/stats", nil, ""); code != http.StatusUnauthorized {
		t.Errorf("another public write: %d, want 401", code)
	}
	codes := map[int]int{}
	for range 6 { // a visitor's burst is 5
		code, _ := call(t, gw, "POST", "/v1/public/try", nil, `{"scenario":"pay"}`)
		codes[code]++
	}
	if codes[200] != 5 || codes[http.StatusTooManyRequests] != 1 {
		t.Errorf("6 demos from one address: %v, want 5 started and 1 refused", codes)
	}
}
