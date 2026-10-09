package auth

import (
	"errors"
	"io"
	"net/http"

	"github.com/jackc/pgx/v5"
)

// The gateways' shared ACME cache (autocert.Cache over HTTP), for the gateway's mTLS
// certificate only:
//
//	GET    /internal/acme/{key}   200 with the bytes, or 404 with X-Acme-Cache: miss
//	PUT    /internal/acme/{key}   stores the body
//	DELETE /internal/acme/{key}
func (s *Service) acme(w http.ResponseWriter, r *http.Request) {
	if r.TLS == nil || len(r.TLS.PeerCertificates) == 0 || r.TLS.PeerCertificates[0].Subject.CommonName != "gateway" {
		http.Error(w, "gateway only", http.StatusForbidden)
		return
	}
	key := r.PathValue("key")
	var err error
	switch r.Method {
	case "GET":
		var data []byte
		err = s.db.QueryRow(r.Context(), `SELECT data FROM acme_cache WHERE key = $1`, key).Scan(&data)
		if errors.Is(err, pgx.ErrNoRows) {
			// Marked, so a caller can't mistake an auth without this route (mid-deploy) for a miss.
			w.Header().Set("X-Acme-Cache", "miss")
			http.Error(w, "miss", http.StatusNotFound)
			return
		}
		if err == nil {
			w.Write(data)
			return
		}
	case "PUT":
		var data []byte
		if data, err = io.ReadAll(http.MaxBytesReader(w, r.Body, 64<<10)); err == nil {
			_, err = s.db.Exec(r.Context(), `INSERT INTO acme_cache (key, data) VALUES ($1, $2)
				ON CONFLICT (key) DO UPDATE SET data = excluded.data, updated_at = now()`, key, data)
		}
	case "DELETE":
		_, err = s.db.Exec(r.Context(), `DELETE FROM acme_cache WHERE key = $1`, key)
	}
	if err != nil {
		s.log.Error("acme cache", "key", key, "err", err)
		http.Error(w, "cache unavailable", http.StatusServiceUnavailable)
	}
}
