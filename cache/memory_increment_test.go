package cache_test

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/go-core-fx/cachefx/cache"
)

func TestMemoryCache_IncrementCreatesMissingKey(t *testing.T) {
	t.Parallel()

	c := cache.NewMemory(0)

	ctx := context.Background()
	key := "views"

	n, err := c.Increment(ctx, key, 1)
	if err != nil {
		t.Fatalf("Increment failed: %v", err)
	}

	if n != 1 {
		t.Errorf("Expected 1, got %d", n)
	}

	retrieved, err := c.Get(ctx, key)
	if err != nil {
		t.Fatalf("Get failed: %v", err)
	}

	if string(retrieved) != "1" {
		t.Errorf("Expected 1, got %s", retrieved)
	}
}

func TestMemoryCache_IncrementAccumulates(t *testing.T) {
	t.Parallel()

	c := cache.NewMemory(0)

	ctx := context.Background()
	key := "quota"

	steps := []struct {
		delta    int64
		expected int64
	}{
		{5, 5},
		{3, 8},
		{-8, 0},
		{-1, -1},
	}

	for _, step := range steps {
		n, err := c.Increment(ctx, key, step.delta)
		if err != nil {
			t.Fatalf("Increment(%d) failed: %v", step.delta, err)
		}

		if n != step.expected {
			t.Errorf("Increment(%d): expected %d, got %d", step.delta, step.expected, n)
		}
	}

	retrieved, err := c.Get(ctx, key)
	if err != nil {
		t.Fatalf("Get failed: %v", err)
	}

	if string(retrieved) != "-1" {
		t.Errorf("Expected -1, got %s", retrieved)
	}
}

func TestMemoryCache_IncrementAppliesDefaultTTLAndRecreatesExpired(t *testing.T) {
	t.Parallel()

	c := cache.NewMemory(5 * time.Millisecond)

	ctx := context.Background()
	key := "short-lived"

	n, err := c.Increment(ctx, key, 1)
	if err != nil {
		t.Fatalf("Increment failed: %v", err)
	}

	if n != 1 {
		t.Errorf("Expected 1, got %d", n)
	}

	time.Sleep(15 * time.Millisecond)

	if _, err = c.Get(ctx, key); !errors.Is(err, cache.ErrKeyNotFound) {
		t.Errorf("Expected ErrKeyNotFound after TTL elapsed, got %v", err)
	}

	n, err = c.Increment(ctx, key, 1)
	if err != nil {
		t.Fatalf("Increment after expiration failed: %v", err)
	}

	if n != 1 {
		t.Errorf("Expected expired counter to be recreated with 1, got %d", n)
	}
}

func TestMemoryCache_IncrementCreateWithTTLOption(t *testing.T) {
	t.Parallel()

	c := cache.NewMemory(0)

	ctx := context.Background()
	key := "one-shot"

	if _, err := c.Increment(ctx, key, 1, cache.WithTTL(5*time.Millisecond)); err != nil {
		t.Fatalf("Increment failed: %v", err)
	}

	time.Sleep(15 * time.Millisecond)

	if _, err := c.Get(ctx, key); !errors.Is(err, cache.ErrKeyNotFound) {
		t.Errorf("Expected ErrKeyNotFound after option TTL elapsed, got %v", err)
	}
}

func TestMemoryCache_IncrementIgnoresOptionsOnExistingKey(t *testing.T) {
	t.Parallel()

	c := cache.NewMemory(time.Hour)

	ctx := context.Background()
	key := "long-lived"

	if _, err := c.Increment(ctx, key, 1); err != nil {
		t.Fatalf("Increment failed: %v", err)
	}

	if n, err := c.Increment(ctx, key, 1, cache.WithTTL(5*time.Millisecond)); err != nil {
		t.Fatalf("Increment failed: %v", err)
	} else if n != 2 {
		t.Errorf("Expected 2, got %d", n)
	}

	time.Sleep(15 * time.Millisecond)

	retrieved, err := c.Get(ctx, key)
	if err != nil {
		t.Fatalf("Get failed: the TTL option must be ignored for an existing key: %v", err)
	}

	if string(retrieved) != "2" {
		t.Errorf("Expected 2, got %s", retrieved)
	}
}

