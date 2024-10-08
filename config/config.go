package config

type Store struct {
	Type       string
	Parameters map[string]string
}

type RateLimits struct {
	ServiceConcurrentRequestAllowance int
	Store                             Store
}

type ServerConfig struct {
	Port               int
	DisableTLS         bool
	TlsCertFile        string
	TlsCertKeyFile     string
	EnableTelemetry    bool
	TelemetryCollector string
	RateLimits         RateLimits
}

type Configuration struct {
	Server ServerConfig
}
