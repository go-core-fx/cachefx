package cachefx

import (
	"go.uber.org/fx"
	"go.uber.org/zap"
)

func Module() fx.Option {
	return fx.Module(
		"cachefx",
		fx.Decorate(func(log *zap.Logger) *zap.Logger {
			return log.Named("cachefx")
		}),
		fx.Provide(NewFactory),
	)
}
