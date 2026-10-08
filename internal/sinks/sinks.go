// Package sinks simulates external systems that make a job's effect happen once by
// checking its effect key (G4):
//
//	POST /ledger/transfer {from, to, amount}  moves credits between accounts
//	POST /webhook         any JSON            fails at random, before or after recording
//	POST /digest          {}                  stores one digest per cron tick
//
// Every request carries the effect key in the Idempotency-Key header. A repeat returns
// 200 with "applied": false, and the worker treats that as success.
package sinks

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"math/rand/v2"
	"net/http"
	"strconv"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/aniruddha81/task-queue/internal/mutant"
)

type Server struct {
	db  *pgxpool.Pool
	log *slog.Logger
	// Probabilities that a webhook call fails before recording anything, or after recording.
	FailBefore, FailAfter float64
}

func New(db *pgxpool.Pool, log *slog.Logger) *Server {
	return &Server{db: db, log: log, FailBefore: 0.15, FailAfter: 0.15}
}

func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("POST /ledger/transfer", s.transfer)
	mux.HandleFunc("POST /webhook", s.webhook)
	mux.HandleFunc("POST /digest", s.digest)
	return mux
}

func (s *Server) transfer(w http.ResponseWriter, r *http.Request) {
	var t struct {
		From, To string
		Amount   int64
	}
	if err := json.NewDecoder(r.Body).Decode(&t); err != nil || t.Amount <= 0 || t.From == "" || t.To == "" {
		http.Error(w, `{"error":"want {from, to, amount > 0}"}`, http.StatusBadRequest)
		return
	}
	s.apply(w, r, "ledger", false, `INSERT INTO ledger_entries (dedupe_key, effect_key, from_account, to_account, amount)
		VALUES ($1, $2, $3, $4, $5) ON CONFLICT (dedupe_key) DO NOTHING`,
		[]any{t.From, t.To, t.Amount},
		func(ctx context.Context, tx pgx.Tx) error {
			_, err := tx.Exec(ctx, `INSERT INTO ledger_accounts (account, balance) VALUES ($1, -$3::bigint), ($2, $3::bigint)
				ON CONFLICT (account) DO UPDATE SET balance = ledger_accounts.balance + EXCLUDED.balance`,
				t.From, t.To, t.Amount)
			return err
		})
}

func (s *Server) webhook(w http.ResponseWriter, r *http.Request) {
	body, err := io.ReadAll(http.MaxBytesReader(w, r.Body, 64<<10))
	if err != nil || !json.Valid(body) {
		http.Error(w, `{"error":"want a JSON body"}`, http.StatusBadRequest)
		return
	}
	if rand.Float64() < s.FailBefore {
		s.record(r.Context(), "webhook", r.Header.Get("Idempotency-Key"), "failed_before")
		http.Error(w, `{"error":"receiver unavailable"}`, http.StatusServiceUnavailable)
		return
	}
	s.apply(w, r, "webhook", rand.Float64() < s.FailAfter, `INSERT INTO webhook_deliveries (dedupe_key, effect_key, body)
		VALUES ($1, $2, $3::jsonb) ON CONFLICT (dedupe_key) DO NOTHING`, []any{string(body)}, nil)
}

func (s *Server) digest(w http.ResponseWriter, r *http.Request) {
	s.apply(w, r, "digest", false, `INSERT INTO digests (dedupe_key, effect_key) VALUES ($1, $2)
		ON CONFLICT (dedupe_key) DO NOTHING`, nil, nil)
}

// apply records the effect key and applies the effect in one transaction, so an effect
// and its key are stored together or not at all. A key already present means the effect
// already happened: nothing is applied, and the call still succeeds. With failAfter, the
// call is recorded and then reported as failed, so the caller retries into a duplicate.
func (s *Server) apply(w http.ResponseWriter, r *http.Request, sink string, failAfter bool, insert string,
	args []any, effect func(context.Context, pgx.Tx) error) {
	key := r.Header.Get("Idempotency-Key")
	if key == "" {
		http.Error(w, `{"error":"Idempotency-Key header required"}`, http.StatusBadRequest)
		return
	}
	dedupe := key
	if mutant.NoEffectKey { // broken on purpose: every call looks new
		dedupe = key + ":" + strconv.FormatUint(rand.Uint64(), 16)
	}
	ctx := r.Context()
	applied, err := func() (bool, error) {
		tx, err := s.db.Begin(ctx)
		if err != nil {
			return false, err
		}
		defer tx.Rollback(context.Background())
		tag, err := tx.Exec(ctx, insert, append([]any{dedupe, key}, args...)...)
		if err != nil {
			return false, err
		}
		applied := tag.RowsAffected() == 1
		if applied && effect != nil {
			if err := effect(ctx, tx); err != nil {
				return false, err
			}
		}
		result := "duplicate"
		switch {
		case failAfter:
			result = "failed_after"
		case applied:
			result = "applied"
		}
		if _, err := tx.Exec(ctx, `INSERT INTO sink_calls (sink, effect_key, result) VALUES ($1, $2, $3)`,
			sink, key, result); err != nil {
			return false, err
		}
		return applied, tx.Commit(ctx)
	}()
	switch {
	case err != nil:
		s.log.Error("sink", "sink", sink, "err", err)
		http.Error(w, `{"error":"sink error"}`, http.StatusServiceUnavailable)
	case failAfter:
		http.Error(w, `{"error":"timed out after recording"}`, http.StatusServiceUnavailable)
	default:
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]bool{"applied": applied})
	}
}

func (s *Server) record(ctx context.Context, sink, key, result string) {
	if _, err := s.db.Exec(ctx, `INSERT INTO sink_calls (sink, effect_key, result) VALUES ($1, $2, $3)`,
		sink, key, result); err != nil {
		s.log.Warn("record sink call", "err", err)
	}
}
