package cache_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/go-core-fx/cachefx/cache"
)

// TestMemoryCache_GetEvictsExpiredEntry verifies that reading an expired key
// removes the entry from the cache rather than leaving it in place.
//
// Expired entries used to remain in the map until an explicit Cleanup or
// Drain, which no caller is required to make: a workload that inserts many
// short-TTL keys and rarely re-reads them grew the map without bound.
func TestMemoryCache_GetEvictsExpiredEntry(t *testing.T) {
	t.Parallel()

	c := cache.NewMemory(0)
	ctx := context.Background()

	if err := c.Set(ctx, "key", []byte("value"), cache.WithTTL(time.Millisecond)); err != nil {
		t.Fatalf("Set failed: %v", err)
	}

	// Wait for expiration
	time.Sleep(5 * time.Millisecond)

	if _, err := c.Get(ctx, "key"); !errors.Is(err, cache.ErrKeyNotFound) {
		t.Fatalf("Expected ErrKeyNotFound, got %v", err)
	}

	// The entry must be gone, not merely invisible: a Drain on the same cache
	// returns only live items, so an extra key would still occupy memory.
	items, err := c.Drain(ctx)
	if err != nil {
		t.Fatalf("Drain failed: %v", err)
	}

	if len(items) != 0 {
		t.Errorf("Expected cache to be empty after expired Get, got %d items", len(items))
	}
}

// TestMemoryCache_CleanupIsNoOpAfterExpiredGet verifies that Get already did
// the work Cleanup would have done.
func TestMemoryCache_CleanupIsNoOpAfterExpiredGet(t *testing.T) {
	t.Parallel()

	c := cache.NewMemory(0)
	ctx := context.Background()

	if err := c.Set(ctx, "key", []byte("value"), cache.WithTTL(time.Millisecond)); err != nil {
		t.Fatalf("Set failed: %v", err)
	}

	time.Sleep(5 * time.Millisecond)

	if _, err := c.Get(ctx, "key"); !errors.Is(err, cache.ErrKeyNotFound) {
		t.Fatalf("Expected ErrKeyNotFound, got %v", err)
	}

	if err := c.Cleanup(ctx); err != nil {
		t.Fatalf("Cleanup failed: %v", err)
	}

	items, err := c.Drain(ctx)
	if err != nil {
		t.Fatalf("Drain failed: %v", err)
	}

	if len(items) != 0 {
		t.Errorf("Expected cache to be empty, got %d items", len(items))
	}
}

// TestMemoryCache_ConcurrentExpiredGetWithSet verifies that the write lock
// taken to evict an expired entry does not deadlock or race with concurrent
// writers on the same key, and that the eviction drops only the entry it read:
// the writers below store a non-expiring value, so a stale Get deleting their
// replacement would be visible in the final state.
func TestMemoryCache_ConcurrentExpiredGetWithSet(t *testing.T) {
	t.Parallel()

	c := cache.NewMemory(0)
	ctx := context.Background()

	if err := c.Set(ctx, "key", []byte("value"), cache.WithTTL(time.Millisecond)); err != nil {
		t.Fatalf("Set failed: %v", err)
	}

	time.Sleep(5 * time.Millisecond)

	const numGoroutines = 50
	done := make(chan struct{})

	for i := range numGoroutines {
		go func(id int) {
			defer func() { done <- struct{}{} }()

			if id%2 == 0 {
				_, _ = c.Get(ctx, "key")
				return
			}

			_ = c.Set(ctx, "key", []byte("value"))
		}(i)
	}

	for range numGoroutines {
		<-done
	}

	if got, err := c.Get(ctx, "key"); err != nil || string(got) != "value" {
		t.Errorf("Expected concurrent Set value to survive, got %q, err %v", got, err)
	}
}

// TestMemoryCache_ManyExpiredGetsMemoryIsReclaimed verifies that repeatedly
// inserting and reading expired entries does not grow the cache.
func TestMemoryCache_ManyExpiredGetsMemoryIsReclaimed(t *testing.T) {
	t.Parallel()

	c := cache.NewMemory(0)
	ctx := context.Background()

	for i := range 1000 {
		key := "key-" + time.Duration(i).String()

		if err := c.Set(ctx, key, []byte("value"), cache.WithTTL(time.Microsecond)); err != nil {
			t.Fatalf("Set failed: %v", err)
		}
	}

	time.Sleep(5 * time.Millisecond)

	for i := range 1000 {
		_, _ = c.Get(ctx, "key-"+time.Duration(i).String())
	}

	items, err := c.Drain(ctx)
	if err != nil {
		t.Fatalf("Drain failed: %v", err)
	}

	if len(items) != 0 {
		t.Errorf("Expected cache to be empty after evicting all expired entries, got %d items", len(items))
	}
}
