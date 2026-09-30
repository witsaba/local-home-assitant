package config_test

import (
	"strings"
	"testing"

	"github.com/witsaba/local-home-assitant/services/workers/internal/infrastructure/config"
)

func TestLoad_Defaults(t *testing.T) {
	// Clear all relevant env vars. Set PG_WORKER_PASSWORD to a non-empty value
	// so validation passes; the workers config requires a password and
	// the operator-supplied compose env fills it in production.
	resetEnv(t)

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
	if cfg.PGHost != "127.0.0.1" {
		t.Errorf("expected default PGHost=127.0.0.1, got %q", cfg.PGHost)
	}
	if cfg.PGPort != 5432 {
		t.Errorf("expected default PGPort=5432, got %d", cfg.PGPort)
	}
	if cfg.PGDatabase != "witsaba" {
		t.Errorf("expected default PGDatabase=witsaba, got %q", cfg.PGDatabase)
	}
	if cfg.PGUser != "pg-worker" {
		t.Errorf("expected default PGUser=pg-worker, got %q", cfg.PGUser)
	}
	if cfg.PGPassword != "test-pw" {
		t.Errorf("expected PGPassword from env, got %q", cfg.PGPassword)
	}
}

func TestLoad_EnvOverride(t *testing.T) {
	resetEnv(t)
	t.Setenv("DISCOVERY_INTERVAL_SECONDS", "30")
	t.Setenv("DISCOVERY_PROBE_TIMEOUT_MS", "2000")
	t.Setenv("DISCOVERY_WORKER_POOL_SIZE", "128")
	t.Setenv("LOG_LEVEL", "debug")
	t.Setenv("PG_HOST", "10.0.0.5")
	t.Setenv("PG_PORT", "6432")
	t.Setenv("PG_DATABASE", "witsaba_alt")
	t.Setenv("PG_USER", "pg-other")
	t.Setenv("PG_WORKER_PASSWORD", "secret")

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
	if cfg.PGHost != "10.0.0.5" {
		t.Errorf("expected PGHost=10.0.0.5, got %q", cfg.PGHost)
	}
	if cfg.PGPort != 6432 {
		t.Errorf("expected PGPort=6432, got %d", cfg.PGPort)
	}
	if cfg.PGDatabase != "witsaba_alt" {
		t.Errorf("expected PGDatabase=witsaba_alt, got %q", cfg.PGDatabase)
	}
	if cfg.PGUser != "pg-other" {
		t.Errorf("expected PGUser=pg-other, got %q", cfg.PGUser)
	}
	if cfg.PGPassword != "secret" {
		t.Errorf("expected PGPassword=secret, got %q", cfg.PGPassword)
	}
}

func TestLoad_InvalidInterval(t *testing.T) {
	resetEnv(t)
	t.Setenv("DISCOVERY_INTERVAL_SECONDS", "0")

	_, err := config.Load()
	if err == nil {
		t.Error("expected error for zero interval, got nil")
	}
}

func TestLoad_InvalidTimeout(t *testing.T) {
	resetEnv(t)
	t.Setenv("DISCOVERY_PROBE_TIMEOUT_MS", "-1")

	_, err := config.Load()
	if err == nil {
		t.Error("expected error for negative timeout, got nil")
	}
}

func TestLoad_InvalidPoolSize(t *testing.T) {
	resetEnv(t)
	t.Setenv("DISCOVERY_WORKER_POOL_SIZE", "0")

	_, err := config.Load()
	if err == nil {
		t.Error("expected error for zero pool size, got nil")
	}
}

