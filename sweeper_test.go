package cachefx_test

import (
	"context"
	"strconv"
	"sync"
	"testing"
	"time"

	"github.com/go-core-fx/cachefx"
	"github.com/go-core-fx/cachefx/cache"
	"go.uber.org/zap"
)

const (
	testInterval = 10 * time.Millisecond
	stubTTL      = time.Millisecond
	waitTimeout  = 2 * time.Second
)

// TestSweeper_EvictsExpiredEntries checks that the sweeper asks the tracked
// caches to drop their expired entries.
//
// The assertion is made on the number of items the cache still holds, because
// the real cache implementations drop the expired items on their own when they
// are drained, so a drained cache cannot tell a swept cache from an unswept
// one.
func TestSweeper_EvictsExpiredEntries(t *testing.T) {
	t.Parallel()

	factory := newStubFactory()

	storage, err := factory.New("messages")
	if err != nil {
		t.Fatalf("New failed: %v", err)
	}

	stub, ok := storage.(*stubCache)
	if !ok {
		t.Fatalf("expected *stubCache, got %T", storage)
	}

	// Fill the cache with entries that expire immediately.
	for i := range 100 {
		if err = storage.Set(context.Background(), "user:ext-"+strconv.Itoa(i), []byte("v")); err != nil {
			t.Fatalf("Set failed: %v", err)
		}
	}

	if got := stub.entryCount(); got != 100 {
		t.Fatalf("expected 100 entries, got %d", got)
	}

	sweeper := newTestSweeper(t, factory)

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	go func() {
		_ = sweeper.Run(ctx)
	}()

	waitFor(t, func() bool { return stub.entryCount() == 0 })

	if stub.cleanupCount() == 0 {
		t.Fatal("expected at least one cleanup call")
	}
}

// TestFactory_NamedFactoryCachesAreSwept checks that a cache created through a
// named factory is swept by the factory it was derived from.
func TestFactory_NamedFactoryCachesAreSwept(t *testing.T) {
	t.Parallel()

	factory := newStubFactory()

	storage, err := factory.WithName("named").New("messages")
	if err != nil {
		t.Fatalf("New failed: %v", err)
	}

	stub, ok := storage.(*stubCache)
	if !ok {
		t.Fatalf("expected *stubCache, got %T", storage)
	}

	if err = storage.Set(context.Background(), "user:ext-1", []byte("v")); err != nil {
		t.Fatalf("Set failed: %v", err)
	}

	if got := stub.entryCount(); got != 1 {
		t.Fatalf("expected 1 entry, got %d", got)
	}

	sweeper := newTestSweeper(t, factory)

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	go func() {
		_ = sweeper.Run(ctx)
	}()

	waitFor(t, func() bool { return stub.entryCount() == 0 })
}

// TestSweeper_KeepsLiveEntries checks that sweeping does not touch entries
// that have not expired.
func TestSweeper_KeepsLiveEntries(t *testing.T) {
	t.Parallel()

	factory, err := cachefx.NewFactory(cachefx.Config{URL: "memory://"})
	if err != nil {
		t.Fatalf("NewFactory failed: %v", err)
	}

	storage, err := factory.New("messages")
	if err != nil {
		t.Fatalf("New failed: %v", err)
	}

	sweeper := newTestSweeper(t, factory)

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	go func() {
		_ = sweeper.Run(ctx)
	}()

	if err = storage.Set(ctx, "live", []byte("v"), cache.WithTTL(time.Hour)); err != nil {
		t.Fatalf("Set failed: %v", err)
	}

	time.Sleep(100 * time.Millisecond)

	got, err := storage.Get(ctx, "live")
	if err != nil {
		t.Fatalf("Get failed: %v", err)
	}
	if string(got) != "v" {
		t.Errorf("Expected v, got %s", string(got))
	}
}

