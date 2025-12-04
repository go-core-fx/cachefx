package cachefx

import (
	"fmt"
	"net/url"

	"github.com/go-core-fx/cachefx/cache"
)

type Factory interface {
	New(name string) (cache.Cache, error)
	WithName(name string) Factory
}

type factory struct {
	new func(name string) (cache.Cache, error)
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
		return &factory{
			new: func(_ string) (cache.Cache, error) {
				return cache.NewMemory(0), nil
			},
		}, nil
	case "redis":
		return &factory{
			new: func(name string) (cache.Cache, error) {
				return cache.NewRedis(cache.RedisConfig{
					Client: nil,
					URL:    config.URL,
					Prefix: name,
					TTL:    0,
				})
			},
		}, nil
	default:
		return nil, fmt.Errorf("%w: invalid scheme: %s", ErrInvalidFactoryConfig, u.Scheme)
	}
}

// New implements Factory.
func (f *factory) New(name string) (cache.Cache, error) {
	return f.new(name)
}

// WithName implements Factory.
func (f *factory) WithName(prefix string) Factory {
	return &factory{
		new: func(name string) (cache.Cache, error) {
			return f.new(prefix + ":" + name)
		},
	}
}
