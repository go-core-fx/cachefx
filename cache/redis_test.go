package cache_test

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/go-core-fx/cachefx/cache"
	"github.com/testcontainers/testcontainers-go"
	"github.com/testcontainers/testcontainers-go/wait"
)

var (
	redisOnce      sync.Once
	redisContainer testcontainers.Container
	redisURL       string
	errRedis       error
)

// TestMain terminates the shared Redis testcontainer after the test run, if one
// was started. Runs that never reach a Redis test leave no container behind.
func TestMain(m *testing.M) {
	code := m.Run()

	if redisContainer != nil {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		if err := redisContainer.Terminate(ctx); err != nil {
			fmt.Fprintf(os.Stderr, "failed to terminate redis testcontainer: %v\n", err)
		}
		cancel()
	}

	os.Exit(code)
}

// redisTestURL starts a shared Redis testcontainer on first use and returns its
// connection URL. It skips the calling test when Docker is unavailable, but fails
// it in CI so a missing container can never hide behind a green run.
func redisTestURL(t *testing.T) string {
	t.Helper()

	redisOnce.Do(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
		defer cancel()

		container, err := testcontainers.GenericContainer(ctx, testcontainers.GenericContainerRequest{
			ContainerRequest: testcontainers.ContainerRequest{
				Image:        "redis:8-alpine",
				ExposedPorts: []string{"6379/tcp"},
				WaitingFor:   wait.ForListeningPort("6379/tcp").WithStartupTimeout(30 * time.Second),
			},
			Started: true,
		})
		if err != nil {
			errRedis = fmt.Errorf("failed to start redis testcontainer: %w", err)
			return
		}

		redisContainer = container

		endpoint, err := container.Endpoint(ctx, "tcp")
		if err != nil {
			errRedis = fmt.Errorf("failed to get redis testcontainer endpoint: %w", err)
			return
		}

		redisURL = "redis://" + strings.TrimPrefix(endpoint, "tcp://")
	})

	if errRedis != nil {
		if os.Getenv("CI") != "" {
			t.Fatalf("redis testcontainer unavailable: %v", errRedis)
		}

		t.Skipf("redis testcontainer unavailable: %v", errRedis)
	}

	return redisURL
}

// newRedisCache builds a cache bound to the shared testcontainer, isolated per
// test through the test name as key prefix, and closes its client on cleanup.
func newRedisCache(t *testing.T, ttl time.Duration) *cache.RedisCache {
	t.Helper()

	c, err := cache.NewRedis(cache.RedisConfig{
		URL:    redisTestURL(t),
		Prefix: t.Name(),
		TTL:    ttl,
	})
	if err != nil {
		t.Fatalf("NewRedis failed: %v", err)
	}

	t.Cleanup(func() {
		if closeErr := c.Close(); closeErr != nil {
			t.Errorf("Close failed: %v", closeErr)
		}
	})

	return c
}

