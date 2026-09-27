// Package config loads environment variables and returns a validated
// application configuration.
package config

import (
	"fmt"
	"os"
	"strconv"
	"strings"
)

// Config holds every env-var-driven setting for the workers host.
type Config struct {
	// DiscoveryIntervalSeconds is how often the discovery job fires.
	// Must be > 0.
	DiscoveryIntervalSeconds int
	// DiscoveryProbeTimeoutMs is the per-IP probe timeout in milliseconds.
	// Must be > 0.
	DiscoveryProbeTimeoutMs int
	// DiscoveryWorkerPoolSize is the max goroutines probing IPs in parallel.
	// Must be > 0.
	DiscoveryWorkerPoolSize int
	// LogLevel is one of debug|info|warn|error.
	LogLevel string
	// PGHost is the Postgres hostname. Default 127.0.0.1 (host loopback
	// under network_mode: host). Must be non-empty.
	PGHost string
	// PGPort is the Postgres TCP port. Must be in [1, 65535].
	PGPort int
	// PGDatabase is the database name. Must be non-empty.
	PGDatabase string
	// PGUser is the role name. Must be non-empty.
	PGUser string
	// PGPassword is the role password. Must be non-empty.
	PGPassword string
}

// Load reads the documented env vars and returns a validated Config.
// Returns an error describing which value is invalid if validation fails.
func Load() (*Config, error) {
	cfg := &Config{
		DiscoveryIntervalSeconds: envInt("DISCOVERY_INTERVAL_SECONDS", 60),
		DiscoveryProbeTimeoutMs:  envInt("DISCOVERY_PROBE_TIMEOUT_MS", 1500),
		DiscoveryWorkerPoolSize:  envInt("DISCOVERY_WORKER_POOL_SIZE", 64),
		LogLevel:                 envStr("LOG_LEVEL", "info"),
		PGHost:                   envStr("PG_HOST", "127.0.0.1"),
		PGPort:                   envInt("PG_PORT", 5432),
		PGDatabase:               envStr("PG_DATABASE", "witsaba"),
		PGUser:                   envStr("PG_USER", "pg-worker"),
		PGPassword:               envStr("PG_PASSWORD", ""),
	}

	if err := validate(cfg); err != nil {
		return nil, err
	}
	return cfg, nil
}

func validate(cfg *Config) error {
	if cfg.DiscoveryIntervalSeconds <= 0 {
		return fmt.Errorf("DISCOVERY_INTERVAL_SECONDS must be > 0, got %d", cfg.DiscoveryIntervalSeconds)
	}
	if cfg.DiscoveryProbeTimeoutMs <= 0 {
		return fmt.Errorf("DISCOVERY_PROBE_TIMEOUT_MS must be > 0, got %d", cfg.DiscoveryProbeTimeoutMs)
	}
	if cfg.DiscoveryWorkerPoolSize <= 0 {
		return fmt.Errorf("DISCOVERY_WORKER_POOL_SIZE must be > 0, got %d", cfg.DiscoveryWorkerPoolSize)
	}
	switch strings.ToLower(cfg.LogLevel) {
	case "debug", "info", "warn", "error", "":
		// valid
	default:
		return fmt.Errorf("LOG_LEVEL must be one of debug|info|warn|error, got %q", cfg.LogLevel)
	}
	if cfg.PGHost == "" {
		return fmt.Errorf("PG_HOST must be non-empty")
	}
	if cfg.PGPort < 1 || cfg.PGPort > 65535 {
		return fmt.Errorf("PG_PORT must be in [1, 65535], got %d", cfg.PGPort)
	}
	if cfg.PGDatabase == "" {
		return fmt.Errorf("PG_DATABASE must be non-empty")
	}
	if cfg.PGUser == "" {
		return fmt.Errorf("PG_USER must be non-empty")
	}
	if cfg.PGPassword == "" {
		return fmt.Errorf("PG_PASSWORD must be non-empty")
	}
	return nil
}

func envInt(key string, fallback int) int {
	if v, ok := os.LookupEnv(key); ok {
		if i, err := strconv.Atoi(v); err == nil {
			return i
		}
	}
	return fallback
}

func envStr(key string, fallback string) string {
	if v, ok := os.LookupEnv(key); ok && v != "" {
		return v
	}
	return fallback
}
