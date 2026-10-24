package cachefx_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/go-core-fx/cachefx"
	"github.com/go-core-fx/cachefx/cache"
	"go.uber.org/zap"
)

// TestFactory_TracksCachesPerInstance checks that each New call hands out a
// distinct instance, so a sweeper must walk the factory's caches rather than
// create its own.
func TestFactory_TracksCachesPerInstance(t *testing.T) {
	t.Parallel()

	factory, err := cachefx.NewFactory(cachefx.Config{URL: "memory://"})
	if err != nil {
		t.Fatalf("NewFactory failed: %v", err)
	}

	first, err := factory.New("messages")
	if err != nil {
		t.Fatalf("New failed: %v", err)
	}
	second, err := factory.New("messages")
	if err != nil {
		t.Fatalf("New failed: %v", err)
	}

	if err = first.Set(context.Background(), "only-in-first", []byte("v")); err != nil {
		t.Fatalf("Set failed: %v", err)
	}

	_, err = second.Get(context.Background(), "only-in-first")
	if !errors.Is(err, cache.ErrKeyNotFound) {
		t.Fatalf("expected ErrKeyNotFound, got %v", err)
	}
}

// TestFactory_WithNestedNamesComposePrefixes checks that nested WithName calls
// compose the prefix handed to the backend.
func TestFactory_WithNestedNamesComposePrefixes(t *testing.T) {
	t.Parallel()

	var names []string
	factory := cachefx.NewTrackedFactory(func(name string) (cache.Cache, error) {
		names = append(names, name)
		return newStubCache(), nil
	})

	if _, err := factory.WithName("a").WithName("b").New("c"); err != nil {
		t.Fatalf("New failed: %v", err)
	}

	if len(names) != 1 || names[0] != "a:b:c" {
		t.Fatalf("expected [a:b:c], got %v", names)
	}
}

// TestFactory_CleanupAllJoinsErrors checks that a failing cache does not hide
// the failures of the others.
func TestFactory_CleanupAllJoinsErrors(t *testing.T) {
	t.Parallel()

	created := 0
	factory := cachefx.NewTrackedFactory(func(_ string) (cache.Cache, error) {
		created++
		switch created {
		case 1:
			return &failingCache{stubCache: newStubCache(), err: cache.ErrKeyNotFound}, nil
		default:
			return &failingCache{stubCache: newStubCache(), err: cache.ErrKeyExists}, nil
		}
	})

	if _, err := factory.New("first"); err != nil {
		t.Fatalf("New failed: %v", err)
	}
	if _, err := factory.New("second"); err != nil {
		t.Fatalf("New failed: %v", err)
	}

	err := factory.CleanupAll(context.Background())
	if !errors.Is(err, cache.ErrKeyNotFound) {
		t.Errorf("expected joined ErrKeyNotFound, got %v", err)
	}
	if !errors.Is(err, cache.ErrKeyExists) {
		t.Errorf("expected joined ErrKeyExists, got %v", err)
	}
}

// TestFactory_CleanupAllSkipsClosedCaches checks that closing a cache
// unregisters it, so the sweeper does not keep walking a closed cache.
func TestFactory_CleanupAllSkipsClosedCaches(t *testing.T) {
	t.Parallel()

	stubs := make([]*stubCache, 0, 2)
	factory := cachefx.NewTrackedFactory(func(_ string) (cache.Cache, error) {
		stub := newStubCache()
		stubs = append(stubs, stub)

		return stub, nil
	})

	if _, err := factory.New("open"); err != nil {
		t.Fatalf("New failed: %v", err)
	}

	closed, err := factory.New("closed")
	if err != nil {
		t.Fatalf("New failed: %v", err)
	}

	if err = closed.Close(); err != nil {
		t.Fatalf("Close failed: %v", err)
	}

	if err = factory.CleanupAll(context.Background()); err != nil {
		t.Fatalf("CleanupAll failed: %v", err)
	}

	if got := stubs[0].cleanupCount(); got != 1 {
		t.Errorf("expected open cache to be swept once, got %d", got)
	}
	if got := stubs[1].cleanupCount(); got != 0 {
		t.Errorf("expected closed cache not to be swept, got %d", got)
	}
}

// TestNewSweeper_RejectsNonPositiveInterval checks that a misconfigured
// sweeper fails at construction instead of never sweeping.
func TestNewSweeper_RejectsNonPositiveInterval(t *testing.T) {
	t.Parallel()

	for _, interval := range []time.Duration{0, -time.Minute} {
		factory, _ := newStubFactory()
		_, err := cachefx.NewSweeper(cachefx.Config{SweepInterval: interval}, factory, zap.NewNop())
		if !errors.Is(err, cachefx.ErrInvalidFactoryConfig) {
			t.Errorf("interval %s: expected ErrInvalidFactoryConfig, got %v", interval, err)
		}
	}
}

// failingCache fails every Cleanup with err.
type failingCache struct {
	*stubCache

	err error
}

func (c *failingCache) Cleanup(context.Context) error {
	return c.err
}
