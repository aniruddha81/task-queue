// Package gateway is the only public entry point. It routes to auth and jobs, checks the
// JWT and a per-user rate limit before anything reaches jobs, caps request bodies, turns
// the browser's session cookie into a bearer token (after a CSRF check), and serves the
// dashboard's static files. /v1/public/ (the live page) needs no token; starting a demo job
// there is rate-limited per address and overall instead.
package gateway

import (
	"log/slog"
	"net"
	"net/http"
	"net/http/httputil"
	"net/url"
	"slices"
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
	Origins    []string     // the dashboard's origins, e.g. https://localhost:8443; cookie writes must come from one
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
	visitors   map[string]*rate.Limiter // by address, for demo jobs
	demos      *rate.Limiter            // every visitor's demo jobs together
}

func New(cfg Config) http.Handler {
	g := &gateway{cfg: cfg, auth: proxy(cfg.Auth, cfg), jobs: proxy(cfg.Jobs, cfg), limiters: map[uuid.UUID]*rate.Limiter{},
		visitors: map[string]*rate.Limiter{}, demos: rate.NewLimiter(0.5, 10)}
	mux := http.NewServeMux()
	mux.Handle("/v1/auth/", g.auth) // login, logout and the API key exchange: no token yet
	// Account routes act as the caller, so a browser session's cookie becomes the token here.
	for _, p := range []string{"/v1/auth/me", "/v1/auth/password", "/v1/auth/users", "/v1/auth/users/", "/v1/auth/keys", "/v1/auth/keys/"} {
		mux.Handle(p, g.authed(g.auth))
	}
	mux.Handle("GET /.well-known/jwks.json", g.auth)
	mux.Handle("/v1/", g.authed(g.jobs)) // everything under /v1 except /v1/auth/ and /v1/public/
	mux.Handle("GET /v1/public/", g.jobs)
	mux.Handle("POST /v1/public/try", g.demo(g.jobs))
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
				if o := r.Header.Get("Origin"); !safeMethod(r.Method) && (o == "" || !slices.Contains(g.cfg.Origins, o)) {
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

// demo admits a visitor's demo job within a budget for their address and one for everyone:
// each takes a worker slot, a frozen payment for 10 s, so all of them together are capped.
func (g *gateway) demo(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ip, _, _ := net.SplitHostPort(r.RemoteAddr) // Traffic Manager is DNS only: this is the client
		g.mu.Lock()
		if len(g.visitors) > 10000 {
			// ponytail: forgetting everyone at once resets their budgets; an LRU if that is ever abused.
			clear(g.visitors)
		}
		l, ok := g.visitors[ip]
		if !ok {
			l = rate.NewLimiter(rate.Every(3*time.Second), 5)
			g.visitors[ip] = l
		}
		g.mu.Unlock()
		if !l.Allow() || !g.demos.Allow() {
			w.Header().Set("Retry-After", "3")
			writeError(w, http.StatusTooManyRequests, "lots of demos running; try again in a few seconds")
			return
		}
		next.ServeHTTP(w, r)
	})
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
