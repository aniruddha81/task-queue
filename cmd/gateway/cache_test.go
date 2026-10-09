package main

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"sync"
	"testing"

	"golang.org/x/crypto/acme/autocert"
)

func TestSharedCache(t *testing.T) {
	ctx := context.Background()
	var mu sync.Mutex
	store := map[string][]byte{}
	up := true
	auth := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		defer mu.Unlock()
		if !up {
			http.Error(w, "down", http.StatusServiceUnavailable)
			return
		}
		key := r.PathValue("key")
		if key == "" {
			key = r.URL.Path[len("/internal/acme/"):]
		}
		switch r.Method {
		case "GET":
			if b, ok := store[key]; ok {
				w.Write(b)
				return
			}
			http.NotFound(w, r)
		case "PUT":
			store[key], _ = io.ReadAll(r.Body)
		case "DELETE":
			delete(store, key)
		}
	}))
	defer auth.Close()
	u, _ := url.Parse(auth.URL)
	newCache := func() sharedCache {
		return sharedCache{local: autocert.DirCache(t.TempDir()), auth: []*url.URL{u}, client: auth.Client()}
	}

	a, b := newCache(), newCache()
	if _, err := a.Get(ctx, "cert"); err != autocert.ErrCacheMiss {
		t.Fatalf("empty: want ErrCacheMiss, got %v", err)
	}
	a.Put(ctx, "cert", []byte("v1"))
	if got, err := b.Get(ctx, "cert"); err != nil || string(got) != "v1" {
		t.Fatalf("the other gateway: want v1 from the shared cache, got %q, %v", got, err)
	}

	mu.Lock()
	up = false
	mu.Unlock()
	if got, err := b.Get(ctx, "cert"); err != nil || string(got) != "v1" {
		t.Fatalf("auth down: want the disk copy, got %q, %v", got, err)
	}

	// A certificate only on one gateway's disk (cached alone, before the shared cache) is
	// shared on the first lookup, instead of a second one being issued.
	mu.Lock()
	up = true
	mu.Unlock()
	old := newCache()
	old.local.Put(ctx, "legacy", []byte("on-disk"))
	if got, err := old.Get(ctx, "legacy"); err != nil || string(got) != "on-disk" {
		t.Fatalf("legacy: got %q, %v", got, err)
	}
	if got, err := newCache().Get(ctx, "legacy"); err != nil || string(got) != "on-disk" {
		t.Fatalf("legacy certificate wasn't shared: got %q, %v", got, err)
	}
}
