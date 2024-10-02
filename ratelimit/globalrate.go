package ratelimit

import (
	"context"
	"fmt"
	"net/http"

	"github.com/valri11/learnRateLimiter/config"
)

type LimitStore interface {
	TryPassRequestLimit(ctx context.Context) bool
}

func NewLimitStore(store config.Store, rateLimitPerSec int) (LimitStore, error) {

	switch store.Type {
	case "localFixedWindow":
		return NewLocalFixedWindowLimit(rateLimitPerSec)
	case "redisFixedWindow":
		return NewRedisFixedWindowLimit(store, rateLimitPerSec)
	case "localSlidingWindow":
		return NewLocalSlidingWindowLimit(rateLimitPerSec)
	case "redisSlidingWindow":
		return NewRedisSlidingWindowLimit(store, rateLimitPerSec)
	case "redisTokenBucket":
		return NewRedisTokenBucketLimit(store, rateLimitPerSec)
	case "localAdaptiveTokenBucket":
		return NewLocalAdaptiveTokenBucketLimit(store, rateLimitPerSec)
	}

	return nil, fmt.Errorf("unknown store type: %s", store.Type)
}

func WithGlobalRequestRateLimiter(store config.Store, rateLimitPerSec int) func(http.Handler) http.Handler {
	limitStore, err := NewLimitStore(store, rateLimitPerSec)
	if err != nil {
		panic(err)
	}
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			ctx := r.Context()
			if !limitStore.TryPassRequestLimit(ctx) {
				//logger := mdlogger.FromContext(r.Context())
				//logger.With(zap.Int("limit", rateLimitPerSec)).Warn("request rate limited")

				w.WriteHeader(http.StatusTooManyRequests)
				return
			}

			next.ServeHTTP(w, r)
		})
	}
}
