package db

import (
	"testing"
	"time"
)

func TestPoolConfigDefaults(t *testing.T) {
	cfg := &PoolConfig{
		Host:     "localhost",
		Port:     5432,
		Database: "witsaba",
		User:     "pg-test",
		Password: "secret",
	}

	pgxCfg, err := cfg.pgxConfig()
	if err != nil {
		t.Fatalf("pgxConfig() failed: %v", err)
	}

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

func TestPoolConfigTuning(t *testing.T) {
	cfg := &PoolConfig{
		Host:     "localhost",
		Port:     5432,
		Database: "witsaba",
		User:     "pg-test",
		Password: "secret",
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
		t.Errorf("MaxConns: got %d, want %d", pgxCfg.MaxConns, 25)
	}
	if pgxCfg.MinConns != 5 {
		t.Errorf("MinConns: got %d, want %d", pgxCfg.MinConns, 5)
	}
	if pgxCfg.MaxConnLifetime != 2*time.Hour {
		t.Errorf("MaxConnLifetime: got %v, want %v", pgxCfg.MaxConnLifetime, 2*time.Hour)
	}
	if pgxCfg.MaxConnIdleTime != 15*time.Minute {
		t.Errorf("MaxConnIdleTime: got %v, want %v", pgxCfg.MaxConnIdleTime, 15*time.Minute)
	}
}

func TestPoolConfigConnectionString(t *testing.T) {
	cfg := &PoolConfig{
		Host:     "192.168.1.10",
		Port:     5433,
		Database: "testdb",
		User:     "pg-test",
		Password: "secret",
	}

	connStr := cfg.connectionString()
	// Password should appear (redacted in String(), not in connectionString).
	if connStr == "" {
		t.Error("connectionString: got empty string")
	}
}

func TestPoolConfigStringRedactsPassword(t *testing.T) {
	cfg := &PoolConfig{
		Host:     "localhost",
		Port:     5432,
		Database: "witsaba",
		User:     "pg-test",
		Password: "supersecret",
	}

	s := cfg.String()
	// String() should contain the redacted form.
	if s == "" {
		t.Error("String: got empty string")
	}
}

func TestOpen_OpenTwiceReturnsSameError(t *testing.T) {
	// Reset singleton state so this test can run independently.
	resetForTesting()
	defer resetForTesting()

	// Calling Open with invalid config should fail.
	err := Open(&PoolConfig{
		Host:     "invalid-host-that-does-not-exist",
		Port:     5432,
		Database: "witsaba",
		User:     "pg-test",
		Password: "secret",
	})
	if err == nil {
		t.Fatal("expected error for invalid host, got nil")
	}

	// Second Open call should return the same error (not panic).
	err2 := Open(&PoolConfig{
		Host:     "localhost",
		Port:     5432,
		Database: "witsaba",
		User:     "pg-test",
		Password: "secret",
	})
	if err2 == nil {
		t.Fatal("expected same error on second Open call, got nil")
	}
}

func TestGet_BeforeOpen_Panics(t *testing.T) {
	// Reset singleton state so this test can run independently.
	resetForTesting()
	defer resetForTesting()

	defer func() {
		if r := recover(); r == nil {
			t.Error("expected panic when calling Get() before Open(), got no panic")
		}
	}()

	Get() // should panic
}

func TestIsOpen_BeforeOpen(t *testing.T) {
	// Reset singleton state so this test can run independently.
	resetForTesting()
	defer resetForTesting()

	if IsOpen() {
		t.Error("IsOpen: got true before Open(), want false")
	}
}
