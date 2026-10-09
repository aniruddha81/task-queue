package gateway

import (
	"bytes"
	"errors"
	"io"
	"net"
	"net/http"
	"net/url"
)

// failover sends a request to the first upstream, and to the next one (its twin in the
// other cloud) when that fails without a response: always when the connection couldn't be
// made, so nothing was sent; otherwise only when repeating the request is safe, because it
// reads or carries an Idempotency-Key that makes a repeat return the original result.
type failover struct {
	next      http.RoundTripper
	upstreams []*url.URL // nearest first
}

func (f failover) RoundTrip(r *http.Request) (*http.Response, error) {
	var body []byte
	if r.Body != nil && r.Body != http.NoBody && len(f.upstreams) > 1 {
		b, err := io.ReadAll(r.Body) // at most 64 KB: limitBody runs first
		r.Body.Close()
		if err != nil {
			return nil, err
		}
		body = b
	}
	var err error
	for i, u := range f.upstreams {
		req := r
		if i > 0 || body != nil {
			req = r.Clone(r.Context())
			req.URL.Scheme, req.URL.Host, req.Host = u.Scheme, u.Host, ""
			if body != nil {
				req.Body = io.NopCloser(bytes.NewReader(body))
			}
		}
		var resp *http.Response
		if resp, err = f.next.RoundTrip(req); err == nil {
			return resp, nil
		}
		if r.Context().Err() != nil || !(notSent(err) || replayable(r)) {
			return nil, err
		}
	}
	return nil, err
}

// notSent: the connection was never made, so the upstream saw nothing.
func notSent(err error) bool {
	var op *net.OpError
	return errors.As(err, &op) && op.Op == "dial"
}

func replayable(r *http.Request) bool {
	return r.Method == "GET" || r.Method == "HEAD" || r.Header.Get("Idempotency-Key") != ""
}
