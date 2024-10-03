/*
Copyright © 2024 NAME HERE <EMAIL ADDRESS>
*/
package cmd

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"time"

	"github.com/justinas/alice"
	"github.com/prometheus/client_golang/prometheus/promhttp"
	"github.com/spf13/cobra"
	"github.com/spf13/viper"
	"go.opentelemetry.io/contrib/instrumentation/net/http/otelhttp"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/metric"
	"go.opentelemetry.io/otel/trace"
	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"

	mdlogger "github.com/valri11/go-servicepack/logger"
	"github.com/valri11/go-servicepack/middleware/cors"
	"github.com/valri11/go-servicepack/telemetry"

	"github.com/valri11/learnRateLimiter/config"
	"github.com/valri11/learnRateLimiter/metrics"
	appmetrics "github.com/valri11/learnRateLimiter/metrics"
	"github.com/valri11/learnRateLimiter/ratelimit"
)

const (
	serviceName = "testratelimiter"
)

// serverCmd represents the server command
var serverCmd = &cobra.Command{
	Use:   "server",
	Short: "A brief description of your command",
	Long: `A longer description that spans multiple lines and likely contains examples
and usage of using your command. For example:

Cobra is a CLI library for Go that empowers applications.
This application is a tool to generate the needed files
to quickly create a Cobra application.`,
	Run: doServerCmd,
}

type srvHandler struct {
	cfg     config.Configuration
	tracer  trace.Tracer
	metrics *appmetrics.AppMetrics
	meter   metric.Meter
}

func init() {
	rootCmd.AddCommand(serverCmd)

	serverCmd.Flags().BoolP("disable-tls", "", false, "development mode (http on loclahost)")
	serverCmd.Flags().String("tls-cert", "", "TLS certificate file")
	serverCmd.Flags().String("tls-cert-key", "", "TLS certificate key file")
	serverCmd.Flags().Int("port", 8060, "service port to listen")
	serverCmd.Flags().BoolP("enable-telemetry", "", false, "enable telemetry publishing")
	serverCmd.Flags().String("telemetry-collector", "localhost:4317", "open telemetry grpc collector")

	viper.BindPFlag("server.port", serverCmd.Flags().Lookup("port"))
	viper.BindPFlag("server.disabletls", serverCmd.Flags().Lookup("disable-tls"))
	viper.BindPFlag("server.tlscertfile", serverCmd.Flags().Lookup("tls-cert"))
	viper.BindPFlag("server.tlscertkeyfile", serverCmd.Flags().Lookup("tls-cert-key"))
	viper.BindPFlag("server.enabletelemetry", serverCmd.Flags().Lookup("enable-telemetry"))
	viper.BindPFlag("server.telemetrycollector", serverCmd.Flags().Lookup("telemetry-collector"))
}

func newWebSrvHandler(cfg config.Configuration) (*srvHandler, error) {
	tracer := otel.Tracer(serviceName)

	meter, err := appmetrics.NewMeterProvider(serviceName)
	if err != nil {
		return nil, err
	}

	metrics, err := appmetrics.NewAppMetrics(meter)
	if err != nil {
		return nil, err
	}
	srv := srvHandler{
		cfg:     cfg,
		tracer:  tracer,
		meter:   meter,
		metrics: metrics,
	}

	return &srv, nil
}

func WithLogger(logger *zap.Logger) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			ctx := mdlogger.NewContext(r.Context(), logger)
			//startedAt := time.Now()
			r = r.WithContext(ctx)

			next.ServeHTTP(w, r)

			/*
				logger.Debug("request",
					zap.String("method", r.Method),
					zap.String("path", r.URL.Path),
					zap.String("proto", r.Proto),
					zap.String("remoteAddr", r.RemoteAddr),
					// zap.Int("status", wl.status),
					zap.Float64("latency", float64(time.Since(startedAt))/float64(time.Microsecond)),
				)
			*/
		})
	}
}

func WithOtelTracerContext(tracer trace.Tracer) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			ctx := telemetry.NewContextWithTracer(r.Context(), tracer)
			r = r.WithContext(ctx)

			next.ServeHTTP(w, r)
		})
	}
}

func doServerCmd(cmd *cobra.Command, args []string) {
	logger, err := mdlogger.New(zapcore.DebugLevel, true)
	if err != nil {
		log.Fatalf("ERR: %v", err)
		return
	}
	defer logger.Sync()

	var cfg config.Configuration
	err = viper.Unmarshal(&cfg)
	if err != nil {
		log.Fatalf("ERR: %v", err)
		return
	}
	logger.Debug("config", zap.Any("cfg", cfg))

	if !cfg.Server.DisableTLS {
		if cfg.Server.TlsCertFile == "" || cfg.Server.TlsCertKeyFile == "" {
			fmt.Println("must provide TLS key and certificate")
			return
		}
	}

	ctx := context.Background()

	shutdown, err := telemetry.InitProvider(ctx, cfg.Server.EnableTelemetry, serviceName, cfg.Server.TelemetryCollector)
	if err != nil {
		log.Fatal(err)
	}
	defer func() {
		if err := shutdown(ctx); err != nil {
			log.Fatal("failed to shutdown TracerProvider: %w", err)
		}
	}()

	h, err := newWebSrvHandler(cfg)
	if err != nil {
		log.Fatalf("ERR: %v", err)
		return
	}

	mux := http.NewServeMux()

	mwChain := []alice.Constructor{
		WithLogger(logger),
		cors.CORS,
		appmetrics.WithMetrics(h.metrics),
		WithOtelTracerContext(h.tracer),
		metrics.WithOtelMeterContext(h.meter),
		ratelimit.WithRequestRateLimiter(
			h.meter,
			cfg.Server.RateLimits.Store),
		ratelimit.WithConcurrentRequestRateLimiter(h.meter, int32(cfg.Server.RateLimits.ServiceConcurrentRequestAllowance)),
	}
	handlerChain := alice.New(mwChain...).Then

	mux.Handle("/livez",
		handlerChain(
			otelhttp.NewHandler(http.HandlerFunc(h.livezHandler), "livez")))

	mux.Handle("/metrics", promhttp.Handler())

	// start server listen with error handling
	srv := &http.Server{
		Addr:         fmt.Sprintf("0.0.0.0:%d", cfg.Server.Port),
		Handler:      mux,
		IdleTimeout:  time.Minute,
		ReadTimeout:  10 * time.Second,
		WriteTimeout: 30 * time.Second,
	}

	logger.Info("server started", zap.Int("port", cfg.Server.Port))
	if cfg.Server.DisableTLS {
		err = srv.ListenAndServe()
	} else {
		err = srv.ListenAndServeTLS(cfg.Server.TlsCertFile, cfg.Server.TlsCertKeyFile)
	}
	if err != nil {
		log.Fatal(err)
	}
}

func (h *srvHandler) livezHandler(w http.ResponseWriter, r *http.Request) {
	//logger := mdlogger.FromContext(r.Context())
	//logger.Debug("livez")

	res := struct {
		Status string `json:"status"`
	}{
		Status: "ok",
	}

	out, err := json.Marshal(res)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	w.Write(out)
}
