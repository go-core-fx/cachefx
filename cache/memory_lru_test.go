package cache_test

import (
	"context"
	"errors"
	"slices"
	"sync"
	"testing"
	"time"

	"github.com/go-core-fx/cachefx/cache"
)

// requireLRUOrder fails the test when the cache's recency order does not
// match want, most recently used first.
func requireLRUOrder(t *testing.T, c *cache.MemoryCache, want ...string) {
	t.Helper()

	if got := c.LRUOrder(); !slices.Equal(got, want) {
		t.Fatalf("LRU order = %v, want %v", got, want)
	}
}

// TestMemoryCache_LRUEvictsLeastRecentlyUsedOnInsert verifies that inserting
// a key while at the cap evicts the least recently used entry and leaves the
// survivors in recency order.
func TestMemoryCache_LRUEvictsLeastRecentlyUsedOnInsert(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	c := cache.NewMemory(0, cache.WithMaxEntries(3))

	for _, key := range []string{"a", "b", "c", "d", "e"} {
		if err := c.Set(ctx, key, []byte(key)); err != nil {
			t.Fatalf("Set(%q) failed: %v", key, err)
		}
	}

	requireLRUOrder(t, c, "e", "d", "c")

	for _, key := range []string{"a", "b"} {
		if _, err := c.Get(ctx, key); !errors.Is(err, cache.ErrKeyNotFound) {
			t.Errorf("Get(%q) error = %v, want ErrKeyNotFound", key, err)
		}
	}

	for _, key := range []string{"c", "d", "e"} {
		if _, err := c.Get(ctx, key); err != nil {
			t.Errorf("Get(%q) failed: %v", key, err)
		}
	}
}

// TestMemoryCache_LRUGetHitProtectsEntry verifies that reading a key marks
// it recently used, so a later insertion evicts another entry instead.
func TestMemoryCache_LRUGetHitProtectsEntry(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	c := cache.NewMemory(0, cache.WithMaxEntries(2))

	if err := c.Set(ctx, "k1", []byte("v1")); err != nil {
		t.Fatalf("Set(k1) failed: %v", err)
	}
	if err := c.Set(ctx, "k2", []byte("v2")); err != nil {
		t.Fatalf("Set(k2) failed: %v", err)
	}

	if _, err := c.Get(ctx, "k1"); err != nil {
		t.Fatalf("Get(k1) failed: %v", err)
	}
	requireLRUOrder(t, c, "k1", "k2")

	if err := c.Set(ctx, "k3", []byte("v3")); err != nil {
		t.Fatalf("Set(k3) failed: %v", err)
	}

	requireLRUOrder(t, c, "k3", "k1")
	if _, err := c.Get(ctx, "k2"); !errors.Is(err, cache.ErrKeyNotFound) {
		t.Errorf("Get(k2) error = %v, want ErrKeyNotFound", err)
	}
}

// TestMemoryCache_LRUIncrementProtectsEntry verifies that Increment marks an
// existing counter recently used.
func TestMemoryCache_LRUIncrementProtectsEntry(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	c := cache.NewMemory(0, cache.WithMaxEntries(2))

	if _, err := c.Increment(ctx, "k1", 1); err != nil {
		t.Fatalf("Increment(k1) failed: %v", err)
	}
	if _, err := c.Increment(ctx, "k2", 1); err != nil {
		t.Fatalf("Increment(k2) failed: %v", err)
	}
	requireLRUOrder(t, c, "k2", "k1")

	if n, err := c.Increment(ctx, "k1", 1); err != nil || n != 2 {
		t.Fatalf("Increment(k1) = %d, %v; want 2, nil", n, err)
	}
	requireLRUOrder(t, c, "k1", "k2")

	if err := c.Set(ctx, "k3", []byte("v3")); err != nil {
		t.Fatalf("Set(k3) failed: %v", err)
	}

	requireLRUOrder(t, c, "k3", "k1")
	if _, err := c.Get(ctx, "k2"); !errors.Is(err, cache.ErrKeyNotFound) {
		t.Errorf("Get(k2) error = %v, want ErrKeyNotFound", err)
	}
}

