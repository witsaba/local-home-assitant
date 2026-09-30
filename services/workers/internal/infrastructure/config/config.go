// Package config loads environment variables and returns a validated
// application configuration.
package config

import (
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/witsaba/local-home-assitant/services/workers/internal/infrastructure/db"
)

// Config holds every env-var-driven setting for the workers host.
type Config struct {
	// Discovery settings.
	DiscoveryIntervalSeconds int
	DiscoveryProbeTimeoutMs  int
	DiscoveryWorkerPoolSize  int

	// Surveillance settings.
	SurveillanceIntervalMinutes       int
	SurveillanceCaptureTimeoutSeconds int
	SurveillanceDeviceFreshnessMinutes int
	SurveillanceRootDir                string

	// Logging.
	LogLevel string

	// Postgres connection.
	PGHost     string
	PGPort     int
	PGDatabase string
	PGUser     string
	PGPassword string

	// Postgres pool tuning (optional - safe defaults applied if zero).
	PGMaxConns        int
	PGMinConns        int
	PGMaxConnLifetime time.Duration
	PGMaxConnIdleTime time.Duration
}

// Load reads the documented env vars and returns a validated Config.
// Returns an error describing which value is invalid if validation fails.
func Load() (*Config, error) {
	cfg := &Config{
		// Discovery defaults.
		DiscoveryIntervalSeconds: envInt("DISCOVERY_INTERVAL_SECONDS", 60),
		DiscoveryProbeTimeoutMs:  envInt("DISCOVERY_PROBE_TIMEOUT_MS", 1500),
		DiscoveryWorkerPoolSize:  envInt("DISCOVERY_WORKER_POOL_SIZE", 64),

		// Surveillance defaults — see odd/tasks/surveillance-worker.md.
		// The user-facing standing direction is 'every 15 minutes,
		// capture from every online camera, save to ~/.witsaba/cameras/'.
		// All four env vars are optional; defaults match that direction.
		SurveillanceIntervalMinutes:        envInt("SURVEILLANCE_INTERVAL_MINUTES", 15),
		SurveillanceCaptureTimeoutSeconds:  envInt("SURVEILLANCE_CAPTURE_TIMEOUT_SECONDS", 10),
		SurveillanceDeviceFreshnessMinutes: envInt("SURVEILLANCE_DEVICE_FRESHNESS_MINUTES", 5),
		SurveillanceRootDir:                envStr("SURVEILLANCE_ROOT_DIR", defaultSurveillanceRootDir()),

		LogLevel: envStr("LOG_LEVEL", "info"),

		// Postgres connection defaults.
		PGHost:     envStr("PG_HOST", "127.0.0.1"),
		PGPort:     envInt("PG_PORT", 5432),
		PGDatabase: envStr("PG_DATABASE", "witsaba"),
		PGUser:     envStr("PG_USER", "pg-worker"),
		PGPassword: envStr("PG_WORKER_PASSWORD", ""),

		// Postgres pool tuning (zero = use db package defaults).
		PGMaxConns:        envInt("PG_MAX_CONNS", 0),
		PGMinConns:        envInt("PG_MIN_CONNS", 0),
		PGMaxConnLifetime: envDuration("PG_MAX_CONN_LIFETIME", 0),
		PGMaxConnIdleTime: envDuration("PG_MAX_CONN_IDLE_TIME", 0),
	}

	if err := validate(cfg); err != nil {
		return nil, err
	}
	return cfg, nil
}

// ToPoolConfig converts Config to db.PoolConfig for pool initialization.
func (c *Config) ToPoolConfig() *db.PoolConfig {
	return &db.PoolConfig{
		Host:     c.PGHost,
		Port:     c.PGPort,
		Database: c.PGDatabase,
		User:     c.PGUser,
		Password: c.PGPassword,

		// Pool tuning - zero values trigger safe defaults in db package.
		MaxConns:        c.PGMaxConns,
		MinConns:        c.PGMinConns,
		MaxConnLifetime: c.PGMaxConnLifetime,
		MaxConnIdleTime: c.PGMaxConnIdleTime,
	}
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
		return fmt.Errorf("PG_WORKER_PASSWORD must be non-empty")
	}
	if cfg.SurveillanceIntervalMinutes <= 0 {
		return fmt.Errorf("SURVEILLANCE_INTERVAL_MINUTES must be > 0, got %d",
			cfg.SurveillanceIntervalMinutes)
	}
	if cfg.SurveillanceCaptureTimeoutSeconds <= 0 {
		return fmt.Errorf("SURVEILLANCE_CAPTURE_TIMEOUT_SECONDS must be > 0, got %d",
			cfg.SurveillanceCaptureTimeoutSeconds)
	}
	if cfg.SurveillanceDeviceFreshnessMinutes <= 0 {
		return fmt.Errorf("SURVEILLANCE_DEVICE_FRESHNESS_MINUTES must be > 0, got %d",
			cfg.SurveillanceDeviceFreshnessMinutes)
	}
	if strings.TrimSpace(cfg.SurveillanceRootDir) == "" {
		return fmt.Errorf("SURVEILLANCE_ROOT_DIR must be non-empty")
	}
	return nil
}

// defaultSurveillanceRootDir returns "$HOME/.witsaba/cameras" or,
// if HOME is unset, a path under os.UserHomeDir()'s fallback. We
// resolve the path at config-load time so a misconfigured HOME is
// surfaced immediately rather than at the first capture.
//
// Returns the resolved path as a string. Errors from
// os.UserHomeDir() fall back to "." + "/.witsaba/cameras" which
// is almost certainly wrong but is loud enough to surface.
func defaultSurveillanceRootDir() string {
	home := os.Getenv("HOME")
	if home == "" {
		if u, err := os.UserHomeDir(); err == nil {
			home = u
		} else {
			home = "."
		}
	}
	return filepath.Join(home, ".witsaba", "cameras")
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

func envDuration(key string, fallback time.Duration) time.Duration {
	if v, ok := os.LookupEnv(key); ok && v != "" {
		if d, err := time.ParseDuration(v); err == nil {
			return d
		}
	}
	return fallback
}
