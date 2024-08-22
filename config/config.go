package config

type ServerConfig struct {
	Port                     int
	DisableTLS               bool
	TlsCertFile              string
	TlsCertKeyFile           string
	EnableTelemetry          bool
	TelemetryCollector       string
	GlobalReqRateLimitPerSec int
}

type Configuration struct {
	Server ServerConfig
}
