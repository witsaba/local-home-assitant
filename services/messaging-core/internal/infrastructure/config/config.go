// Package config loads the messaging-core runtime configuration
// from environment variables. Defaults are chosen so the service
// runs cleanly on a developer laptop without any setup.
package config

import (
	"errors"
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"
)

// Defaults applied when the corresponding env var is unset or empty.
const (
	defaultHost       = "127.0.0.1"
	defaultPort      = 4222
	defaultLogLevel  = "info"
	defaultDataDir   = ""
	defaultSTREAMPort = 8080
)

// Config is the resolved, validated runtime configuration.
type Config struct {
	// Host the embedded NATS server binds to. Defaults to 127.0.0.1.
	Host string

	// Port the embedded NATS server listens on. Defaults to 4222.
	Port int

	// LogLevel controls the zap logger verbosity. One of:
	// "debug", "info", "warn", "error". Defaults to "info".
	LogLevel string

	// DataDir is reserved for a future JetStream store path. Optional.
	DataDir string

	// STREAMPort is the HTTP/WS port for the camera streaming gateway.
	// Defaults to 8080.
	STREAMPort int

	// Postgres connection parameters.
	PGHost            string
	PGPort            int
	PGDatabase        string
	PGUser            string
	PGPassword        string
	PGMaxConns        int
	PGMinConns        int
	PGMaxConnLifetime time.Duration
	PGMaxConnIdleTime time.Duration
}

// Load reads configuration from the process environment and returns
// a validated Config. It never panics; all errors are returned to
// the caller.
func Load() (Config, error) {
	cfg := Config{
		Host:     getEnv("NATS_HOST", defaultHost),
		LogLevel: strings.ToLower(getEnv("LOG_LEVEL", defaultLogLevel)),
		DataDir:  getEnv("NATS_DATA_DIR", defaultDataDir),

		STREAMPort: envInt("STREAM_PORT", defaultSTREAMPort),

		// Postgres defaults mirror the workers service.
		PGHost:     envStr("PG_HOST", "127.0.0.1"),
		PGPort:     envInt("PG_PORT", 5432),
		PGDatabase: envStr("PG_DATABASE", "witsaba"),
		PGUser:     envStr("PG_USER", "pg-messaging-core"),
		PGPassword: envStr("PG_PASSWORD", ""),

		// Pool tuning defaults (0 = db package applies safe defaults).
		PGMaxConns:        envInt("PG_MAX_CONNS", 0),
		PGMinConns:        envInt("PG_MIN_CONNS", 0),
		PGMaxConnLifetime: envDuration("PG_MAX_CONN_LIFETIME", 0),
		PGMaxConnIdleTime: envDuration("PG_MAX_CONN_IDLE_TIME", 0),
	}

	port, err := parsePort(getEnv("NATS_PORT", strconv.Itoa(defaultPort)))
	if err != nil {
		return Config{}, fmt.Errorf("NATS_PORT: %w", err)
	}
	cfg.Port = port

	if err := validate(cfg); err != nil {
		return Config{}, err
	}
	return cfg, nil
}

func getEnv(key, fallback string) string {
	if v, ok := os.LookupEnv(key); ok && v != "" {
		return v
	}
	return fallback
}

func parsePort(raw string) (int, error) {
	p, err := strconv.Atoi(raw)
	if err != nil {
		return 0, fmt.Errorf("not a number: %q (%w)", raw, err)
	}
	if p < 0 || p > 65535 {
		return 0, fmt.Errorf("port out of range: %d", p)
	}
	return p, nil
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

func validate(cfg Config) error {
	if cfg.Host == "" {
		return errors.New("NATS_HOST: must not be empty")
	}
	switch cfg.LogLevel {
	case "debug", "info", "warn", "error":
	default:
		return fmt.Errorf("LOG_LEVEL: unsupported value %q (want debug|info|warn|error)", cfg.LogLevel)
	}
	// Port 0 is acceptable for tests (lets the OS pick a free port).
	// PG_HOST default is "127.0.0.1" so empty is unreachable here.
	if cfg.PGPort < 1 || cfg.PGPort > 65535 {
		return fmt.Errorf("PG_PORT: must be in [1, 65535], got %d", cfg.PGPort)
	}
	if cfg.PGDatabase == "" {
		return errors.New("PG_DATABASE: must not be empty")
	}
	if cfg.PGUser == "" {
		return errors.New("PG_USER: must not be empty")
	}
	if cfg.PGPassword == "" {
		return errors.New("PG_PASSWORD: must not be empty")
	}
	if cfg.STREAMPort < 1 || cfg.STREAMPort > 65535 {
		return fmt.Errorf("STREAM_PORT: must be in [1, 65535], got %d", cfg.STREAMPort)
	}
	return nil
}
