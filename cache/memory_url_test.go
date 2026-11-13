package cache_test

import (
	"context"
	"errors"
	"testing"

	"github.com/go-core-fx/cachefx/cache"
)

// TestNewMemoryURL_ParsesMaxEntries checks that the memory backend reads the
// entry cap from the URL it is handed.
func TestNewMemoryURL_ParsesMaxEntries(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	c, err := cache.NewMemoryURL("memory://?max_entries=2")
	if err != nil {
		t.Fatalf("NewMemoryURL failed: %v", err)
	}

	for _, key := range []string{"k1", "k2", "k3"} {
		if err = c.Set(ctx, key, []byte(key)); err != nil {
			t.Fatalf("Set(%q) failed: %v", key, err)
		}
	}

	if _, err = c.Get(ctx, "k1"); !errors.Is(err, cache.ErrKeyNotFound) {
		t.Errorf("Get(k1) error = %v, want ErrKeyNotFound: the cap must evict the oldest key", err)
	}
	if _, err = c.Get(ctx, "k3"); err != nil {
		t.Errorf("Get(k3) failed: %v", err)
	}
}

// TestNewMemoryURL_UnboundedWithoutParameter checks that a bare memory:// URL
// keeps the default unbounded behavior.
func TestNewMemoryURL_UnboundedWithoutParameter(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	c, err := cache.NewMemoryURL("memory://")
	if err != nil {
		t.Fatalf("NewMemoryURL failed: %v", err)
	}

	for _, key := range []string{"k1", "k2", "k3"} {
		if err = c.Set(ctx, key, []byte(key)); err != nil {
			t.Fatalf("Set(%q) failed: %v", key, err)
		}
		if _, err = c.Get(ctx, key); err != nil {
			t.Errorf("Get(%q) failed: %v", key, err)
		}
	}
}

// TestNewMemoryURL_RejectsInvalid checks that a malformed URL fails with
// ErrInvalidConfig instead of silently producing an unbounded cache.
func TestNewMemoryURL_RejectsInvalid(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name string
		url  string
	}{
		{name: "wrong scheme", url: "redis://?max_entries=2"},
		{name: "not a number", url: "memory://?max_entries=abc"},
		{name: "negative", url: "memory://?max_entries=-1"},
		{name: "empty value", url: "memory://?max_entries="},
		{name: "unparseable", url: "memory://\x00"},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			if _, err := cache.NewMemoryURL(tc.url); !errors.Is(err, cache.ErrInvalidConfig) {
				t.Fatalf("NewMemoryURL(%q) error = %v, want ErrInvalidConfig", tc.url, err)
			}
		})
	}
}
