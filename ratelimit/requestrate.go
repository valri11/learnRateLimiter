package ratelimit

import (
	"context"
	"fmt"
	"net/http"
	"time"

	"github.com/valri11/learnRateLimiter/config"
	metricsApi "go.opentelemetry.io/otel/metric"
)

type LimitStore interface {
	TryPassRequestLimit(ctx context.Context) bool
}

func NewLimitStore(store config.Store) (LimitStore, error) {

	switch store.Type {
	case "localFixedWindow":
		return NewLocalFixedWindowLimit(store)
	case "redisFixedWindow":
		return NewRedisFixedWindowLimit(store)
	case "localSlidingWindow":
		return NewLocalSlidingWindowLimit(store)
	case "redisSlidingWindow":
		return NewRedisSlidingWindowLimit(store)
	case "redisTokenBucket":
		return NewRedisTokenBucketLimit(store)
	case "localAdaptiveTokenBucket":
		return NewLocalAdaptiveTokenBucketLimit(store)
	case "redisAdaptiveTokenBucket":
		return NewRedisAdaptiveTokenBucketLimit(store)
	}

	return nil, fmt.Errorf("unknown store type: %s", store.Type)
}

func WithRequestRateLimiter(meter metricsApi.Meter, store config.Store) func(http.Handler) http.Handler {

	rateLimiterDuration, err := meter.Int64Histogram(
		"ratelimiter_check_duration",
		metricsApi.WithDescription("Rate limiter check duration"),
		metricsApi.WithUnit("ms"),
		metricsApi.WithExplicitBucketBoundaries([]float64{1, 2, 3, 4, 5, 10, 20, 100, 250}...),
	)
	if err != nil {
		panic(err)
	}

	limitStore, err := NewLimitStore(store)
	if err != nil {
		panic(err)
	}
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			ctx := r.Context()

			requestStartTime := time.Now()

			if !limitStore.TryPassRequestLimit(ctx) {
				//logger := mdlogger.FromContext(r.Context())
				//logger.With(zap.Int("limit", rateLimitPerSec)).Warn("request rate limited")

				w.WriteHeader(http.StatusTooManyRequests)

				elapsedTime := time.Since(requestStartTime).Milliseconds()
				rateLimiterDuration.Record(ctx, elapsedTime)
				return
			}

			elapsedTime := time.Since(requestStartTime).Milliseconds()
			rateLimiterDuration.Record(ctx, elapsedTime)

			next.ServeHTTP(w, r)
		})
	}
}
