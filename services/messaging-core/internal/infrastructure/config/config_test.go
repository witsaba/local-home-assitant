package config

import (
	"testing"
)

func TestLoad_Defaults(t *testing.T) {
	clearEnv(t)
	cfg, err := Load()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if cfg.Host != "127.0.0.1" {
		t.Errorf("Host: got %q, want %q", cfg.Host, "127.0.0.1")
	}
	if cfg.Port != 4222 {
		t.Errorf("Port: got %d, want %d", cfg.Port, 4222)
	}
	if cfg.LogLevel != "info" {
		t.Errorf("LogLevel: got %q, want %q", cfg.LogLevel, "info")
	}
	if cfg.DataDir != "" {
		t.Errorf("DataDir: got %q, want empty", cfg.DataDir)
	}
}

func TestLoad_Overrides(t *testing.T) {
	t.Setenv("NATS_HOST", "0.0.0.0")
	t.Setenv("NATS_PORT", "5222")
	t.Setenv("LOG_LEVEL", "DEBUG")
	t.Setenv("NATS_DATA_DIR", "/var/lib/nats")

	cfg, err := Load()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if cfg.Host != "0.0.0.0" {
		t.Errorf("Host: got %q, want %q", cfg.Host, "0.0.0.0")
	}
	if cfg.Port != 5222 {
		t.Errorf("Port: got %d, want %d", cfg.Port, 5222)
	}
	if cfg.LogLevel != "debug" {
		t.Errorf("LogLevel (case-insensitive): got %q, want %q", cfg.LogLevel, "debug")
	}
	if cfg.DataDir != "/var/lib/nats" {
		t.Errorf("DataDir: got %q, want %q", cfg.DataDir, "/var/lib/nats")
	}
}

func TestLoad_InvalidLogLevel(t *testing.T) {
	t.Setenv("LOG_LEVEL", "trace")
	if _, err := Load(); err == nil {
		t.Fatal("expected error for LOG_LEVEL=trace, got nil")
	}
}

func TestLoad_InvalidPort(t *testing.T) {
	t.Setenv("NATS_PORT", "not-a-number")
	if _, err := Load(); err == nil {
		t.Fatal("expected error for NATS_PORT=not-a-number, got nil")
	}
}

func TestLoad_OutOfRangePort(t *testing.T) {
	t.Setenv("NATS_PORT", "70000")
	if _, err := Load(); err == nil {
		t.Fatal("expected error for NATS_PORT=70000, got nil")
	}
}

// clearEnv removes all messaging-core env vars so the defaults test
// is hermetic. t.Setenv handles re-restore on test exit.
func clearEnv(t *testing.T) {
	t.Helper()
	for _, k := range []string{"NATS_HOST", "NATS_PORT", "LOG_LEVEL", "NATS_DATA_DIR"} {
		t.Setenv(k, "")
	}
}
