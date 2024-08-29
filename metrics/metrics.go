package metrics

import (
	"net/http"
	"time"

	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/exporters/prometheus"
	metricsApi "go.opentelemetry.io/otel/metric"
	"go.opentelemetry.io/otel/sdk/metric"
	"go.opentelemetry.io/otel/sdk/resource"
	semconv "go.opentelemetry.io/otel/semconv/v1.4.0"
)

type AppMetrics struct {
	ReqCounter  metricsApi.Int64Counter
	ReqDuration metricsApi.Int64Histogram
	ErrCounter  metricsApi.Int64Counter
}

func NewAppMetrics(meter metricsApi.Meter) (*AppMetrics, error) {
	reqCounter, err := meter.Int64Counter("req_cnt", metricsApi.WithDescription("request counter"))
	if err != nil {
		return nil, err
	}
	reqDuration, err := meter.Int64Histogram(
		"req_duration",
		metricsApi.WithDescription("Requests handler end to end duration"),
		metricsApi.WithUnit("ms"),
	)
	if err != nil {
		return nil, err
	}
	errCounter, err := meter.Int64Counter("err_cnt", metricsApi.WithDescription("service error counter"))
	if err != nil {
		return nil, err
	}

	m := AppMetrics{
		ReqCounter:  reqCounter,
		ReqDuration: reqDuration,
		ErrCounter:  errCounter,
	}
	return &m, nil
}

func NewMeterProvider(serviceName string) (metricsApi.Meter, error) {
	exporter, err := prometheus.New()
	if err != nil {
		return nil, err
	}

	resources := resource.NewWithAttributes(
		semconv.SchemaURL,
		semconv.ServiceNameKey.String(serviceName),
	)

	provider := metric.NewMeterProvider(
		metric.WithResource(resources),
		metric.WithReader(exporter))

	return provider.Meter(serviceName), nil
}

type CustomResponseWriter struct {
	responseWriter http.ResponseWriter
	StatusCode     int
}

func ExtendResponseWriter(w http.ResponseWriter) *CustomResponseWriter {
	return &CustomResponseWriter{w, 0}
}

func (w *CustomResponseWriter) Write(b []byte) (int, error) {
	return w.responseWriter.Write(b)
}

func (w *CustomResponseWriter) Header() http.Header {
	return w.responseWriter.Header()
}

func (w *CustomResponseWriter) WriteHeader(statusCode int) {
	// receive status code from this method
	if w.StatusCode != 0 {
		// status code already set by some handler
		return
	}
	w.StatusCode = statusCode
	w.responseWriter.WriteHeader(statusCode)
}

func (w *CustomResponseWriter) Done() {
	// if the `w.WriteHeader` wasn't called, set status code to 200 OK
	if w.StatusCode == 0 {
		w.StatusCode = http.StatusOK
	}
}

func WithMetrics(metrics *AppMetrics) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			ctx := r.Context()
			requestStartTime := time.Now()

			ew := ExtendResponseWriter(w)
			next.ServeHTTP(ew, r)
			ew.Done()

			if ew.StatusCode >= http.StatusBadRequest {
				metrics.ErrCounter.Add(ctx, 1,
					metricsApi.WithAttributes(
						attribute.Int("status", ew.StatusCode)))
			}

			metrics.ReqCounter.Add(ctx, 1,
				metricsApi.WithAttributes(
					attribute.Int("status", ew.StatusCode)))

			elapsedTime := time.Since(requestStartTime).Milliseconds()
			metrics.ReqDuration.Record(ctx, elapsedTime)
		})
	}
}
