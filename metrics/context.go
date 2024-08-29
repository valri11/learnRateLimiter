package metrics

import (
	"context"
	"net/http"

	"go.opentelemetry.io/otel/metric"
)

type ctxMeterKey struct{}

func MeterFromContext(ctx context.Context) (metric.Meter, bool) {
	t, ok := ctx.Value(ctxMeterKey{}).(metric.Meter)
	return t, ok
}

func MustMeterFromContext(ctx context.Context) metric.Meter {
	t, ok := ctx.Value(ctxMeterKey{}).(metric.Meter)
	if !ok {
		panic("otel tracer not set in context")
	}
	return t
}

func NewContextWithMeter(parent context.Context, m metric.Meter) context.Context {
	return context.WithValue(parent, ctxMeterKey{}, m)
}

func WithOtelMeterContext(meter metric.Meter) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			ctx := NewContextWithMeter(r.Context(), meter)
			r = r.WithContext(ctx)

			next.ServeHTTP(w, r)
		})
	}
}