// TestMemoryCache_LRUSetDoesNotTouchEntry verifies the documented semantics
// that overwriting a present key keeps its recency position, so the
// overwritten key is evicted before an older untouched one.
func TestMemoryCache_LRUSetDoesNotTouchEntry(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	c := cache.NewMemory(0, cache.WithMaxEntries(2))

	if err := c.Set(ctx, "k1", []byte("v1")); err != nil {
		t.Fatalf("Set(k1) failed: %v", err)
	}
	if err := c.Set(ctx, "k2", []byte("v2")); err != nil {
		t.Fatalf("Set(k2) failed: %v", err)
	}

	// Overwriting k1 must not move it ahead of k2.
	if err := c.Set(ctx, "k1", []byte("v1-new")); err != nil {
		t.Fatalf("Set(k1) overwrite failed: %v", err)
	}
	requireLRUOrder(t, c, "k2", "k1")

	if err := c.Set(ctx, "k3", []byte("v3")); err != nil {
		t.Fatalf("Set(k3) failed: %v", err)
	}

	requireLRUOrder(t, c, "k3", "k2")
	if _, err := c.Get(ctx, "k1"); !errors.Is(err, cache.ErrKeyNotFound) {
		t.Errorf("Get(k1) error = %v, want ErrKeyNotFound", err)
	}
}

// TestMemoryCache_LRUOverwriteDoesNotEvict verifies that replacing a value
// under the cap changes no entries, because the entry count is unchanged.
func TestMemoryCache_LRUOverwriteDoesNotEvict(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	c := cache.NewMemory(0, cache.WithMaxEntries(2))

	if err := c.Set(ctx, "k1", []byte("v1")); err != nil {
		t.Fatalf("Set(k1) failed: %v", err)
	}
	if err := c.Set(ctx, "k2", []byte("v2")); err != nil {
		t.Fatalf("Set(k2) failed: %v", err)
	}
	if err := c.Set(ctx, "k1", []byte("v1-new")); err != nil {
		t.Fatalf("Set(k1) overwrite failed: %v", err)
	}

	requireLRUOrder(t, c, "k2", "k1")
	for _, key := range []string{"k1", "k2"} {
		if _, err := c.Get(ctx, key); err != nil {
			t.Errorf("Get(%q) failed: %v", key, err)
		}
	}
}

// TestMemoryCache_LRUDeleteFreesSlot verifies that Delete removes the key
// from the recency order, so the next insert needs no eviction.
func TestMemoryCache_LRUDeleteFreesSlot(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	c := cache.NewMemory(0, cache.WithMaxEntries(2))

	if err := c.Set(ctx, "k1", []byte("v1")); err != nil {
		t.Fatalf("Set(k1) failed: %v", err)
	}
	if err := c.Set(ctx, "k2", []byte("v2")); err != nil {
		t.Fatalf("Set(k2) failed: %v", err)
	}
	if err := c.Delete(ctx, "k1"); err != nil {
		t.Fatalf("Delete(k1) failed: %v", err)
	}
	requireLRUOrder(t, c, "k2")

	if err := c.Set(ctx, "k3", []byte("v3")); err != nil {
		t.Fatalf("Set(k3) failed: %v", err)
	}

	requireLRUOrder(t, c, "k3", "k2")
	if _, err := c.Get(ctx, "k2"); err != nil {
		t.Errorf("Get(k2) failed: %v", err)
	}
}

// TestMemoryCache_LRUGetAndDeleteKeepsOrderInSync verifies that
// GetAndDelete removes the key from the recency order, so no stale element
// survives to shift later evictions.
func TestMemoryCache_LRUGetAndDeleteKeepsOrderInSync(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	c := cache.NewMemory(0, cache.WithMaxEntries(2))

	if err := c.Set(ctx, "k1", []byte("v1")); err != nil {
		t.Fatalf("Set(k1) failed: %v", err)
	}
	if err := c.Set(ctx, "k2", []byte("v2")); err != nil {
		t.Fatalf("Set(k2) failed: %v", err)
	}

	if _, err := c.GetAndDelete(ctx, "k1"); err != nil {
		t.Fatalf("GetAndDelete(k1) failed: %v", err)
	}
	requireLRUOrder(t, c, "k2")

	if err := c.Set(ctx, "k3", []byte("v3")); err != nil {
		t.Fatalf("Set(k3) failed: %v", err)
	}
	if err := c.Set(ctx, "k4", []byte("v4")); err != nil {
		t.Fatalf("Set(k4) failed: %v", err)
	}

	requireLRUOrder(t, c, "k4", "k3")
	if _, err := c.Get(ctx, "k2"); !errors.Is(err, cache.ErrKeyNotFound) {
		t.Errorf("Get(k2) error = %v, want ErrKeyNotFound", err)
	}
}

