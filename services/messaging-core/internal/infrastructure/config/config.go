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
)

// Defaults applied when the corresponding env var is unset or empty.
const (
	defaultHost      = "127.0.0.1"
	defaultPort      = 4222
	defaultLogLevel  = "info"
	defaultDataDir   = ""
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
}

// Load reads configuration from the process environment and returns
// a validated Config. It never panics; all errors are returned to
// the caller.
func Load() (Config, error) {
	cfg := Config{
		Host:     getEnv("NATS_HOST", defaultHost),
		LogLevel: strings.ToLower(getEnv("LOG_LEVEL", defaultLogLevel)),
		DataDir:  getEnv("NATS_DATA_DIR", defaultDataDir),
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
	return nil
}
