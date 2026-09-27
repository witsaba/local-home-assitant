package db

import (
	"context"
	"strings"
	"sync"
	"testing"
	"time"
)

func TestPoolConfigConnectionString(t *testing.T) {
	cfg := &PoolConfig{
		Host:     "localhost",
		Port:     5432,
		Database: "witsaba",
		User:     "pg-worker",
		Password: "secret123",
	}

	connStr := cfg.connectionString()

	if !strings.HasPrefix(connStr, "postgres://") {
		t.Errorf("expected postgres:// prefix, got %s", connStr)
	}
	if !strings.Contains(connStr, "localhost:5432") {
		t.Errorf("expected localhost:5432, got %s", connStr)
	}
	if !strings.Contains(connStr, "/witsaba") {
		t.Errorf("expected /witsaba path, got %s", connStr)
	}
	if !strings.Contains(connStr, "sslmode=disable") {
		t.Errorf("expected sslmode=disable, got %s", connStr)
	}
	if !strings.Contains(connStr, "pg-worker") {
		t.Errorf("expected username in URL, got %s", connStr)
	}
}

func TestPoolConfigConnectionStringEscapesSpecialChars(t *testing.T) {
	cfg := &PoolConfig{
		Host:     "db.example.com",
		Port:     5433,
		Database: "prod",
		User:     "admin",
		Password: "pass@word!", // special chars should be escaped
	}

	connStr := cfg.connectionString()

	// Password with special chars should be percent-encoded
	if !strings.Contains(connStr, "admin:pass%40word%21@") {
		t.Errorf("expected escaped password in URL, got %s", connStr)
	}
}

func TestPoolConfigStringRedactsPassword(t *testing.T) {
	cfg := &PoolConfig{
		Host:     "localhost",
		Port:     5432,
		Database: "witsaba",
		User:     "pg-worker",
		Password: "secret",
	}

	s := cfg.String()

	if !strings.Contains(s, "pg-worker@") {
		t.Errorf("expected user in String(), got %s", s)
	}
	// String() should not panic or reveal password
	if strings.Contains(s, "secret") {
		t.Errorf("String() should not contain password, got %s", s)
	}
}

func TestPoolConfigPgxConfigDefaults(t *testing.T) {
	cfg := &PoolConfig{
		Host:     "localhost",
		Port:     5432,
		Database: "test",
		User:     "user",
		Password: "pass",
	}

	pgxCfg, err := cfg.pgxConfig()
	if err != nil {
		t.Fatalf("pgxConfig() failed: %v", err)
	}

	// Check defaults are applied.
	if pgxCfg.MaxConns != 10 {
		t.Errorf("MaxConns: got %d, want 10", pgxCfg.MaxConns)
	}
	if pgxCfg.MinConns != 1 {
		t.Errorf("MinConns: got %d, want 1", pgxCfg.MinConns)
	}
	if pgxCfg.MaxConnLifetime != 1*time.Hour {
		t.Errorf("MaxConnLifetime: got %v, want 1h", pgxCfg.MaxConnLifetime)
	}
	if pgxCfg.MaxConnIdleTime != 30*time.Minute {
		t.Errorf("MaxConnIdleTime: got %v, want 30m", pgxCfg.MaxConnIdleTime)
	}
	if pgxCfg.HealthCheckPeriod != 1*time.Minute {
		t.Errorf("HealthCheckPeriod: got %v, want 1m", pgxCfg.HealthCheckPeriod)
	}
}

func TestPoolConfigPgxConfigTuning(t *testing.T) {
	cfg := &PoolConfig{
		Host:     "localhost",
		Port:     5432,
		Database: "test",
		User:     "user",
		Password: "pass",
		// Custom tuning.
		MaxConns:        25,
		MinConns:        5,
		MaxConnLifetime: 2 * time.Hour,
		MaxConnIdleTime: 15 * time.Minute,
	}

	pgxCfg, err := cfg.pgxConfig()
	if err != nil {
		t.Fatalf("pgxConfig() failed: %v", err)
	}

	if pgxCfg.MaxConns != 25 {
		t.Errorf("MaxConns: got %d, want 25", pgxCfg.MaxConns)
	}
	if pgxCfg.MinConns != 5 {
		t.Errorf("MinConns: got %d, want 5", pgxCfg.MinConns)
	}
	if pgxCfg.MaxConnLifetime != 2*time.Hour {
		t.Errorf("MaxConnLifetime: got %v, want 2h", pgxCfg.MaxConnLifetime)
	}
	if pgxCfg.MaxConnIdleTime != 15*time.Minute {
		t.Errorf("MaxConnIdleTime: got %v, want 15m", pgxCfg.MaxConnIdleTime)
	}
}

func TestIsOpenBeforeOpen(t *testing.T) {
	// Reset singleton state for this test.
	resetForTesting()
	defer resetForTesting()

	if IsOpen() {
		t.Error("IsOpen() should return false before Open()")
	}
}

func TestGetBeforeOpenPanics(t *testing.T) {
	// Reset singleton state for this test.
	resetForTesting()
	defer resetForTesting()

	defer func() {
		if r := recover(); r == nil {
			t.Error("Get() should panic before Open()")
		}
	}()

	Get()
}

func TestOpenIdempotentWithInvalidConfig(t *testing.T) {
	// Reset singleton state for this test.
	resetForTesting()
	defer resetForTesting()

	cfg := &PoolConfig{
		Host:     "invalid-host-that-does-not-exist",
		Port:     5432,
		Database: "test",
		User:     "user",
		Password: "pass",
	}

	// First call should fail.
	err := Open(cfg)
	if err == nil {
		t.Error("expected error for invalid host")
	}

	// Second call should return the same error (idempotent).
	err2 := Open(cfg)
	if err2 == nil {
		t.Error("expected error on second Open() call")
	}

	// Pool should not be open.
	if IsOpen() {
		t.Error("pool should not be open after failed Open()")
	}
}

func TestCloseIdempotent(t *testing.T) {
	// Reset singleton state for this test.
	resetForTesting()
	defer resetForTesting()

	// Close on nil pool should not panic.
	Close()

	// Second close should also not panic.
	Close()
}

func TestResetForTesting(t *testing.T) {
	// Ensure resetForTesting works for test isolation.
	resetForTesting()

	if IsOpen() {
		t.Error("IsOpen() should return false after resetForTesting()")
	}

	// After reset, Open can be called again (though it will fail without a real DB).
	cfg := &PoolConfig{
		Host:     "localhost",
		Port:     5432,
		Database: "test",
		User:     "user",
		Password: "pass",
	}
	err := Open(cfg)
	// We expect this to fail (no DB running), but reset should have worked.
	_ = err
}

func TestConcurrentAccess(t *testing.T) {
	// Reset singleton state for this test.
	resetForTesting()
	defer resetForTesting()

	cfg := &PoolConfig{
		Host:     "invalid-host",
		Port:     5432,
		Database: "test",
		User:     "user",
		Password: "pass",
	}

	// Concurrently call Open from multiple goroutines.
	var wg sync.WaitGroup
	for i := 0; i < 10; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_ = Open(cfg)
		}()
	}
	wg.Wait()

	// All goroutines should have seen the same result (error or nil).
	// The important thing is no race condition or panic occurred.
}

func TestPingWithoutPool(t *testing.T) {
	// Reset singleton state for this test.
	resetForTesting()
	defer resetForTesting()

	defer func() {
		if r := recover(); r == nil {
			t.Error("Ping() should panic when pool is nil")
		}
	}()

	ctx := context.Background()
	_ = Ping(ctx)
}
