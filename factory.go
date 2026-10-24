package cachefx

import (
	"context"
	"errors"
	"fmt"
	"net/url"
	"sync"

	"github.com/go-core-fx/cachefx/cache"
)

type Factory interface {
	New(name string) (cache.Cache, error)
	WithName(name string) Factory

	// CleanupAll evicts expired entries from every cache handed out by this
	// factory, including the ones created by factories derived from it through
	// WithName.
	CleanupAll(ctx context.Context) error
}

// trackedCache is a cache handed out by a factory, stored under the name it
// was created with, so a cleanup failure can be reported with context.
type trackedCache struct {
	name  string
	cache cache.Cache
}

// cacheTracker holds the caches handed out by a factory.
//
// It is shared between a factory and the factories derived from it, so a cache
// is swept no matter which of them created it.
type cacheTracker struct {
	mu     sync.Mutex
	caches []trackedCache
}

func (t *cacheTracker) add(c trackedCache) {
	t.mu.Lock()
	t.caches = append(t.caches, c)
	t.mu.Unlock()
}

func (t *cacheTracker) remove(c cache.Cache) {
	t.mu.Lock()
	defer t.mu.Unlock()

	for i, tc := range t.caches {
		if tc.cache == c {
			t.caches = append(t.caches[:i], t.caches[i+1:]...)

			return
		}
	}
}

func (t *cacheTracker) list() []trackedCache {
	t.mu.Lock()
	defer t.mu.Unlock()

	caches := make([]trackedCache, len(t.caches))
	copy(caches, t.caches)

	return caches
}

// unregisteringCache wraps a cache handed out by a factory so that closing it
// removes it from the tracker: a cache the caller has closed must not be
// swept anymore.
type unregisteringCache struct {
	cache.Cache

	tracker *cacheTracker
}

// Close closes the underlying cache and then unregisters it. The entry is
// removed whether or not the close succeeded, because a caller that closed a
// cache relinquishes it either way.
func (c *unregisteringCache) Close() error {
	err := c.Cache.Close()
	c.tracker.remove(c)

	if err != nil {
		return fmt.Errorf("failed to close cache: %w", err)
	}

	return nil
}

type factory struct {
	prefix  string
	new     func(name string) (cache.Cache, error)
	tracker *cacheTracker
}

func NewFactory(config Config) (Factory, error) {
	if config.URL == "" {
		config.URL = "memory://"
	}

	u, err := url.Parse(config.URL)
	if err != nil {
		return nil, fmt.Errorf("%w: failed to parse url: %w", ErrInvalidFactoryConfig, err)
	}

	switch u.Scheme {
	case "memory":
		return newFactory("", func(_ string) (cache.Cache, error) {
			return cache.NewMemory(0), nil
		}), nil
	case "redis":
		return newFactory("", func(name string) (cache.Cache, error) {
			return cache.NewRedis(cache.RedisConfig{
				Client: nil,
				URL:    config.URL,
				Prefix: name,
				TTL:    0,
			})
		}), nil
	default:
		return nil, fmt.Errorf("%w: invalid scheme: %s", ErrInvalidFactoryConfig, u.Scheme)
	}
}

// newFactory builds a factory with a fresh tracker; every factory created by
// NewFactory shares its tracker with the factories derived from it.
func newFactory(prefix string, build func(name string) (cache.Cache, error)) *factory {
	return &factory{
		prefix:  prefix,
		new:     build,
		tracker: &cacheTracker{mu: sync.Mutex{}, caches: []trackedCache{}},
	}
}

// New implements Factory.
func (f *factory) New(name string) (cache.Cache, error) {
	fullName := name
	if f.prefix != "" {
		fullName = f.prefix + ":" + name
	}

	inner, err := f.new(fullName)
	if err != nil {
		return nil, fmt.Errorf("failed to create cache %q: %w", name, err)
	}

	wrapped := &unregisteringCache{Cache: inner, tracker: f.tracker}
	f.tracker.add(trackedCache{name: fullName, cache: wrapped})

	return wrapped, nil
}

// WithName implements Factory.
func (f *factory) WithName(prefix string) Factory {
	fullPrefix := prefix
	if f.prefix != "" {
		fullPrefix = f.prefix + ":" + prefix
	}

	return &factory{
		prefix:  fullPrefix,
		new:     f.new,
		tracker: f.tracker,
	}
}

// CleanupAll implements Factory.
func (f *factory) CleanupAll(ctx context.Context) error {
	var errs []error
	for _, c := range f.tracker.list() {
		if err := c.cache.Cleanup(ctx); err != nil {
			errs = append(errs, fmt.Errorf("failed to cleanup cache %q: %w", c.name, err))
		}
	}

	return errors.Join(errs...)
}