// TestMemoryCache_LRUCleanupRemovesExpiredFromOrder verifies that Cleanup
// frees both the entry count and the recency order, so the freed slot is
// available to the next insert without evicting a live entry.
func TestMemoryCache_LRUCleanupRemovesExpiredFromOrder(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	c := cache.NewMemory(0, cache.WithMaxEntries(2))

	if err := c.Set(ctx, "k1", []byte("v1"), cache.WithTTL(time.Millisecond)); err != nil {
		t.Fatalf("Set(k1) failed: %v", err)
	}
	if err := c.Set(ctx, "k2", []byte("v2")); err != nil {
		t.Fatalf("Set(k2) failed: %v", err)
	}

	time.Sleep(5 * time.Millisecond)

	if err := c.Cleanup(ctx); err != nil {
		t.Fatalf("Cleanup failed: %v", err)
	}
	requireLRUOrder(t, c, "k2")

	if err := c.Set(ctx, "k3", []byte("v3")); err != nil {
		t.Fatalf("Set(k3) failed: %v", err)
	}

	requireLRUOrder(t, c, "k3", "k2")
	if _, err := c.Get(ctx, "k2"); err != nil {
		t.Errorf("Get(k2) failed: %v", err)
	}
}

// TestMemoryCache_LRUExpiredGetRemovesFromOrder verifies that the Get that
// observes an expired entry also drops it from the recency order.
func TestMemoryCache_LRUExpiredGetRemovesFromOrder(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	c := cache.NewMemory(0, cache.WithMaxEntries(2))

	if err := c.Set(ctx, "k1", []byte("v1"), cache.WithTTL(time.Millisecond)); err != nil {
		t.Fatalf("Set(k1) failed: %v", err)
	}
	if err := c.Set(ctx, "k2", []byte("v2")); err != nil {
		t.Fatalf("Set(k2) failed: %v", err)
	}

	time.Sleep(5 * time.Millisecond)

	if _, err := c.Get(ctx, "k1"); !errors.Is(err, cache.ErrKeyNotFound) {
		t.Fatalf("Get(k1) error = %v, want ErrKeyNotFound", err)
	}
	requireLRUOrder(t, c, "k2")
}

// TestMemoryCache_LRUDrainResetsOrder verifies that Drain empties the
// recency order together with the entries, so the cache starts fresh.
func TestMemoryCache_LRUDrainResetsOrder(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	c := cache.NewMemory(0, cache.WithMaxEntries(2))

	if err := c.Set(ctx, "k1", []byte("v1")); err != nil {
		t.Fatalf("Set(k1) failed: %v", err)
	}
	if err := c.Set(ctx, "k2", []byte("v2")); err != nil {
		t.Fatalf("Set(k2) failed: %v", err)
	}

	items, err := c.Drain(ctx)
	if err != nil {
		t.Fatalf("Drain failed: %v", err)
	}
	if len(items) != 2 {
		t.Errorf("Drain returned %d items, want 2", len(items))
	}
	requireLRUOrder(t, c)

	for _, key := range []string{"k3", "k4"} {
		if err = c.Set(ctx, key, []byte(key)); err != nil {
			t.Fatalf("Set(%q) failed: %v", key, err)
		}
	}
	requireLRUOrder(t, c, "k4", "k3")
}

// TestMemoryCache_LRUIncrementCreateEvictsAtCap verifies that a counter
// created at the cap counts as an insert and evicts the least recently used
// entry.
func TestMemoryCache_LRUIncrementCreateEvictsAtCap(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	c := cache.NewMemory(0, cache.WithMaxEntries(2))

	if _, err := c.Increment(ctx, "k1", 1); err != nil {
		t.Fatalf("Increment(k1) failed: %v", err)
	}
	if _, err := c.Increment(ctx, "k2", 1); err != nil {
		t.Fatalf("Increment(k2) failed: %v", err)
	}
	if _, err := c.Increment(ctx, "k3", 1); err != nil {
		t.Fatalf("Increment(k3) failed: %v", err)
	}

	requireLRUOrder(t, c, "k3", "k2")
	if _, err := c.Get(ctx, "k1"); !errors.Is(err, cache.ErrKeyNotFound) {
		t.Errorf("Get(k1) error = %v, want ErrKeyNotFound", err)
	}
	if _, err := c.Get(ctx, "k3"); err != nil {
		t.Errorf("Get(k3) failed: %v", err)
	}
}

// TestMemoryCache_LRUSetOrFailEvictsAtCap verifies that SetOrFail inserts
// under the same recency rules as Set, including eviction at the cap.
func TestMemoryCache_LRUSetOrFailEvictsAtCap(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	c := cache.NewMemory(0, cache.WithMaxEntries(2))

	if err := c.Set(ctx, "k1", []byte("v1")); err != nil {
		t.Fatalf("Set(k1) failed: %v", err)
	}
	if err := c.Set(ctx, "k2", []byte("v2")); err != nil {
		t.Fatalf("Set(k2) failed: %v", err)
	}
	if err := c.SetOrFail(ctx, "k3", []byte("v3")); err != nil {
		t.Fatalf("SetOrFail(k3) failed: %v", err)
	}

	requireLRUOrder(t, c, "k3", "k2")
	if err := c.SetOrFail(ctx, "k2", []byte("again")); !errors.Is(err, cache.ErrKeyExists) {
		t.Errorf("SetOrFail(k2) error = %v, want ErrKeyExists", err)
	}
}

