// Package auth is the auth service: login, admin-only user creation, and the JWKS that
// other services verify tokens with.
//
//	POST /v1/auth/login           {email, password} -> {token, expires_at}, plus a session cookie
//	POST /v1/auth/logout          clears the cookie (the token itself stays valid until it expires)
//	GET  /v1/auth/me              the caller: {id, email, admin}
//	POST /v1/auth/password        change the caller's password: {current, new}
//	POST /v1/auth/users           admin only: {email, password, admin}
//	GET  /v1/auth/users           admin only: every user
//	DELETE /v1/auth/users/{id}    admin only: the user can't sign in again (a token already
//	                              issued stays valid until it expires, at most TokenTTL)
//	GET  /.well-known/jwks.json   public keys
//	     /internal/acme/{key}     the gateways' shared certificate cache (gateway's mTLS cert only)
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
	"github.com/aniruddha81/task-queue/internal/pgretry"
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
	mux.HandleFunc("GET /v1/auth/me", s.me)
	mux.HandleFunc("POST /v1/auth/password", s.changePassword)
	mux.HandleFunc("POST /v1/auth/users", s.createUser)
	mux.HandleFunc("GET /v1/auth/users", s.listUsers)
	mux.HandleFunc("DELETE /v1/auth/users/{id}", s.deleteUser)
	mux.HandleFunc("/internal/acme/{key}", s.acme) // the gateways' shared certificate cache
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
	_, err := pgretry.Do(r.Context(), func(ctx context.Context) (struct{}, error) { // rides out a database failover
		return struct{}{}, s.db.QueryRow(ctx, `SELECT id, password_hash, admin FROM users WHERE email = $1`, c.Email).
			Scan(&id, &hash, &admin)
	})
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

// caller returns the token's identity, or writes 401 (or 403 when admin is required).
func (s *Service) caller(w http.ResponseWriter, r *http.Request, admin bool) (authn.Identity, bool) {
	id, err := s.verify.FromRequest(r)
	switch {
	case err != nil:
		writeError(w, http.StatusUnauthorized, "invalid or missing token")
	case admin && !id.Admin:
		writeError(w, http.StatusForbidden, "admin token required")
	default:
		return id, true
	}
	return id, false
}

type user struct {
	ID        uuid.UUID `json:"id"`
	Email     string    `json:"email"`
	Admin     bool      `json:"admin"`
	CreatedAt time.Time `json:"created_at"`
}

func (s *Service) me(w http.ResponseWriter, r *http.Request) {
	id, ok := s.caller(w, r, false)
	if !ok {
		return
	}
	var u user
	err := s.db.QueryRow(r.Context(), `SELECT id, email, admin, created_at FROM users WHERE id = $1`, id.Owner).
		Scan(&u.ID, &u.Email, &u.Admin, &u.CreatedAt)
	switch {
	case errors.Is(err, pgx.ErrNoRows):
		writeError(w, http.StatusUnauthorized, "user no longer exists")
	case err != nil:
		s.log.Error("me", "err", err)
		writeError(w, http.StatusInternalServerError, "internal error")
	default:
		writeJSON(w, http.StatusOK, u)
	}
}

// changePassword needs the current password too, so a stolen session can't lock its owner
// out. Guesses at it share the login limit for that email.
func (s *Service) changePassword(w http.ResponseWriter, r *http.Request) {
	id, ok := s.caller(w, r, false)
	if !ok {
		return
	}
	var b struct{ Current, New string }
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 4<<10)).Decode(&b); err != nil {
		writeError(w, http.StatusBadRequest, "invalid JSON body")
		return
	}
	if len(b.New) < 12 {
		writeError(w, http.StatusBadRequest, errWeakPassword.Error())
		return
	}
	var email, hash string
	if err := s.db.QueryRow(r.Context(), `SELECT email, password_hash FROM users WHERE id = $1`, id.Owner).Scan(&email, &hash); err != nil {
		writeError(w, http.StatusUnauthorized, "user no longer exists")
		return
	}
	if !s.limiter(email).Allow() {
		writeError(w, http.StatusTooManyRequests, "too many attempts; wait and retry")
		return
	}
	if ok, err := checkPassword(b.Current, hash); err != nil || !ok {
		writeError(w, http.StatusForbidden, "current password is wrong")
		return
	}
	newHash, err := hashPassword(b.New)
	if err == nil {
		_, err = s.db.Exec(r.Context(), `UPDATE users SET password_hash = $2 WHERE id = $1`, id.Owner, newHash)
	}
	if err != nil {
		s.log.Error("change password", "err", err)
		writeError(w, http.StatusInternalServerError, "internal error")
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *Service) listUsers(w http.ResponseWriter, r *http.Request) {
	if _, ok := s.caller(w, r, true); !ok {
		return
	}
	rows, _ := s.db.Query(r.Context(), `SELECT id, email, admin, created_at FROM users ORDER BY email`)
	users, err := pgx.CollectRows(rows, func(row pgx.CollectableRow) (u user, err error) {
		err = row.Scan(&u.ID, &u.Email, &u.Admin, &u.CreatedAt)
		return u, err
	})
	if err != nil {
		s.log.Error("list users", "err", err)
		writeError(w, http.StatusInternalServerError, "internal error")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"users": users})
}

func (s *Service) deleteUser(w http.ResponseWriter, r *http.Request) {
	me, ok := s.caller(w, r, true)
	if !ok {
		return
	}
	target, err := uuid.Parse(r.PathValue("id"))
	if err != nil {
		writeError(w, http.StatusNotFound, "user not found")
		return
	}
	if target == me.Owner {
		writeError(w, http.StatusConflict, "you can't delete your own account")
		return
	}
	tag, err := s.db.Exec(r.Context(), `DELETE FROM users WHERE id = $1`, target)
	switch {
	case err != nil:
		s.log.Error("delete user", "err", err)
		writeError(w, http.StatusInternalServerError, "internal error")
	case tag.RowsAffected() == 0:
		writeError(w, http.StatusNotFound, "user not found")
	default:
		w.WriteHeader(http.StatusNoContent)
	}
}

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
