package ratelimit

import (
	"net/http"
	"sync/atomic"

	metricsApi "go.opentelemetry.io/otel/metric"

	mdlogger "github.com/valri11/go-servicepack/logger"
)

func WithConcurrentRequestRateLimiter(meter metricsApi.Meter, concurrentRequestAllowance int32) func(http.Handler) http.Handler {
	inProgressRequestCounter := int32(0)
	concurrentReqMeter, err := meter.Int64Histogram(
		"concurrent_req",
		metricsApi.WithDescription("Concurrent requests"),
		metricsApi.WithExplicitBucketBoundaries([]float64{1, 3, 9, 15, 30, 90, 300}...),
	)
	if err != nil {
		panic(err)
	}

	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			ctx := r.Context()
			logger := mdlogger.FromContext(ctx)

			if inProgressRequestCounter+1 > concurrentRequestAllowance {
				logger.Warn("breached concurrent rate limit")

				w.WriteHeader(http.StatusServiceUnavailable)
				return
			}

			atomic.AddInt32(&inProgressRequestCounter, 1)
			concurrentReqMeter.Record(ctx, int64(inProgressRequestCounter))

			next.ServeHTTP(w, r)

			//logger.With(zap.Int32("concurrent_req", inProgressRequestCounter)).Debug("concurrent requests")

			atomic.AddInt32(&inProgressRequestCounter, -1)
		})
	}
}
