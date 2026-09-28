package config

import (
	"os"
	"testing"
	"time"
)

func TestLoad_Defaults(t *testing.T) {
	clearEnv(t)
	// MESSAGING_CORE_PG_PASSWORD has no default — set a dummy so validation passes.
	t.Setenv("MESSAGING_CORE_PG_PASSWORD", "dummy")
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
	if cfg.STREAMPort != 8080 {
		t.Errorf("STREAMPort: got %d, want %d", cfg.STREAMPort, 8080)
	}
	if cfg.APIPort != 8081 {
		t.Errorf("APIPort: got %d, want %d", cfg.APIPort, 8081)
	}
	if cfg.PGHost != "127.0.0.1" {
		t.Errorf("PGHost: got %q, want %q", cfg.PGHost, "127.0.0.1")
	}
	if cfg.PGPort != 5432 {
		t.Errorf("PGPort: got %d, want %d", cfg.PGPort, 5432)
	}
	if cfg.PGDatabase != "witsaba" {
		t.Errorf("PGDatabase: got %q, want %q", cfg.PGDatabase, "witsaba")
	}
	if cfg.PGUser != "pg-messaging-core" {
		t.Errorf("PGUser: got %q, want %q", cfg.PGUser, "pg-messaging-core")
	}
	if cfg.PGPassword != "dummy" {
		t.Errorf("PGPassword: got %q, want %q (set by test)", cfg.PGPassword, "dummy")
	}
	if cfg.PGMaxConns != 0 {
		t.Errorf("PGMaxConns: got %d, want 0 (db package applies safe default)", cfg.PGMaxConns)
	}
	if cfg.PGMinConns != 0 {
		t.Errorf("PGMinConns: got %d, want 0", cfg.PGMinConns)
	}
	if cfg.PGMaxConnLifetime != 0 {
		t.Errorf("PGMaxConnLifetime: got %v, want 0", cfg.PGMaxConnLifetime)
	}
	if cfg.PGMaxConnIdleTime != 0 {
		t.Errorf("PGMaxConnIdleTime: got %v, want 0", cfg.PGMaxConnIdleTime)
	}
}

func TestLoad_Overrides(t *testing.T) {
	t.Setenv("NATS_HOST", "0.0.0.0")
	t.Setenv("NATS_PORT", "5222")
	t.Setenv("LOG_LEVEL", "DEBUG")
	t.Setenv("NATS_DATA_DIR", "/var/lib/nats")
	t.Setenv("STREAM_PORT", "9000")
	t.Setenv("API_PORT", "9090")
	t.Setenv("MESSAGING_CORE_PG_HOST", "192.168.1.10")
	t.Setenv("MESSAGING_CORE_PG_PORT", "5433")
	t.Setenv("MESSAGING_CORE_PG_DATABASE", "testdb")
	t.Setenv("MESSAGING_CORE_PG_USER", "pg-test")
	t.Setenv("MESSAGING_CORE_PG_PASSWORD", "secret")
	t.Setenv("MESSAGING_CORE_PG_MAX_CONNS", "25")
	t.Setenv("MESSAGING_CORE_PG_MIN_CONNS", "5")
	t.Setenv("MESSAGING_CORE_PG_MAX_CONN_LIFETIME", "2h")
	t.Setenv("MESSAGING_CORE_PG_MAX_CONN_IDLE_TIME", "15m")

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
	if cfg.STREAMPort != 9000 {
		t.Errorf("STREAMPort: got %d, want %d", cfg.STREAMPort, 9000)
	}
	if cfg.APIPort != 9090 {
		t.Errorf("APIPort: got %d, want %d", cfg.APIPort, 9090)
	}
	if cfg.PGHost != "192.168.1.10" {
		t.Errorf("PGHost: got %q, want %q", cfg.PGHost, "192.168.1.10")
	}
	if cfg.PGPort != 5433 {
		t.Errorf("PGPort: got %d, want %d", cfg.PGPort, 5433)
	}
	if cfg.PGDatabase != "testdb" {
		t.Errorf("PGDatabase: got %q, want %q", cfg.PGDatabase, "testdb")
	}
	if cfg.PGUser != "pg-test" {
		t.Errorf("PGUser: got %q, want %q", cfg.PGUser, "pg-test")
	}
	if cfg.PGPassword != "secret" {
		t.Errorf("PGPassword: got %q, want %q", cfg.PGPassword, "secret")
	}
	if cfg.PGMaxConns != 25 {
		t.Errorf("PGMaxConns: got %d, want %d", cfg.PGMaxConns, 25)
	}
	if cfg.PGMinConns != 5 {
		t.Errorf("PGMinConns: got %d, want %d", cfg.PGMinConns, 5)
	}
	if cfg.PGMaxConnLifetime != 2*time.Hour {
		t.Errorf("PGMaxConnLifetime: got %v, want %v", cfg.PGMaxConnLifetime, 2*time.Hour)
	}
	if cfg.PGMaxConnIdleTime != 15*time.Minute {
		t.Errorf("PGMaxConnIdleTime: got %v, want %v", cfg.PGMaxConnIdleTime, 15*time.Minute)
	}
}