func TestLoad_InvalidLogLevel(t *testing.T) {
	resetEnv(t)
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
			resetEnv(t)
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

func TestLoad_InvalidPGPort(t *testing.T) {
	resetEnv(t)
	t.Setenv("PG_PORT", "0")

	_, err := config.Load()
	if err == nil {
		t.Error("expected error for PG_PORT=0, got nil")
	}
}

func TestLoad_InvalidPGPortTooHigh(t *testing.T) {
	resetEnv(t)
	t.Setenv("PG_PORT", "70000")

	_, err := config.Load()
	if err == nil {
		t.Error("expected error for PG_PORT=70000, got nil")
	}
}

func TestLoad_InvalidPGPassword(t *testing.T) {
	resetEnv(t)
	t.Setenv("PG_WORKER_PASSWORD", "")

	_, err := config.Load()
	if err == nil {
		t.Error("expected error for empty PG_WORKER_PASSWORD, got nil")
	}
}

func TestLoad_EnvVarUnset_FallsBackToDefault(t *testing.T) {
	// Ensure unset env vars fall back to defaults.
	resetEnv(t)

	cfg, err := config.Load()
	if err != nil {
		t.Fatalf("Load() with unset env vars returned error: %v", err)
	}
	if cfg.DiscoveryIntervalSeconds != 60 {
		t.Errorf("expected default interval=60 when env var is unset, got %d", cfg.DiscoveryIntervalSeconds)
	}
}

// TestLoad_EnvVarEmpty_FallsBackToDefault documents that envStr
// treats an explicitly empty value as "use the default". An operator
// who sets PG_HOST="" in the environment gets the same default as one
// who omits the variable entirely. This matches the existing pattern
// for LOG_LEVEL and the discovery knobs.
func TestLoad_EnvVarEmpty_FallsBackToDefault(t *testing.T) {
	resetEnv(t)
	t.Setenv("PG_HOST", "")
	t.Setenv("PG_DATABASE", "")
	t.Setenv("PG_USER", "")
	// PG_WORKER_PASSWORD stays at the test-pw value so validation passes.

	cfg, err := config.Load()
	if err != nil {
		t.Fatalf("Load() with empty PG string vars returned error: %v", err)
	}
	if cfg.PGHost != "127.0.0.1" {
		t.Errorf("expected default PGHost=127.0.0.1, got %q", cfg.PGHost)
	}
	if cfg.PGDatabase != "witsaba" {
		t.Errorf("expected default PGDatabase=witsaba, got %q", cfg.PGDatabase)
	}
	if cfg.PGUser != "pg-worker" {
		t.Errorf("expected default PGUser=pg-worker, got %q", cfg.PGUser)
	}
}

// resetEnv clears every env var consumed by config.Load and sets
// PG_WORKER_PASSWORD to a non-empty value so validation passes. Each call
// installs the right env state for a single Load invocation.
func resetEnv(t *testing.T) {
	t.Helper()
	for _, k := range []string{
		"DISCOVERY_INTERVAL_SECONDS",
		"DISCOVERY_PROBE_TIMEOUT_MS",
		"DISCOVERY_WORKER_POOL_SIZE",
		"SURVEILLANCE_INTERVAL_MINUTES",
		"SURVEILLANCE_CAPTURE_TIMEOUT_SECONDS",
		"SURVEILLANCE_DEVICE_FRESHNESS_MINUTES",
		"SURVEILLANCE_ROOT_DIR",
		"LOG_LEVEL",
		"PG_HOST",
		"PG_PORT",
		"PG_DATABASE",
		"PG_USER",
		"PG_WORKER_PASSWORD",
	} {
		t.Setenv(k, "")
	}
	t.Setenv("PG_WORKER_PASSWORD", "test-pw")
}

func TestLoad_SurveillanceDefaults(t *testing.T) {
	resetEnv(t)

	cfg, err := config.Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.SurveillanceIntervalMinutes != 15 {
		t.Errorf("SurveillanceIntervalMinutes = %d, want 15", cfg.SurveillanceIntervalMinutes)
	}
	if cfg.SurveillanceCaptureTimeoutSeconds != 10 {
		t.Errorf("SurveillanceCaptureTimeoutSeconds = %d, want 10",
			cfg.SurveillanceCaptureTimeoutSeconds)
	}
	if cfg.SurveillanceDeviceFreshnessMinutes != 5 {
		t.Errorf("SurveillanceDeviceFreshnessMinutes = %d, want 5",
			cfg.SurveillanceDeviceFreshnessMinutes)
	}
	if cfg.SurveillanceRootDir == "" {
		t.Errorf("SurveillanceRootDir = empty, want non-empty default")
	}
	// The default should expand to ~/.witsaba/cameras.
	if !strings.HasSuffix(cfg.SurveillanceRootDir, "/.witsaba/cameras") {
		t.Errorf("SurveillanceRootDir = %q, want suffix '/.witsaba/cameras'",
			cfg.SurveillanceRootDir)
	}
}

func TestLoad_SurveillanceEnvOverrides(t *testing.T) {
	resetEnv(t)
	t.Setenv("SURVEILLANCE_INTERVAL_MINUTES", "30")
	t.Setenv("SURVEILLANCE_CAPTURE_TIMEOUT_SECONDS", "20")
	t.Setenv("SURVEILLANCE_DEVICE_FRESHNESS_MINUTES", "10")
	t.Setenv("SURVEILLANCE_ROOT_DIR", "/srv/witsaba/cameras")

	cfg, err := config.Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.SurveillanceIntervalMinutes != 30 {
		t.Errorf("SurveillanceIntervalMinutes = %d, want 30", cfg.SurveillanceIntervalMinutes)
	}
	if cfg.SurveillanceCaptureTimeoutSeconds != 20 {
		t.Errorf("SurveillanceCaptureTimeoutSeconds = %d, want 20",
			cfg.SurveillanceCaptureTimeoutSeconds)
	}
	if cfg.SurveillanceDeviceFreshnessMinutes != 10 {
		t.Errorf("SurveillanceDeviceFreshnessMinutes = %d, want 10",
			cfg.SurveillanceDeviceFreshnessMinutes)
	}
	if cfg.SurveillanceRootDir != "/srv/witsaba/cameras" {
		t.Errorf("SurveillanceRootDir = %q, want /srv/witsaba/cameras",
			cfg.SurveillanceRootDir)
	}
}

func TestLoad_InvalidSurveillanceInterval(t *testing.T) {
	resetEnv(t)
	t.Setenv("SURVEILLANCE_INTERVAL_MINUTES", "0")

	_, err := config.Load()
	if err == nil {
		t.Fatal("Load(SURVEILLANCE_INTERVAL_MINUTES=0) = nil err, want non-nil")
	}
	if !strings.Contains(err.Error(), "SURVEILLANCE_INTERVAL_MINUTES") {
		t.Errorf("Err = %q, want contains 'SURVEILLANCE_INTERVAL_MINUTES'", err)
	}
}

func TestLoad_InvalidSurveillanceCaptureTimeout(t *testing.T) {
	resetEnv(t)
	t.Setenv("SURVEILLANCE_CAPTURE_TIMEOUT_SECONDS", "-5")

	_, err := config.Load()
	if err == nil {
		t.Fatal("Load(SURVEILLANCE_CAPTURE_TIMEOUT_SECONDS=-5) = nil err, want non-nil")
	}
	if !strings.Contains(err.Error(), "SURVEILLANCE_CAPTURE_TIMEOUT_SECONDS") {
		t.Errorf("Err = %q, want contains 'SURVEILLANCE_CAPTURE_TIMEOUT_SECONDS'", err)
	}
}

func TestLoad_InvalidSurveillanceDeviceFreshness(t *testing.T) {
	resetEnv(t)
	t.Setenv("SURVEILLANCE_DEVICE_FRESHNESS_MINUTES", "0")

	_, err := config.Load()
	if err == nil {
		t.Fatal("Load(SURVEILLANCE_DEVICE_FRESHNESS_MINUTES=0) = nil err, want non-nil")
	}
	if !strings.Contains(err.Error(), "SURVEILLANCE_DEVICE_FRESHNESS_MINUTES") {
		t.Errorf("Err = %q, want contains 'SURVEILLANCE_DEVICE_FRESHNESS_MINUTES'", err)
	}
}

func TestLoad_EmptySurveillanceRootDir(t *testing.T) {
	resetEnv(t)
	t.Setenv("SURVEILLANCE_ROOT_DIR", "   ")

	_, err := config.Load()
	if err == nil {
		t.Fatal("Load(SURVEILLANCE_ROOT_DIR='   ') = nil err, want non-nil")
	}
	if !strings.Contains(err.Error(), "SURVEILLANCE_ROOT_DIR") {
		t.Errorf("Err = %q, want contains 'SURVEILLANCE_ROOT_DIR'", err)
	}
}
