package cachefx

import (
	"context"
	"fmt"
	"time"

	"go.uber.org/zap"
)

// Sweeper periodically evicts expired entries from every cache created by the
// Factory.
//
// Eviction of expired entries is not guaranteed by every backend: the in-memory
// backend keeps expired items until an explicit Cleanup, and each New call
// returns a distinct instance, so the sweeper walks every cache the factory
// has handed out.
type Sweeper struct {
	factory  Factory
	interval time.Duration
	logger   *zap.Logger
}

// NewSweeper creates a Sweeper running every config.SweepInterval.
//
// It fails when the interval is not positive, so a module wired to run the
// sweeper fails at startup instead of silently never sweeping.
func NewSweeper(config Config, factory Factory, logger *zap.Logger) (*Sweeper, error) {
	if config.SweepInterval <= 0 {
		return nil, fmt.Errorf(
			"%w: sweep interval must be positive, got %s",
			ErrInvalidFactoryConfig,
			config.SweepInterval,
		)
	}

	return &Sweeper{
		factory:  factory,
		interval: config.SweepInterval,
		logger:   logger,
	}, nil
}

// Run evicts expired entries until ctx is canceled.
func (s *Sweeper) Run(ctx context.Context) error {
	ticker := time.NewTicker(s.interval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			s.logger.Info("Cache sweeper stopped")
			return nil
		case <-ticker.C:
			if err := s.factory.CleanupAll(ctx); err != nil {
				s.logger.Error("failed to cleanup caches", zap.Error(err))
			}
		}
	}
}
