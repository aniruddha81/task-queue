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

func send(t *testing.T, srv *httptest.Server, method, path, token, body string) (int, []byte) {
	t.Helper()
	req, _ := http.NewRequest(method, srv.URL+path, strings.NewReader(body))
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	b, _ := io.ReadAll(resp.Body)
	return resp.StatusCode, b
}

func TestMe(t *testing.T) {
	_, srv := newService(t)
	_, tok := login(t, srv, "user@example.com", "user-password-12")
	code, b := send(t, srv, "GET", "/v1/auth/me", tok, "")
	var u struct {
		Email string
		Admin bool
	}
	json.Unmarshal(b, &u)
	if code != http.StatusOK || u.Email != "user@example.com" || u.Admin {
		t.Errorf("me: %d %s", code, b)
	}
	if code, _ := send(t, srv, "GET", "/v1/auth/me", "", ""); code != http.StatusUnauthorized {
		t.Errorf("me without a token: %d, want 401", code)
	}
}

func TestChangePassword(t *testing.T) {
	_, srv := newService(t)
	_, tok := login(t, srv, "user@example.com", "user-password-12")
	for _, c := range []struct {
		name, body string
		want       int
	}{
		{"wrong current", `{"current":"not-my-password","new":"a-brand-new-password"}`, http.StatusForbidden},
		{"too short", `{"current":"user-password-12","new":"short"}`, http.StatusBadRequest},
		{"right", `{"current":"user-password-12","new":"a-brand-new-password"}`, http.StatusNoContent},
	} {
		if code, b := send(t, srv, "POST", "/v1/auth/password", tok, c.body); code != c.want {
			t.Errorf("%s: %d %s, want %d", c.name, code, b, c.want)
		}
	}
	if code, _ := send(t, srv, "POST", "/v1/auth/password", "", `{"current":"x","new":"a-brand-new-password"}`); code != http.StatusUnauthorized {
		t.Errorf("no token: %d, want 401", code)
	}
	if code, _ := login(t, srv, "user@example.com", "user-password-12"); code != http.StatusUnauthorized {
		t.Errorf("old password still works: %d", code)
	}
	if code, _ := login(t, srv, "user@example.com", "a-brand-new-password"); code != http.StatusOK {
		t.Errorf("new password refused: %d", code)
	}
}

func TestListAndDeleteUsers(t *testing.T) {
	_, srv := newService(t)
	_, adminTok := login(t, srv, "admin@example.com", "admin-password-1")
	_, userTok := login(t, srv, "user@example.com", "user-password-12")

	if code, _ := send(t, srv, "GET", "/v1/auth/users", userTok, ""); code != http.StatusForbidden {
		t.Errorf("non-admin list: %d, want 403", code)
	}
	code, b := send(t, srv, "GET", "/v1/auth/users", adminTok, "")
	var list struct {
		Users []struct {
			ID    string
			Email string
		}
	}
	json.Unmarshal(b, &list)
	if code != http.StatusOK || len(list.Users) != 2 {
		t.Fatalf("list: %d %s", code, b)
	}
	ids := map[string]string{}
	for _, u := range list.Users {
		ids[u.Email] = u.ID
	}

	if code, _ := send(t, srv, "DELETE", "/v1/auth/users/"+ids["admin@example.com"], userTok, ""); code != http.StatusForbidden {
		t.Errorf("non-admin delete: %d, want 403", code)
	}
	if code, _ := send(t, srv, "DELETE", "/v1/auth/users/"+ids["admin@example.com"], adminTok, ""); code != http.StatusConflict {
		t.Errorf("admin deleting itself: %d, want 409", code)
	}
	if code, _ := send(t, srv, "DELETE", "/v1/auth/users/"+ids["user@example.com"], adminTok, ""); code != http.StatusNoContent {
		t.Errorf("delete: %d, want 204", code)
	}
	if code, _ := send(t, srv, "DELETE", "/v1/auth/users/"+ids["user@example.com"], adminTok, ""); code != http.StatusNotFound {
		t.Errorf("delete again: %d, want 404", code)
	}
	if code, _ := login(t, srv, "user@example.com", "user-password-12"); code != http.StatusUnauthorized {
		t.Errorf("deleted user can still sign in: %d", code)
	}
}

func TestAPIKeys(t *testing.T) {
	s, srv := newService(t)
	_, userTok := login(t, srv, "user@example.com", "user-password-12")
	_, adminTok := login(t, srv, "admin@example.com", "admin-password-1")

	code, b := send(t, srv, "POST", "/v1/auth/keys", userTok, `{"name":"billing service"}`)
	var k struct{ ID, Key string }
	json.Unmarshal(b, &k)
	if code != http.StatusCreated || !strings.HasPrefix(k.Key, "tq_") || len(k.Key) < 40 {
		t.Fatalf("create: %d %s", code, b)
	}
	if code, _ := send(t, srv, "POST", "/v1/auth/keys", "", `{"name":"x"}`); code != http.StatusUnauthorized {
		t.Errorf("create without a token: %d, want 401", code)
	}

	// The key buys a token for its owner.
	code, b = send(t, srv, "POST", "/v1/auth/token", k.Key, "")
	var tok struct{ Token string }
	json.Unmarshal(b, &tok)
	if code != http.StatusOK || tok.Token == "" {
		t.Fatalf("exchange: %d %s", code, b)
	}
	if code, b := send(t, srv, "GET", "/v1/auth/me", tok.Token, ""); code != http.StatusOK || !strings.Contains(string(b), "user@example.com") {
		t.Errorf("token from key is not the owner's: %d %s", code, b)
	}
	for name, bad := range map[string]string{"wrong key": "tq_not-a-real-key", "no prefix": "abc", "a session token": userTok} {
		if code, _ := send(t, srv, "POST", "/v1/auth/token", bad, ""); code != http.StatusUnauthorized {
			t.Errorf("exchange with %s: %d, want 401", name, code)
		}
	}

	// Listed without the secret; nobody else's to see or revoke.
	if code, b := send(t, srv, "GET", "/v1/auth/keys", userTok, ""); code != http.StatusOK || strings.Contains(string(b), k.Key) || !strings.Contains(string(b), "billing service") {
		t.Errorf("list: %d %s", code, b)
	}
	if code, b := send(t, srv, "GET", "/v1/auth/keys", adminTok, ""); code != http.StatusOK || strings.Contains(string(b), k.ID) {
		t.Errorf("another user's list shows the key: %d %s", code, b)
	}
	if code, _ := send(t, srv, "DELETE", "/v1/auth/keys/"+k.ID, adminTok, ""); code != http.StatusNotFound {
		t.Errorf("another user revoking: %d, want 404", code)
	}

	// Only a hash is stored.
	var stored []byte
	s.db.QueryRow(t.Context(), `SELECT hash FROM api_keys WHERE id = $1`, k.ID).Scan(&stored)
	if len(stored) != 32 || strings.Contains(string(stored), k.Key) {
		t.Errorf("stored %d bytes; want a 32-byte hash", len(stored))
	}

	if code, _ := send(t, srv, "DELETE", "/v1/auth/keys/"+k.ID, userTok, ""); code != http.StatusNoContent {
		t.Errorf("revoke: %d, want 204", code)
	}
	if code, _ := send(t, srv, "POST", "/v1/auth/token", k.Key, ""); code != http.StatusUnauthorized {
		t.Errorf("revoked key still exchanges: %d", code)
	}
}
