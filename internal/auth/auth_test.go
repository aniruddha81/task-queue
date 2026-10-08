package auth

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
)

func TestPasswordHash(t *testing.T) {
	h, err := hashPassword("correct horse battery")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(h, "$argon2id$v=19$m=19456,t=2,p=1$") {
		t.Errorf("hash format: %s", h)
	}
	if ok, _ := checkPassword("correct horse battery", h); !ok {
		t.Error("right password rejected")
	}
	if ok, _ := checkPassword("correct horse batterY", h); ok {
		t.Error("wrong password accepted")
	}
	if h2, _ := hashPassword("correct horse battery"); h2 == h {
		t.Error("two hashes of one password are equal: salt not random")
	}
}

func newService(t *testing.T) (*Service, *httptest.Server) {
	_, key, _ := ed25519.GenerateKey(nil)
	s, err := New(pgtest.Migrated(t, "auth"), key, slog.New(slog.DiscardHandler))
	if err != nil {
		t.Fatal(err)
	}
	if err := s.Seed(t.Context(), "admin@example.com:admin-password-1:admin,user@example.com:user-password-12"); err != nil {
		t.Fatal(err)
	}
	srv := httptest.NewServer(s.Handler())
	t.Cleanup(srv.Close)
	return s, srv
}

func post(t *testing.T, srv *httptest.Server, path, token, body string) (*http.Response, []byte) {
	t.Helper()
	req, _ := http.NewRequest("POST", srv.URL+path, strings.NewReader(body))
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	b, _ := io.ReadAll(resp.Body)
	return resp, b
}

func login(t *testing.T, srv *httptest.Server, email, password string) (int, string) {
	resp, b := post(t, srv, "/v1/auth/login", "", `{"email":"`+email+`","password":"`+password+`"}`)
	var out struct{ Token string }
	json.Unmarshal(b, &out)
	return resp.StatusCode, out.Token
}

func TestLogin(t *testing.T) {
	_, srv := newService(t)

	resp, b := post(t, srv, "/v1/auth/login", "", `{"email":"User@Example.com ","password":"user-password-12"}`)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("login: %d %s", resp.StatusCode, b)
	}
	var cookie *http.Cookie
	for _, c := range resp.Cookies() {
		if c.Name == CookieName {
			cookie = c
		}
	}
	if cookie == nil || !cookie.HttpOnly || !cookie.Secure || cookie.SameSite != http.SameSiteStrictMode {
		t.Errorf("session cookie must be HttpOnly, Secure, SameSite=Strict: %+v", cookie)
	}

	// The token verifies against the published JWKS.
	jwksResp, err := http.Get(srv.URL + "/.well-known/jwks.json")
	if err != nil {
		t.Fatal(err)
	}
	jwksResp.Body.Close()
	keys, err := authn.FetchJWKS(t.Context(), http.DefaultClient, srv.URL+"/.well-known/jwks.json")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := authn.NewVerifier(keys...).Verify(cookie.Value); err != nil {
		t.Errorf("login token doesn't verify against JWKS: %v", err)
	}

	for name, pw := range map[string][2]string{
		"wrong password": {"user@example.com", "nope-nope-nope"},
		"unknown email":  {"nobody@example.com", "user-password-12"},
	} {
		if code, _ := login(t, srv, pw[0], pw[1]); code != http.StatusUnauthorized {
			t.Errorf("%s: %d, want 401", name, code)
		}
	}
}

func TestLoginRateLimited(t *testing.T) {
	_, srv := newService(t)
	limited := false
	for range 10 {
		if code, _ := login(t, srv, "user@example.com", "guessing-wrong"); code == http.StatusTooManyRequests {
			limited = true
			break
		}
	}
	if !limited {
		t.Error("10 rapid wrong logins were never rate-limited")
	}
}

func TestOnlyAdminsCreateUsers(t *testing.T) {
	_, srv := newService(t)
	_, adminTok := login(t, srv, "admin@example.com", "admin-password-1")
	_, userTok := login(t, srv, "user@example.com", "user-password-12")
	forged, _ := authn.Sign(func() ed25519.PrivateKey { _, k, _ := ed25519.GenerateKey(nil); return k }(),
		authn.Identity{Owner: uuid.NewV7(), Admin: true}, time.Hour)

	body := `{"email":"new@example.com","password":"a-long-password"}`
	for name, tok := range map[string]string{"no token": "", "non-admin": userTok, "forged admin": forged} {
		if resp, _ := post(t, srv, "/v1/auth/users", tok, body); resp.StatusCode != http.StatusForbidden {
			t.Errorf("%s: %d, want 403", name, resp.StatusCode)
		}
	}
	if resp, b := post(t, srv, "/v1/auth/users", adminTok, body); resp.StatusCode != http.StatusCreated {
		t.Fatalf("admin create: %d %s", resp.StatusCode, b)
	}
	if resp, _ := post(t, srv, "/v1/auth/users", adminTok, body); resp.StatusCode != http.StatusConflict {
		t.Errorf("duplicate: %d, want 409", resp.StatusCode)
	}
	if resp, _ := post(t, srv, "/v1/auth/users", adminTok, `{"email":"x@example.com","password":"short"}`); resp.StatusCode != http.StatusBadRequest {
		t.Errorf("weak password: %d, want 400", resp.StatusCode)
	}
	if code, _ := login(t, srv, "new@example.com", "a-long-password"); code != http.StatusOK {
		t.Errorf("new user login: %d", code)
	}

}
