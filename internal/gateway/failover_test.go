package gateway

import (
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync/atomic"
	"testing"
)

func TestFailover(t *testing.T) {
	var hits atomic.Int32
	twin := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits.Add(1)
		b, _ := io.ReadAll(r.Body)
		w.Write(append([]byte("twin:"), b...))
	}))
	defer twin.Close()
	// A port with nothing listening: the connection is refused, so nothing was sent.
	l, _ := net.Listen("tcp", "127.0.0.1:0")
	down, _ := url.Parse("http://" + l.Addr().String())
	l.Close()
	// An upstream that takes the request and drops the connection without answering.
	dropper := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		c, _, _ := w.(http.Hijacker).Hijack()
		c.Close()
	}))
	defer dropper.Close()
	dropped, _ := url.Parse(dropper.URL)
	up, _ := url.Parse(twin.URL)

	do := func(first *url.URL, method string, key string) (string, error) {
		f := failover{http.DefaultTransport, []*url.URL{first, up}}
		req, _ := http.NewRequest(method, first.String()+"/v1/jobs", strings.NewReader(`{"n":1}`))
		if key != "" {
			req.Header.Set("Idempotency-Key", key)
		}
		resp, err := f.RoundTrip(req)
		if err != nil {
			return "", err
		}
		defer resp.Body.Close()
		b, _ := io.ReadAll(resp.Body)
		return string(b), nil
	}

	if got, err := do(down, "POST", ""); err != nil || got != `twin:{"n":1}` {
		t.Errorf("refused connection: want the twin with the same body, got %q, %v", got, err)
	}
	if got, err := do(dropped, "POST", "k1"); err != nil || got != `twin:{"n":1}` {
		t.Errorf("dropped keyed write: want the twin (the key makes a repeat safe), got %q, %v", got, err)
	}
	before := hits.Load()
	if _, err := do(dropped, "POST", ""); err == nil || hits.Load() != before {
		t.Errorf("dropped unkeyed write: it may have run, so it must not be repeated (twin hits %d → %d)", before, hits.Load())
	}
}
