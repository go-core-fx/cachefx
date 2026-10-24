package cachefx

import "github.com/go-core-fx/cachefx/cache"

// NewTrackedFactory exposes the package-private factory with a caller-supplied
// cache constructor, so external tests can inject stub caches.
func NewTrackedFactory(build func(name string) (cache.Cache, error)) Factory {
	return newFactory("", build)
}
