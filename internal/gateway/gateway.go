// Package gateway is the only public entry point. It routes to auth and jobs, checks the
// JWT and a per-user rate limit before anything reaches jobs, caps request bodies, turns
// the browser's session cookie into a bearer token (after a CSRF check), and serves the
// dashboard's static files.
package gateway

import (
	"log/slog"
	"net/http"
	"net/http/httputil"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"time"
	"uuid"

	"golang.org/x/time/rate"

	"github.com/aniruddha81/task-queue/internal/auth"
	"github.com/aniruddha81/task-queue/internal/authn"
)

const maxBody = 64 << 10

type Config struct {
	Auth, Jobs []*url.URL        // upstreams, nearest first; the rest are tried when one is down
	Transport  http.RoundTripper // mTLS to the upstreams
	Verifier   *authn.Verifier
	Origin     string       // the dashboard's origin, e.g. https://localhost:8443; cookie writes must come from it
	Static     http.Handler // the dashboard
	Rate       rate.Limit   // requests per second per user
	Burst      int
	Log        *slog.Logger
}

type gateway struct {
	cfg        Config
	auth, jobs *httputil.ReverseProxy
	mu         sync.Mutex
	limiters   map[uuid.UUID]*rate.Limiter
}

func New(cfg Config) http.Handler {
	g := &gateway{cfg: cfg, auth: proxy(cfg.Auth, cfg), jobs: proxy(cfg.Jobs, cfg), limiters: map[uuid.UUID]*rate.Limiter{}}
	mux := http.NewServeMux()
	mux.Handle("/v1/auth/", g.auth)
	mux.Handle("GET /.well-known/jwks.json", g.auth)
	mux.Handle("/v1/", g.authed(g.jobs)) // everything under /v1 except /v1/auth/
	mux.Handle("/", http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != "GET" && r.Method != "HEAD" {
			writeError(w, http.StatusMethodNotAllowed, "method not allowed")
			return
		}
		cfg.Static.ServeHTTP(w, r)
	}))
	return headers(limitBody(mux))
}

func proxy(targets []*url.URL, cfg Config) *httputil.ReverseProxy {
	next := cfg.Transport
	if next == nil {
		next = http.DefaultTransport
	}
	return &httputil.ReverseProxy{
		Rewrite: func(r *httputil.ProxyRequest) {
			r.SetURL(targets[0])
			r.SetXForwarded()
			r.Out.Header.Del("Cookie") // upstreams authenticate by bearer token only
		},
		Transport: failover{next, targets},
		ErrorHandler: func(w http.ResponseWriter, r *http.Request, err error) {
			cfg.Log.Warn("upstream", "path", r.URL.Path, "err", err)
			http.Error(w, `{"error":"upstream unavailable"}`, http.StatusBadGateway)
		},
	}
}

// authed admits a request only with a valid token, within the caller's rate limit.
func (g *gateway) authed(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") == "" {
			if c, err := r.Cookie(auth.CookieName); err == nil {
				// A browser session. SameSite=Strict already stops cross-site sends; the
				// Origin check is a second guard for anything that changes state.
				if !safeMethod(r.Method) && r.Header.Get("Origin") != g.cfg.Origin {
					writeError(w, http.StatusForbidden, "cross-origin request refused")
					return
				}
				r.Header.Set("Authorization", "Bearer "+c.Value)
			}
		}
		id, err := g.cfg.Verifier.FromRequest(r)
		if err != nil {
			writeError(w, http.StatusUnauthorized, "invalid or missing token")
			return
		}
		if !g.limiter(id.Owner).Allow() {
			w.Header().Set("Retry-After", "1")
			writeError(w, http.StatusTooManyRequests, "rate limit exceeded")
			return
		}
		next.ServeHTTP(w, r)
	})
}

func (g *gateway) limiter(owner uuid.UUID) *rate.Limiter {
	g.mu.Lock()
	defer g.mu.Unlock()
	l, ok := g.limiters[owner]
	if !ok {
		// ponytail: per-instance and never evicted; bounded by the number of users, who are
		// seeded (registration is admin-only). Two gateways allow up to twice the rate.
		l = rate.NewLimiter(g.cfg.Rate, g.cfg.Burst)
		g.limiters[owner] = l
	}
	return l
}

func safeMethod(m string) bool { return m == "GET" || m == "HEAD" || m == "OPTIONS" }

func limitBody(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.ContentLength > maxBody {
			writeError(w, http.StatusRequestEntityTooLarge, "body larger than 64 KB")
			return
		}
		r.Body = http.MaxBytesReader(w, r.Body, maxBody)
		next.ServeHTTP(w, r)
	})
}

func headers(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		h := w.Header()
		h.Set("Strict-Transport-Security", "max-age="+strconv.Itoa(int((365*24*time.Hour).Seconds())))
		h.Set("X-Content-Type-Options", "nosniff")
		h.Set("X-Frame-Options", "DENY")
		h.Set("Referrer-Policy", "no-referrer")
		next.ServeHTTP(w, r)
	})
}

func writeError(w http.ResponseWriter, status int, msg string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	w.Write([]byte(`{"error":"` + strings.ReplaceAll(msg, `"`, `'`) + `"}`))
}
