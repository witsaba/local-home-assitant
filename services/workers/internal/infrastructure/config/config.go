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
}

// Load reads the documented env vars and returns a validated Config.
// Returns an error describing which value is invalid if validation fails.
func Load() (*Config, error) {
	cfg := &Config{
		DiscoveryIntervalSeconds: envInt("DISCOVERY_INTERVAL_SECONDS", 60),
		DiscoveryProbeTimeoutMs:  envInt("DISCOVERY_PROBE_TIMEOUT_MS", 1500),
		DiscoveryWorkerPoolSize:  envInt("DISCOVERY_WORKER_POOL_SIZE", 64),
		LogLevel:                 envStr("LOG_LEVEL", "info"),
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
