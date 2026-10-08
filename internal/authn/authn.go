// Package authn verifies the JWTs that identify a job's owner.
package authn

import (
	"crypto/ed25519"
	"crypto/sha256"
	"encoding/base64"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"
	"uuid"

	"github.com/golang-jwt/jwt/v5"
)

// Verifier accepts only EdDSA tokens signed by its key, with an exp claim and a UUID sub.
type Verifier struct{ key ed25519.PublicKey }

// NewVerifier takes a base64 (std encoding) Ed25519 public key, e.g. from $JWT_PUBLIC_KEY.
func NewVerifier(b64 string) (*Verifier, error) {
	key, err := base64.StdEncoding.DecodeString(b64)
	if err != nil || len(key) != ed25519.PublicKeySize {
		return nil, errors.New("JWT public key must be 32 bytes, base64-encoded")
	}
	return &Verifier{ed25519.PublicKey(key)}, nil
}

// Owner returns the verified owner ID from an "Authorization: Bearer <jwt>" header.
func (v *Verifier) Owner(r *http.Request) (uuid.UUID, error) {
	raw, ok := strings.CutPrefix(r.Header.Get("Authorization"), "Bearer ")
	if !ok {
		return uuid.UUID{}, errors.New("missing bearer token")
	}
	tok, err := jwt.ParseWithClaims(raw, &jwt.RegisteredClaims{},
		func(*jwt.Token) (any, error) { return v.key, nil },
		jwt.WithValidMethods([]string{"EdDSA"}), // never "none", never HMAC with a public key
		jwt.WithExpirationRequired(),
		jwt.WithLeeway(60*time.Second),
	)
	if err != nil {
		return uuid.UUID{}, err
	}
	sub, _ := tok.Claims.GetSubject()
	owner, err := uuid.Parse(sub)
	if err != nil {
		return uuid.UUID{}, fmt.Errorf("sub is not a UUID: %w", err)
	}
	return owner, nil
}

// Sign makes a token for owner. Tests and cmd/devtoken use it; in production only auth signs.
func Sign(key ed25519.PrivateKey, owner uuid.UUID, ttl time.Duration) (string, error) {
	return jwt.NewWithClaims(jwt.SigningMethodEdDSA, jwt.RegisteredClaims{
		Subject:   owner.String(),
		ExpiresAt: jwt.NewNumericDate(time.Now().Add(ttl)),
	}).SignedString(key)
}

// DevKey is a fixed, publicly known key for local development only. It is public,
// so nothing deployed may ever trust it; week 6's auth service replaces it.
func DevKey() ed25519.PrivateKey {
	seed := sha256.Sum256([]byte("task-queue local development only"))
	return ed25519.NewKeyFromSeed(seed[:])
}