func TestMemoryCache_IncrementPreservesExistingTTL(t *testing.T) {
	t.Parallel()

	c := cache.NewMemory(5 * time.Millisecond)

	ctx := context.Background()
	key := "explicit-ttl"

	if err := c.Set(ctx, key, []byte("41"), cache.WithTTL(time.Hour)); err != nil {
		t.Fatalf("Set failed: %v", err)
	}

	// Stay past the cache default TTL so an Increment that re-applied it
	// would let the counter expire in the next sleep.
	time.Sleep(15 * time.Millisecond)

	if n, err := c.Increment(ctx, key, 1); err != nil {
		t.Fatalf("Increment failed: %v", err)
	} else if n != 42 {
		t.Errorf("Expected 42, got %d", n)
	}

	time.Sleep(15 * time.Millisecond)

	retrieved, err := c.Get(ctx, key)
	if err != nil {
		t.Fatalf("Get failed: the original TTL must survive Increment: %v", err)
	}

	if string(retrieved) != "42" {
		t.Errorf("Expected 42, got %s", retrieved)
	}
}

func TestMemoryCache_IncrementRejectsNonCanonicalValues(t *testing.T) {
	t.Parallel()

	c := cache.NewMemory(0)

	ctx := context.Background()

	for _, value := range []string{"abc", "007", "+7", " 7", ""} {
		key := "counter:" + value
		if err := c.Set(ctx, key, []byte(value)); err != nil {
			t.Fatalf("Set(%q) failed: %v", value, err)
		}

		if _, err := c.Increment(ctx, key, 1); !errors.Is(err, cache.ErrNotAnInteger) {
			t.Errorf("Increment on %q: expected ErrNotAnInteger, got %v", value, err)
		}

		retrieved, err := c.Get(ctx, key)
		if err != nil {
			t.Fatalf("Get failed: %v", err)
		}

		if string(retrieved) != value {
			t.Errorf("Increment on %q must leave the value unchanged, got %q", value, retrieved)
		}
	}
}

func TestMemoryCache_IncrementOverflow(t *testing.T) {
	t.Parallel()

	c := cache.NewMemory(0)

	ctx := context.Background()

	if err := c.Set(ctx, "max", []byte("9223372036854775807")); err != nil {
		t.Fatalf("Set failed: %v", err)
	}

	if _, err := c.Increment(ctx, "max", 1); !errors.Is(err, cache.ErrCounterOverflow) {
		t.Errorf("Expected ErrCounterOverflow, got %v", err)
	}

	retrieved, err := c.Get(ctx, "max")
	if err != nil {
		t.Fatalf("Get failed: %v", err)
	}

	if string(retrieved) != "9223372036854775807" {
		t.Errorf("Overflow must leave the value unchanged, got %s", retrieved)
	}

	if err = c.Set(ctx, "min", []byte("-9223372036854775808")); err != nil {
		t.Fatalf("Set failed: %v", err)
	}

	if _, err = c.Increment(ctx, "min", -1); !errors.Is(err, cache.ErrCounterOverflow) {
		t.Errorf("Expected ErrCounterOverflow, got %v", err)
	}
}

func TestMemoryCache_IncrementConcurrent(t *testing.T) {
	t.Parallel()

	c := cache.NewMemory(0)

	ctx := context.Background()
	key := "views"

	const numGoroutines = 100
	const incrementsPerGoroutine = 100

	var wg sync.WaitGroup

	for range numGoroutines {
		wg.Add(1)
		go func() {
			defer wg.Done()

			for range incrementsPerGoroutine {
				if _, err := c.Increment(ctx, key, 1); err != nil {
					t.Errorf("Increment failed: %v", err)
					return
				}
			}
		}()
	}

	wg.Wait()

	retrieved, err := c.Get(ctx, key)
	if err != nil {
		t.Fatalf("Get failed: %v", err)
	}

	const expected = numGoroutines * incrementsPerGoroutine
	if string(retrieved) != "10000" {
		t.Errorf("Expected %d, got %s", expected, retrieved)
	}
}
