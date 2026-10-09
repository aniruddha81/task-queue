package main

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"

	"golang.org/x/crypto/acme/autocert"
)

// sharedCache is autocert's cache, shared by every gateway through auth (which keeps it in
// its database), so both gateways serve one certificate and either can answer Let's
// Encrypt's validation: autocert looks up the challenge certificate in the cache. This
// gateway's disk keeps a copy, so an auth or database outage never takes TLS down; and a
// certificate found only on disk (from a gateway that cached alone) is shared, not reissued.
type sharedCache struct {
	local  autocert.DirCache
	auth   []*url.URL // nearest first
	client *http.Client
}

func (c sharedCache) Get(ctx context.Context, key string) ([]byte, error) {
	data, err := c.remote(ctx, "GET", key, nil)
	if err == nil {
		c.local.Put(ctx, key, data)
		return data, nil
	}
	local, lerr := c.local.Get(ctx, key)
	if lerr == nil && errors.Is(err, autocert.ErrCacheMiss) {
		c.remote(ctx, "PUT", key, local) // best effort: the next Get tries again
	}
	return local, lerr
}

func (c sharedCache) Put(ctx context.Context, key string, data []byte) error {
	err := c.local.Put(ctx, key, data)
	if _, rerr := c.remote(ctx, "PUT", key, data); err == nil {
		err = rerr
	}
	return err
}

func (c sharedCache) Delete(ctx context.Context, key string) error {
	c.remote(ctx, "DELETE", key, nil)
	return c.local.Delete(ctx, key)
}

// remote tries each auth in turn. A 404 is an answer (ErrCacheMiss), not a failure.
func (c sharedCache) remote(ctx context.Context, method, key string, body []byte) ([]byte, error) {
	err := errors.New("no auth upstream")
	for _, u := range c.auth {
		req, rerr := http.NewRequestWithContext(ctx, method, u.JoinPath("/internal/acme", key).String(), bytes.NewReader(body))
		if rerr != nil {
			return nil, rerr
		}
		resp, derr := c.client.Do(req)
		if derr != nil {
			err = derr
			continue
		}
		data, _ := io.ReadAll(resp.Body)
		resp.Body.Close()
		switch {
		case resp.StatusCode == http.StatusNotFound:
			return nil, autocert.ErrCacheMiss
		case resp.StatusCode == http.StatusOK:
			return data, nil
		}
		err = fmt.Errorf("acme cache %s: %s", method, resp.Status)
	}
	return nil, err
}