// TestMemoryCache_LRUSetOrFailOverExpiredKeepsRecency verifies the documented
// edge that overwriting an expired-but-present key does not refresh recency:
// the fresh value sits at the old position and can be evicted first.
func TestMemoryCache_LRUSetOrFailOverExpiredKeepsRecency(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	c := cache.NewMemory(0, cache.WithMaxEntries(2))

	if err := c.Set(ctx, "k1", []byte("v1"), cache.WithTTL(time.Millisecond)); err != nil {
		t.Fatalf("Set(k1) failed: %v", err)
	}
	if err := c.Set(ctx, "k2", []byte("v2")); err != nil {
		t.Fatalf("Set(k2) failed: %v", err)
	}

	time.Sleep(5 * time.Millisecond)

	if err := c.SetOrFail(ctx, "k1", []byte("v1-new")); err != nil {
		t.Fatalf("SetOrFail(k1) over expired failed: %v", err)
	}
	requireLRUOrder(t, c, "k2", "k1")

	// k1 was written but not touched, so the cap evicts it first.
	if err := c.Set(ctx, "k3", []byte("v3")); err != nil {
		t.Fatalf("Set(k3) failed: %v", err)
	}
	requireLRUOrder(t, c, "k3", "k2")
	if _, err := c.Get(ctx, "k1"); !errors.Is(err, cache.ErrKeyNotFound) {
		t.Errorf("Get(k1) error = %v, want ErrKeyNotFound", err)
	}
}

// TestMemoryCache_UncappedByDefaultAndNonPositiveCap verifies that no cap is
// installed by default or with a non-positive WithMaxEntries.
func TestMemoryCache_UncappedByDefaultAndNonPositiveCap(t *testing.T) {
	t.Parallel()

	ctx := context.Background()

	cases := map[string]*cache.MemoryCache{
		"default":          cache.NewMemory(0),
		"zero":             cache.NewMemory(0, cache.WithMaxEntries(0)),
		"negative":         cache.NewMemory(0, cache.WithMaxEntries(-1)),
		"no opts zero ttl": cache.NewMemory(time.Hour),
	}

	for name, c := range cases {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			if order := c.LRUOrder(); order != nil {
				t.Fatalf("LRUOrder() = %v, want nil (uncapped)", order)
			}

			for i := range 10 {
				key := "k" + string(rune('a'+i))
				if err := c.Set(ctx, key, []byte(key)); err != nil {
					t.Fatalf("Set(%q) failed: %v", key, err)
				}
			}

			for i := range 10 {
				key := "k" + string(rune('a'+i))
				if _, err := c.Get(ctx, key); err != nil {
					t.Errorf("Get(%q) failed: %v", key, err)
				}
			}
		})
	}
}

// TestMemoryCache_LRUCappedConcurrentUse exercises a capped cache from
// concurrent goroutines; correctness is that the race detector stays silent
// and the cache never grows past the cap.
func TestMemoryCache_LRUCappedConcurrentUse(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	const maxEntries = 8
	const keySpace = 32

	c := cache.NewMemory(0, cache.WithMaxEntries(maxEntries))

	var wg sync.WaitGroup
	for worker := range 4 {
		wg.Go(func() {
			for i := range 50 {
				key := "k" + string(rune('a'+(worker*13+i)%keySpace))
				switch i % 4 {
				case 0:
					if err := c.Set(ctx, key, []byte(key)); err != nil {
						t.Errorf("Set(%q) failed: %v", key, err)
					}
				case 1:
					if _, err := c.Get(ctx, key); err != nil && !errors.Is(err, cache.ErrKeyNotFound) {
						t.Errorf("Get(%q) failed: %v", key, err)
					}
				case 2:
					if _, err := c.Increment(ctx, key, 1); err != nil &&
						!errors.Is(err, cache.ErrNotAnInteger) {
						t.Errorf("Increment(%q) failed: %v", key, err)
					}
				case 3:
					if err := c.Delete(ctx, key); err != nil {
						t.Errorf("Delete(%q) failed: %v", key, err)
					}
				}
			}
		})
	}
	wg.Wait()

	if order := c.LRUOrder(); len(order) > maxEntries {
		t.Errorf("recency order holds %d keys, want at most %d", len(order), maxEntries)
	}
}