// newTestSweeper builds a Sweeper with the test interval and a no-op logger.
func newTestSweeper(t *testing.T, factory cachefx.Factory) *cachefx.Sweeper {
	t.Helper()

	sweeper, err := cachefx.NewSweeper(cachefx.Config{SweepInterval: testInterval}, factory, zap.NewNop())
	if err != nil {
		t.Fatalf("NewSweeper failed: %v", err)
	}

	return sweeper
}

// waitFor waits until the condition is met, and fails the test if it is not met
// in time.
func waitFor(t *testing.T, cond func() bool) {
	t.Helper()

	deadline := time.Now().Add(waitTimeout)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}

		time.Sleep(20 * time.Millisecond)
	}

	t.Fatal("condition was not met in time")
}

// stubCache is a cache that keeps its expired items until Cleanup removes them.
//
// The cachefx options are opaque outside of the cachefx package, so the
// lifetime of the items is defined by the stub itself.
type stubCache struct {
	mu       sync.Mutex
	items    map[string]stubItem
	cleanups int
}

type stubItem struct {
	value      []byte
	validUntil time.Time
}

func newStubCache() *stubCache {
	return &stubCache{
		items: map[string]stubItem{},
	}
}

func (c *stubCache) Set(_ context.Context, key string, value []byte, _ ...cache.Option) error {
	c.mu.Lock()
	defer c.mu.Unlock()

	c.items[key] = stubItem{
		value:      value,
		validUntil: time.Now().Add(stubTTL),
	}

	return nil
}

func (c *stubCache) SetOrFail(_ context.Context, key string, value []byte, _ ...cache.Option) error {
	c.mu.Lock()
	defer c.mu.Unlock()

	if _, ok := c.items[key]; ok {
		return cache.ErrKeyExists
	}

	c.items[key] = stubItem{
		value:      value,
		validUntil: time.Now().Add(stubTTL),
	}

	return nil
}

func (c *stubCache) Get(_ context.Context, key string, _ ...cache.GetOption) ([]byte, error) {
	c.mu.Lock()
	defer c.mu.Unlock()

	item, ok := c.items[key]
	if !ok || item.expired(time.Now()) {
		return nil, cache.ErrKeyNotFound
	}

	return item.value, nil
}

func (c *stubCache) GetAndDelete(ctx context.Context, key string) ([]byte, error) {
	value, err := c.Get(ctx, key)
	if err != nil {
		return nil, err
	}

	if err = c.Delete(ctx, key); err != nil {
		return nil, err
	}

	return value, nil
}

func (c *stubCache) Delete(_ context.Context, key string) error {
	c.mu.Lock()
	defer c.mu.Unlock()

	delete(c.items, key)

	return nil
}

func (c *stubCache) Cleanup(_ context.Context) error {
	c.mu.Lock()
	defer c.mu.Unlock()

	now := time.Now()
	for key, item := range c.items {
		if item.expired(now) {
			delete(c.items, key)
		}
	}

	c.cleanups++

	return nil
}

func (c *stubCache) Drain(_ context.Context) (map[string][]byte, error) {
	c.mu.Lock()
	defer c.mu.Unlock()

	items := make(map[string][]byte, len(c.items))
	for key, item := range c.items {
		if !item.expired(time.Now()) {
			items[key] = item.value
		}
	}

	c.items = map[string]stubItem{}

	return items, nil
}

func (c *stubCache) Close() error {
	return nil
}

func (c *stubCache) entryCount() int {
	c.mu.Lock()
	defer c.mu.Unlock()

	return len(c.items)
}

func (c *stubCache) cleanupCount() int {
	c.mu.Lock()
	defer c.mu.Unlock()

	return c.cleanups
}

func (i stubItem) expired(now time.Time) bool {
	return !i.validUntil.IsZero() && now.After(i.validUntil)
}

// newStubFactory creates a factory that hands out stub caches, so a test can
// observe how the sweeper treats the caches it tracks.
func newStubFactory() cachefx.Factory {
	return cachefx.NewTrackedFactory(func(_ string) (cache.Cache, error) {
		return newStubCache(), nil
	})
}
