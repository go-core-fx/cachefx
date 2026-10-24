package cachefx

import "time"

// Config controls the cache backend via a URL (e.g., "memory://", "redis://...")
// and the sweeper.
type Config struct {
	URL string

	// SweepInterval is how often the Sweeper evicts expired entries from every
	// cache created by the Factory. A non-positive value disables sweeping:
	// NewSweeper rejects it, so a module wired with withRun=true fails at
	// startup instead of silently never sweeping.
	SweepInterval time.Duration
}
