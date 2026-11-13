package cachefx

import "time"

// Config controls the cache backend via a URL (e.g., "memory://", "redis://...")
// and the sweeper.
type Config struct {
	// URL selects and configures the backend. It is passed to the backend
	// unchanged, like Redis's: "memory://" serves an unbounded in-memory
	// cache, and "memory://?max_entries=N" caps every cache the factory hands
	// out at N entries with least-recently-used eviction. The cache backend
	// parses the URL, so a malformed N fails when the cache is created
	// (typically at app startup) instead of silently dropping the cap.
	// "redis://" connects to Redis, where eviction is a server-side concern.
	URL string

	// SweepInterval is how often the Sweeper evicts expired entries from every
	// cache created by the Factory. A non-positive value disables sweeping:
	// NewSweeper rejects it, so a module wired with withRun=true fails at
	// startup instead of silently never sweeping.
	SweepInterval time.Duration
}