func TestRedisCache_IncrementCreatesMissingKey(t *testing.T) {
	t.Parallel()

	c := newRedisCache(t, 0)

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

func TestRedisCache_IncrementAccumulates(t *testing.T) {
	t.Parallel()

	c := newRedisCache(t, 0)

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

func TestRedisCache_IncrementAppliesDefaultTTLAndRecreatesExpired(t *testing.T) {
	t.Parallel()

	c := newRedisCache(t, 100*time.Millisecond)

	ctx := context.Background()
	key := "short-lived"

	n, err := c.Increment(ctx, key, 1)
	if err != nil {
		t.Fatalf("Increment failed: %v", err)
	}

	if n != 1 {
		t.Errorf("Expected 1, got %d", n)
	}

	time.Sleep(300 * time.Millisecond)

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

func TestRedisCache_IncrementCreateWithTTLOption(t *testing.T) {
	t.Parallel()

	c := newRedisCache(t, 0)

	ctx := context.Background()
	key := "one-shot"

	if _, err := c.Increment(ctx, key, 1, cache.WithTTL(100*time.Millisecond)); err != nil {
		t.Fatalf("Increment failed: %v", err)
	}

	time.Sleep(300 * time.Millisecond)

	if _, err := c.Get(ctx, key); !errors.Is(err, cache.ErrKeyNotFound) {
		t.Errorf("Expected ErrKeyNotFound after option TTL elapsed, got %v", err)
	}
}

func TestRedisCache_IncrementIgnoresOptionsOnExistingKey(t *testing.T) {
	t.Parallel()

	c := newRedisCache(t, time.Hour)

	ctx := context.Background()
	key := "long-lived"

	if _, err := c.Increment(ctx, key, 1); err != nil {
		t.Fatalf("Increment failed: %v", err)
	}

	if n, err := c.Increment(ctx, key, 1, cache.WithTTL(50*time.Millisecond)); err != nil {
		t.Fatalf("Increment failed: %v", err)
	} else if n != 2 {
		t.Errorf("Expected 2, got %d", n)
	}

	time.Sleep(300 * time.Millisecond)

	retrieved, err := c.Get(ctx, key)
	if err != nil {
		t.Fatalf("Get failed: the TTL option must be ignored for an existing key: %v", err)
	}

	if string(retrieved) != "2" {
		t.Errorf("Expected 2, got %s", retrieved)
	}
}

func TestRedisCache_IncrementPreservesExistingTTL(t *testing.T) {
	t.Parallel()

	c := newRedisCache(t, 100*time.Millisecond)

	ctx := context.Background()
	key := "explicit-ttl"

	if err := c.Set(ctx, key, []byte("41"), cache.WithTTL(time.Hour)); err != nil {
		t.Fatalf("Set failed: %v", err)
	}

	// Sleep past the cache default TTL so an Increment that re-applied it
	// would let the counter expire in the next sleep.
	time.Sleep(300 * time.Millisecond)

	if n, err := c.Increment(ctx, key, 1); err != nil {
		t.Fatalf("Increment failed: %v", err)
	} else if n != 42 {
		t.Errorf("Expected 42, got %d", n)
	}

	time.Sleep(300 * time.Millisecond)

	retrieved, err := c.Get(ctx, key)
	if err != nil {
		t.Fatalf("Get failed: the original TTL must survive Increment: %v", err)
	}

	if string(retrieved) != "42" {
		t.Errorf("Expected 42, got %s", retrieved)
	}
}

func TestRedisCache_IncrementRejectsNonCanonicalValues(t *testing.T) {
	t.Parallel()

	c := newRedisCache(t, 0)

	ctx := context.Background()

	for _, value := range []string{"abc", "007", "+7", " 7", ""} {
		key := "counter:" + value
		if err := c.Set(ctx, key, []byte(value), cache.WithTTL(time.Hour)); err != nil {
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

func TestRedisCache_IncrementOverflow(t *testing.T) {
	t.Parallel()

	c := newRedisCache(t, 0)

	ctx := context.Background()

	if err := c.Set(ctx, "max", []byte("9223372036854775807"), cache.WithTTL(time.Hour)); err != nil {
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

	if err = c.Set(ctx, "min", []byte("-9223372036854775808"), cache.WithTTL(time.Hour)); err != nil {
		t.Fatalf("Set failed: %v", err)
	}

	if _, err = c.Increment(ctx, "min", -1); !errors.Is(err, cache.ErrCounterOverflow) {
		t.Errorf("Expected ErrCounterOverflow, got %v", err)
	}
}

func TestRedisCache_IncrementConcurrent(t *testing.T) {
	t.Parallel()

	c := newRedisCache(t, 0)

	ctx := context.Background()
	key := "views"

	const numGoroutines = 100
	const incrementsPerGoroutine = 20

	var wg sync.WaitGroup

	for range numGoroutines {
		wg.Go(func() {
			for range incrementsPerGoroutine {
				if _, err := c.Increment(ctx, key, 1); err != nil {
					t.Errorf("Increment failed: %v", err)
					return
				}
			}
		})
	}

	wg.Wait()

	retrieved, err := c.Get(ctx, key)
	if err != nil {
		t.Fatalf("Get failed: %v", err)
	}

	const expected = numGoroutines * incrementsPerGoroutine
	if string(retrieved) != strconv.Itoa(expected) {
		t.Errorf("Expected %d, got %s", expected, retrieved)
	}
}
