package config_test

import (
	"os"
	"testing"

	"github.com/witsaba/local-home-assitant/services/workers/internal/infrastructure/config"
)

func TestLoad_Defaults(t *testing.T) {
	// Clear all relevant env vars.
	t.Setenv("DISCOVERY_INTERVAL_SECONDS", "")
	t.Setenv("DISCOVERY_PROBE_TIMEOUT_MS", "")
	t.Setenv("DISCOVERY_WORKER_POOL_SIZE", "")
	t.Setenv("LOG_LEVEL", "")

	cfg, err := config.Load()
	if err != nil {
		t.Fatalf("Load() returned error with defaults: %v", err)
	}
	if cfg.DiscoveryIntervalSeconds != 60 {
		t.Errorf("expected default DiscoveryIntervalSeconds=60, got %d", cfg.DiscoveryIntervalSeconds)
	}
	if cfg.DiscoveryProbeTimeoutMs != 1500 {
		t.Errorf("expected default DiscoveryProbeTimeoutMs=1500, got %d", cfg.DiscoveryProbeTimeoutMs)
	}
	if cfg.DiscoveryWorkerPoolSize != 64 {
		t.Errorf("expected default DiscoveryWorkerPoolSize=64, got %d", cfg.DiscoveryWorkerPoolSize)
	}
	if cfg.LogLevel != "info" {
		t.Errorf("expected default LogLevel=info, got %q", cfg.LogLevel)
	}
}

func TestLoad_EnvOverride(t *testing.T) {
	t.Setenv("DISCOVERY_INTERVAL_SECONDS", "30")
	t.Setenv("DISCOVERY_PROBE_TIMEOUT_MS", "2000")
	t.Setenv("DISCOVERY_WORKER_POOL_SIZE", "128")
	t.Setenv("LOG_LEVEL", "debug")

	cfg, err := config.Load()
	if err != nil {
		t.Fatalf("Load() returned error with env override: %v", err)
	}
	if cfg.DiscoveryIntervalSeconds != 30 {
		t.Errorf("expected DiscoveryIntervalSeconds=30, got %d", cfg.DiscoveryIntervalSeconds)
	}
	if cfg.DiscoveryProbeTimeoutMs != 2000 {
		t.Errorf("expected DiscoveryProbeTimeoutMs=2000, got %d", cfg.DiscoveryProbeTimeoutMs)
	}
	if cfg.DiscoveryWorkerPoolSize != 128 {
		t.Errorf("expected DiscoveryWorkerPoolSize=128, got %d", cfg.DiscoveryWorkerPoolSize)
	}
	if cfg.LogLevel != "debug" {
		t.Errorf("expected LogLevel=debug, got %q", cfg.LogLevel)
	}
}

func TestLoad_InvalidInterval(t *testing.T) {
	t.Setenv("DISCOVERY_INTERVAL_SECONDS", "0")
	t.Setenv("DISCOVERY_PROBE_TIMEOUT_MS", "")
	t.Setenv("DISCOVERY_WORKER_POOL_SIZE", "")
	t.Setenv("LOG_LEVEL", "")

	_, err := config.Load()
	if err == nil {
		t.Error("expected error for zero interval, got nil")
	}
}

func TestLoad_InvalidTimeout(t *testing.T) {
	t.Setenv("DISCOVERY_INTERVAL_SECONDS", "")
	t.Setenv("DISCOVERY_PROBE_TIMEOUT_MS", "-1")
	t.Setenv("DISCOVERY_WORKER_POOL_SIZE", "")
	t.Setenv("LOG_LEVEL", "")

	_, err := config.Load()
	if err == nil {
		t.Error("expected error for negative timeout, got nil")
	}
}

func TestLoad_InvalidPoolSize(t *testing.T) {
	t.Setenv("DISCOVERY_INTERVAL_SECONDS", "")
	t.Setenv("DISCOVERY_PROBE_TIMEOUT_MS", "")
	t.Setenv("DISCOVERY_WORKER_POOL_SIZE", "0")
	t.Setenv("LOG_LEVEL", "")

	_, err := config.Load()
	if err == nil {
		t.Error("expected error for zero pool size, got nil")
	}
}

func TestLoad_InvalidLogLevel(t *testing.T) {
	t.Setenv("DISCOVERY_INTERVAL_SECONDS", "")
	t.Setenv("DISCOVERY_PROBE_TIMEOUT_MS", "")
	t.Setenv("DISCOVERY_WORKER_POOL_SIZE", "")
	t.Setenv("LOG_LEVEL", "not-a-level")

	_, err := config.Load()
	if err == nil {
		t.Error("expected error for invalid log level, got nil")
	}
}

func TestLoad_LogLevelCaseInsensitive(t *testing.T) {
	for _, lvl := range []string{"DEBUG", "Info", "WARN", "ErRoR"} {
		lvl := lvl
		t.Run(lvl, func(t *testing.T) {
			t.Setenv("DISCOVERY_INTERVAL_SECONDS", "")
			t.Setenv("DISCOVERY_PROBE_TIMEOUT_MS", "")
			t.Setenv("DISCOVERY_WORKER_POOL_SIZE", "")
			t.Setenv("LOG_LEVEL", lvl)

			cfg, err := config.Load()
			if err != nil {
				t.Errorf("Load() with LOG_LEVEL=%q returned error: %v", lvl, err)
			}
			if cfg == nil {
				t.Errorf("Load() with LOG_LEVEL=%q returned nil config", lvl)
			}
		})
	}
}

func TestLoad_EnvVarUnset_FallsBackToDefault(t *testing.T) {
	// Ensure unset env vars fall back to defaults.
	_ = os.Unsetenv("DISCOVERY_INTERVAL_SECONDS")
	_ = os.Unsetenv("DISCOVERY_PROBE_TIMEOUT_MS")
	_ = os.Unsetenv("DISCOVERY_WORKER_POOL_SIZE")
	_ = os.Unsetenv("LOG_LEVEL")

	cfg, err := config.Load()
	if err != nil {
		t.Fatalf("Load() with unset env vars returned error: %v", err)
	}
	if cfg.DiscoveryIntervalSeconds != 60 {
		t.Errorf("expected default interval=60 when env var is unset, got %d", cfg.DiscoveryIntervalSeconds)
	}
}
