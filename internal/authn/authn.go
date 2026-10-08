// Package authn issues and verifies the JWTs that identify a user. Tokens are EdDSA
// (Ed25519) with a key ID; verifiers get the public keys from auth's JWKS.
package authn

import (
	"context"
	"crypto/ed25519"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"strings"
	"sync"
	"time"
	"uuid"

	"github.com/golang-jwt/jwt/v5"
)

// Identity is a verified caller.
type Identity struct {
	Owner uuid.UUID
	Admin bool
}

type claims struct {
	jwt.RegisteredClaims
	Admin bool `json:"adm,omitempty"`
}

// KeyID names a public key: the first 8 bytes of its SHA-256, in hex.
func KeyID(pub ed25519.PublicKey) string {
	sum := sha256.Sum256(pub)
	return hex.EncodeToString(sum[:8])
}

// Sign makes a token. Only auth signs in production; tests sign with their own keys.
func Sign(key ed25519.PrivateKey, id Identity, ttl time.Duration) (string, error) {
	tok := jwt.NewWithClaims(jwt.SigningMethodEdDSA, claims{
		RegisteredClaims: jwt.RegisteredClaims{
			Subject:   id.Owner.String(),
			ExpiresAt: jwt.NewNumericDate(time.Now().Add(ttl)),
			IssuedAt:  jwt.NewNumericDate(time.Now()),
		},
		Admin: id.Admin,
	})
	tok.Header["kid"] = KeyID(key.Public().(ed25519.PublicKey))
	return tok.SignedString(key)
}

// Verifier accepts only EdDSA tokens signed by one of its keys (chosen by kid), with an
// exp claim and a UUID subject. Until it has keys it rejects everything (fails closed).
type Verifier struct {
	mu   sync.RWMutex
	keys map[string]ed25519.PublicKey
}

func NewVerifier(keys ...ed25519.PublicKey) *Verifier {
	v := &Verifier{}
	v.set(keys)
	return v
}

func (v *Verifier) set(keys []ed25519.PublicKey) {
	m := make(map[string]ed25519.PublicKey, len(keys))
	for _, k := range keys {
		m[KeyID(k)] = k
	}
	v.mu.Lock()
	v.keys = m
	v.mu.Unlock()
}

// Ready reports whether the verifier has keys; wire it into /readyz.
func (v *Verifier) Ready(context.Context) error {
	v.mu.RLock()
	defer v.mu.RUnlock()
	if len(v.keys) == 0 {
		return errors.New("no JWT verification keys yet")
	}
	return nil
}

// Verify checks a raw token.
func (v *Verifier) Verify(raw string) (Identity, error) {
	var c claims
	_, err := jwt.ParseWithClaims(raw, &c, func(t *jwt.Token) (any, error) {
		kid, _ := t.Header["kid"].(string)
		v.mu.RLock()
		defer v.mu.RUnlock()
		if k, ok := v.keys[kid]; ok {
			return k, nil
		}
		return nil, fmt.Errorf("unknown key %q", kid)
	},
		jwt.WithValidMethods([]string{"EdDSA"}), // never "none", never HMAC with a public key
		jwt.WithExpirationRequired(),
		jwt.WithLeeway(60*time.Second),
	)
	if err != nil {
		return Identity{}, err
	}
	owner, err := uuid.Parse(c.Subject)
	if err != nil {
		return Identity{}, fmt.Errorf("sub is not a UUID: %w", err)
	}
	return Identity{Owner: owner, Admin: c.Admin}, nil
}

// FromRequest verifies an "Authorization: Bearer <jwt>" header.
func (v *Verifier) FromRequest(r *http.Request) (Identity, error) {
	raw, ok := strings.CutPrefix(r.Header.Get("Authorization"), "Bearer ")
	if !ok {
		return Identity{}, errors.New("missing bearer token")
	}
	return v.Verify(raw)
}

// Watch loads keys from a JWKS URL now (retrying until it works) and refreshes them
// hourly, so a rotated key is picked up. It returns when ctx ends.
func (v *Verifier) Watch(ctx context.Context, url string, client *http.Client, log *slog.Logger) {
	for wait := time.Second; ctx.Err() == nil; {
		keys, err := FetchJWKS(ctx, client, url)
		if err == nil {
			v.set(keys)
			wait = time.Hour
		} else if ctx.Err() == nil {
			log.Warn("fetch JWKS", "url", url, "err", err)
			if v.Ready(ctx) == nil {
				wait = time.Minute // keep the old keys and try again soon
			}
		}
		select {
		case <-ctx.Done():
		case <-time.After(wait):
		}
	}
}

type jwk struct {
	Kty string `json:"kty"`
	Crv string `json:"crv"`
	X   string `json:"x"`
	Kid string `json:"kid"`
	Alg string `json:"alg"`
	Use string `json:"use"`
}

// JWKS renders public keys as a JSON Web Key Set.
func JWKS(keys ...ed25519.PublicKey) []byte {
	set := struct {
		Keys []jwk `json:"keys"`
	}{}
	for _, k := range keys {
		set.Keys = append(set.Keys, jwk{Kty: "OKP", Crv: "Ed25519", Alg: "EdDSA", Use: "sig",
			X: base64.RawURLEncoding.EncodeToString(k), Kid: KeyID(k)})
	}
	b, _ := json.Marshal(set)
	return b
}

func FetchJWKS(ctx context.Context, client *http.Client, url string) ([]ed25519.PublicKey, error) {
	req, err := http.NewRequestWithContext(ctx, "GET", url, nil)
	if err != nil {
		return nil, err
	}
	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("JWKS: %s", resp.Status)
	}
	var set struct{ Keys []jwk }
	if err := json.NewDecoder(resp.Body).Decode(&set); err != nil {
		return nil, err
	}
	var keys []ed25519.PublicKey
	for _, k := range set.Keys {
		x, err := base64.RawURLEncoding.DecodeString(k.X)
		if k.Kty != "OKP" || k.Crv != "Ed25519" || err != nil || len(x) != ed25519.PublicKeySize {
			continue
		}
		keys = append(keys, ed25519.PublicKey(x))
	}
	if len(keys) == 0 {
		return nil, errors.New("JWKS has no Ed25519 keys")
	}
	return keys, nil
}

// ParsePrivateKey reads a base64 (std) Ed25519 seed (32 bytes) or private key (64 bytes).
func ParsePrivateKey(b64 string) (ed25519.PrivateKey, error) {
	b, err := base64.StdEncoding.DecodeString(b64)
	switch {
	case err != nil:
		return nil, err
	case len(b) == ed25519.SeedSize:
		return ed25519.NewKeyFromSeed(b), nil
	case len(b) == ed25519.PrivateKeySize:
		return ed25519.PrivateKey(b), nil
	}
	return nil, errors.New("Ed25519 key must be a 32-byte seed or 64-byte key, base64-encoded")
}
