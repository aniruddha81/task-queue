// Package auth is the auth service: login, admin-only user creation, and the JWKS that
// other services verify tokens with.
//
//	POST /v1/auth/login           {email, password} -> {token, expires_at}, plus a session cookie
//	POST /v1/auth/logout          clears the cookie (the token itself stays valid until it expires)
//	POST /v1/auth/users           admin only: {email, password, admin}
//	GET  /.well-known/jwks.json   public keys
package auth

import (
	"context"
	"crypto/ed25519"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"strings"
	"sync"
	"time"
	"uuid"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
	"golang.org/x/time/rate"

	"github.com/aniruddha81/task-queue/internal/authn"
)

const (
	TokenTTL   = time.Hour
	CookieName = "tq_session"
)

type Service struct {
	db     *pgxpool.Pool
	key    ed25519.PrivateKey
	verify *authn.Verifier // its own key: for admin-only routes
	log    *slog.Logger

	mu       sync.Mutex
	limiters map[string]*rate.Limiter // login attempts per email
	dummy    string                   // hashed once, so unknown emails cost as much as wrong passwords
}

func New(db *pgxpool.Pool, key ed25519.PrivateKey, log *slog.Logger) (*Service, error) {
	dummy, err := hashPassword("not a real password")
	if err != nil {
		return nil, err
	}
	return &Service{db: db, key: key, verify: authn.NewVerifier(key.Public().(ed25519.PublicKey)),
		log: log, limiters: map[string]*rate.Limiter{}, dummy: dummy}, nil
}

func (s *Service) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("POST /v1/auth/login", s.login)
	mux.HandleFunc("POST /v1/auth/logout", s.logout)
	mux.HandleFunc("POST /v1/auth/users", s.createUser)
	mux.HandleFunc("GET /.well-known/jwks.json", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("Cache-Control", "max-age=300")
		w.Write(authn.JWKS(s.key.Public().(ed25519.PublicKey)))
	})
	return mux
}

type credentials struct {
	Email    string `json:"email"`
	Password string `json:"password"`
	Admin    bool   `json:"admin"`
}

func readCredentials(w http.ResponseWriter, r *http.Request) (credentials, bool) {
	var c credentials
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 4<<10)).Decode(&c); err != nil {
		writeError(w, http.StatusBadRequest, "invalid JSON body")
		return c, false
	}
	c.Email = strings.ToLower(strings.TrimSpace(c.Email))
	return c, true
}

func (s *Service) login(w http.ResponseWriter, r *http.Request) {
	c, ok := readCredentials(w, r)
	if !ok {
		return
	}
	if !s.limiter(c.Email).Allow() {
		writeError(w, http.StatusTooManyRequests, "too many login attempts; wait and retry")
		return
	}
	var id uuid.UUID
	var hash string
	var admin bool
	err := s.db.QueryRow(r.Context(), `SELECT id, password_hash, admin FROM users WHERE email = $1`, c.Email).
		Scan(&id, &hash, &admin)
	if errors.Is(err, pgx.ErrNoRows) {
		checkPassword(c.Password, s.dummy) // same cost as a real check: no user enumeration by timing
		writeError(w, http.StatusUnauthorized, "invalid email or password")
		return
	}
	if err != nil {
		s.log.Error("login", "err", err)
		writeError(w, http.StatusServiceUnavailable, "try again")
		return
	}
	if ok, err := checkPassword(c.Password, hash); err != nil || !ok {
		writeError(w, http.StatusUnauthorized, "invalid email or password")
		return
	}
	expires := time.Now().Add(TokenTTL)
	tok, err := authn.Sign(s.key, authn.Identity{Owner: id, Admin: admin}, TokenTTL)
	if err != nil {
		s.log.Error("sign", "err", err)
		writeError(w, http.StatusInternalServerError, "internal error")
		return
	}
	// The browser keeps the token in an HttpOnly cookie, out of reach of page scripts.
	http.SetCookie(w, &http.Cookie{Name: CookieName, Value: tok, Path: "/", MaxAge: int(TokenTTL.Seconds()),
		HttpOnly: true, Secure: true, SameSite: http.SameSiteStrictMode})
	writeJSON(w, http.StatusOK, map[string]any{"token": tok, "expires_at": expires})
}

func (s *Service) logout(w http.ResponseWriter, _ *http.Request) {
	http.SetCookie(w, &http.Cookie{Name: CookieName, Value: "", Path: "/", MaxAge: -1,
		HttpOnly: true, Secure: true, SameSite: http.SameSiteStrictMode})
	w.WriteHeader(http.StatusNoContent)
}

func (s *Service) createUser(w http.ResponseWriter, r *http.Request) {
	if id, err := s.verify.FromRequest(r); err != nil || !id.Admin {
		writeError(w, http.StatusForbidden, "admin token required")
		return
	}
	c, ok := readCredentials(w, r)
	if !ok {
		return
	}
	id, err := s.CreateUser(r.Context(), c.Email, c.Password, c.Admin)
	var pgErr *pgconn.PgError
	switch {
	case errors.As(err, &pgErr) && pgErr.Code == "23505":
		writeError(w, http.StatusConflict, "email already registered")
	case errors.As(err, &pgErr) && pgErr.Code == "23514":
		writeError(w, http.StatusBadRequest, "invalid email")
	case errors.Is(err, errWeakPassword):
		writeError(w, http.StatusBadRequest, err.Error())
	case err != nil:
		s.log.Error("create user", "err", err)
		writeError(w, http.StatusInternalServerError, "internal error")
	default:
		writeJSON(w, http.StatusCreated, map[string]any{"id": id, "email": c.Email, "admin": c.Admin})
	}
}

var errWeakPassword = errors.New("password must be at least 12 characters")

// CreateUser adds a user. It is also how seed users are made at startup.
func (s *Service) CreateUser(ctx context.Context, email, password string, admin bool) (uuid.UUID, error) {
	if len(password) < 12 {
		return uuid.UUID{}, errWeakPassword
	}
	hash, err := hashPassword(password)
	if err != nil {
		return uuid.UUID{}, err
	}
	var id uuid.UUID
	err = s.db.QueryRow(ctx, `INSERT INTO users (email, password_hash, admin) VALUES ($1, $2, $3) RETURNING id`,
		strings.ToLower(strings.TrimSpace(email)), hash, admin).Scan(&id)
	return id, err
}

// Seed creates users from "email:password[:admin],..." unless they already exist.
func (s *Service) Seed(ctx context.Context, spec string) error {
	for _, entry := range strings.Split(spec, ",") {
		if entry = strings.TrimSpace(entry); entry == "" {
			continue
		}
		parts := strings.Split(entry, ":")
		if len(parts) < 2 {
			return errors.New("seed users must be email:password[:admin]")
		}
		_, err := s.CreateUser(ctx, parts[0], parts[1], len(parts) > 2 && parts[2] == "admin")
		var pgErr *pgconn.PgError
		if err != nil && !(errors.As(err, &pgErr) && pgErr.Code == "23505") {
			return err
		}
	}
	return nil
}

func (s *Service) limiter(email string) *rate.Limiter {
	s.mu.Lock()
	defer s.mu.Unlock()
	l, ok := s.limiters[email]
	if !ok {
		// ponytail: one limiter per email, kept forever; bounded by distinct emails tried.
		// Add eviction if login traffic ever comes from the open internet at scale.
		l = rate.NewLimiter(rate.Every(2*time.Second), 5)
		s.limiters[email] = l
	}
	return l
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(v)
}

func writeError(w http.ResponseWriter, status int, msg string) {
	writeJSON(w, status, map[string]string{"error": msg})
}
