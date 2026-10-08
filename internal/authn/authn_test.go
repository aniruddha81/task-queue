package authn

import (
	"crypto/ed25519"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
	"uuid"

	"github.com/golang-jwt/jwt/v5"
)

func TestVerifyRejectsBadTokens(t *testing.T) {
	pub, key, _ := ed25519.GenerateKey(nil)
	_, otherKey, _ := ed25519.GenerateKey(nil)
	v := NewVerifier(pub)
	id := Identity{Owner: uuid.NewV7()}

	good, _ := Sign(key, id, time.Hour)
	if got, err := v.Verify(good); err != nil || got.Owner != id.Owner {
		t.Fatalf("valid token: %+v %v", got, err)
	}

	exp := jwt.NewNumericDate(time.Now().Add(time.Hour))
	withKid := func(tok *jwt.Token) *jwt.Token { tok.Header["kid"] = KeyID(pub); return tok }
	wrongKey, _ := Sign(otherKey, id, time.Hour)
	expired, _ := Sign(key, id, -2*time.Minute) // beyond the 60 s leeway
	none, _ := withKid(jwt.NewWithClaims(jwt.SigningMethodNone, jwt.RegisteredClaims{
		Subject: id.Owner.String(), ExpiresAt: exp})).SignedString(jwt.UnsafeAllowNoneSignatureType)
	hmac, _ := withKid(jwt.NewWithClaims(jwt.SigningMethodHS256, jwt.RegisteredClaims{
		Subject: id.Owner.String(), ExpiresAt: exp})).SignedString([]byte(pub)) // algorithm confusion
	noExp, _ := withKid(jwt.NewWithClaims(jwt.SigningMethodEdDSA, jwt.RegisteredClaims{
		Subject: id.Owner.String()})).SignedString(key)
	notUUID, _ := withKid(jwt.NewWithClaims(jwt.SigningMethodEdDSA, jwt.RegisteredClaims{
		Subject: "alice", ExpiresAt: exp})).SignedString(key)
	noKid, _ := jwt.NewWithClaims(jwt.SigningMethodEdDSA, jwt.RegisteredClaims{
		Subject: id.Owner.String(), ExpiresAt: exp}).SignedString(key)

	for name, tok := range map[string]string{
		"empty": "", "wrong key": wrongKey, "expired": expired, "alg none": none,
		"HS256 with public key": hmac, "no exp": noExp, "sub not a UUID": notUUID, "no kid": noKid,
	} {
		if _, err := v.Verify(tok); err == nil {
			t.Errorf("%s token accepted", name)
		}
	}
	if _, err := NewVerifier().Verify(good); err == nil {
		t.Error("a verifier with no keys accepted a token (must fail closed)")
	}
}

func TestWatchLoadsJWKS(t *testing.T) {
	pub, key, _ := ed25519.GenerateKey(nil)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Write(JWKS(pub))
	}))
	defer srv.Close()

	v := NewVerifier()
	go v.Watch(t.Context(), srv.URL, srv.Client(), slog.New(slog.DiscardHandler))
	deadline := time.Now().Add(5 * time.Second)
	for v.Ready(t.Context()) != nil {
		if time.Now().After(deadline) {
			t.Fatal("JWKS not loaded")
		}
		time.Sleep(10 * time.Millisecond)
	}
	tok, _ := Sign(key, Identity{Owner: uuid.NewV7(), Admin: true}, time.Hour)
	if id, err := v.Verify(tok); err != nil || !id.Admin {
		t.Errorf("token from the JWKS key: %+v %v", id, err)
	}
}
