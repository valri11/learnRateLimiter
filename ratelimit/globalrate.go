package ratelimit

import (
	"context"
	"errors"
	"fmt"
	"net/http"

	"go.uber.org/zap"

	mdlogger "github.com/valri11/go-servicepack/logger"
	"github.com/valri11/learnRateLimiter/config"
)

type LimitStore interface {
	TryPassRequestLimit(ctx context.Context) bool
}

func NewLimitStore(store config.Store, rateLimitPerSec int32) (LimitStore, error) {

	switch store.Type {
	case "local":
		return NewLocalLimitStore(store, rateLimitPerSec)
	case "localSlidingWindow":
		return NewLocalSlidingWindowLimit(rateLimitPerSec)
	case "redis":
		return NewRedisLimitStore(store, rateLimitPerSec)
	case "redisSlidingWindow":
		return NewRedisSlidingWindowLimit(store, rateLimitPerSec)
	}

	return nil, errors.New(fmt.Sprintf("unknown store type: %s", store.Type))
}

func WithGlobalRequestRateLimiter(store config.Store, rateLimitPerSec int32) func(http.Handler) http.Handler {
	limitStore, err := NewLimitStore(store, rateLimitPerSec)
	if err != nil {
		panic(err)
	}
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			ctx := r.Context()
			if !limitStore.TryPassRequestLimit(ctx) {
				logger := mdlogger.FromContext(r.Context())
				logger.With(zap.Int32("limit", rateLimitPerSec)).Warn("request rate limited")

				w.WriteHeader(http.StatusTooManyRequests)
				return
			}

			next.ServeHTTP(w, r)
		})
	}
}
