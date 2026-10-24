package cachefx

import (
	"github.com/go-core-fx/fxutil"
	"go.uber.org/fx"
	"go.uber.org/zap"
)

type moduleConfig struct {
	withSweeper bool
}

// Option configures Module.
type Option func(*moduleConfig)

// WithSweeper registers the Sweeper as a runnable, so Config.SweepInterval
// must be positive or the app fails at startup.
func WithSweeper() Option {
	return func(cfg *moduleConfig) {
		cfg.withSweeper = true
	}
}

// Module provides the cache Factory and, with WithSweeper, registers the
// Sweeper to periodically evict expired entries from every cache the factory
// hands out.
func Module(opts ...Option) fx.Option {
	cfg := new(moduleConfig)
	for _, opt := range opts {
		opt(cfg)
	}

	options := []fx.Option{
		fx.Decorate(func(log *zap.Logger) *zap.Logger {
			return log.Named("cachefx")
		}),
		fx.Provide(NewFactory),
	}

	if cfg.withSweeper {
		options = append(options, fx.Provide(NewSweeper, fx.Private))
		options = append(options, fx.Invoke(fxutil.RegisterRunnable[*Sweeper]()))
	}

	return fx.Module("cachefx", options...)
}