func TestLoad_InvalidLogLevel(t *testing.T) {
	clearEnv(t)
	t.Setenv("LOG_LEVEL", "trace")
	if _, err := Load(); err == nil {
		t.Fatal("expected error for LOG_LEVEL=trace, got nil")
	}
}

func TestLoad_InvalidNATSPort(t *testing.T) {
	clearEnv(t)
	t.Setenv("NATS_PORT", "not-a-number")
	if _, err := Load(); err == nil {
		t.Fatal("expected error for NATS_PORT=not-a-number, got nil")
	}
}

func TestLoad_OutOfRangeNATSPort(t *testing.T) {
	clearEnv(t)
	t.Setenv("NATS_PORT", "70000")
	if _, err := Load(); err == nil {
		t.Fatal("expected error for NATS_PORT=70000, got nil")
	}
}

func TestLoad_MissingPGPassword(t *testing.T) {
	clearEnv(t)
	// MESSAGING_CORE_PG_PASSWORD unset + PG_USER set → validation should fail
	t.Setenv("MESSAGING_CORE_PG_USER", "pg-messaging-core")
	if _, err := Load(); err == nil {
		t.Fatal("expected error for missing MESSAGING_CORE_PG_PASSWORD, got nil")
	}
}

// TestLoad_EmptyPGHostIsCoveredByDefault covers the case where MESSAGING_CORE_PG_HOST
// would be empty but is rescued by the fallback. This test confirms the
// fallback never produces an empty string.
func TestLoad_EmptyPGHostFallsBack(t *testing.T) {
	clearEnv(t)
	t.Setenv("MESSAGING_CORE_PG_HOST", "")
	t.Setenv("MESSAGING_CORE_PG_PASSWORD", "secret")
	cfg, err := Load()
	if err != nil {
		t.Fatalf("unexpected error (empty MESSAGING_CORE_PG_HOST should fall back to default): %v", err)
	}
	if cfg.PGHost != "127.0.0.1" {
		t.Errorf("PGHost: got %q, want fallback %q", cfg.PGHost, "127.0.0.1")
	}
}

func TestLoad_OutOfRangeAPIPort(t *testing.T) {
	clearEnv(t)
	t.Setenv("API_PORT", "0")
	t.Setenv("MESSAGING_CORE_PG_PASSWORD", "secret")
	if _, err := Load(); err == nil {
		t.Fatal("expected error for API_PORT=0, got nil")
	}
}

func TestLoad_OutOfRangeSTREAMPort(t *testing.T) {
	clearEnv(t)
	t.Setenv("STREAM_PORT", "70000")
	t.Setenv("MESSAGING_CORE_PG_PASSWORD", "secret")
	if _, err := Load(); err == nil {
		t.Fatal("expected error for STREAM_PORT=70000, got nil")
	}
}

func TestLoad_OutOfRangePGPort(t *testing.T) {
	clearEnv(t)
	t.Setenv("MESSAGING_CORE_PG_PORT", "0")
	t.Setenv("MESSAGING_CORE_PG_PASSWORD", "secret")
	if _, err := Load(); err == nil {
		t.Fatal("expected error for MESSAGING_CORE_PG_PORT=0, got nil")
	}
}

// clearEnv removes all messaging-core env vars so the defaults test
// is hermetic. t.Setenv handles re-restore on test exit.
func clearEnv(t *testing.T) {
	t.Helper()
	keys := []string{
		"NATS_HOST", "NATS_PORT", "LOG_LEVEL", "NATS_DATA_DIR",
		"STREAM_PORT", "API_PORT",
		"MESSAGING_CORE_PG_HOST", "MESSAGING_CORE_PG_PORT", "MESSAGING_CORE_PG_DATABASE", "MESSAGING_CORE_PG_USER", "MESSAGING_CORE_PG_PASSWORD",
		"MESSAGING_CORE_PG_MAX_CONNS", "MESSAGING_CORE_PG_MIN_CONNS", "MESSAGING_CORE_PG_MAX_CONN_LIFETIME", "MESSAGING_CORE_PG_MAX_CONN_IDLE_TIME",
	}
	for _, k := range keys {
		os.Unsetenv(k)
	}
}
