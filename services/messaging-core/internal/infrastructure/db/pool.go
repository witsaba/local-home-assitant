// Package db provides a thread-safe singleton connection pool for Postgres.
// It wraps pgxpool and ensures at most one pool instance per process.
//
// Usage:
//
//	// Initialize once at startup (before any goroutines access the pool).
//	cfg := &db.PoolConfig{
//		Host:     "localhost",
//		Port:     5432,
//		Database: "witsaba",
//		User:     "pg-messaging-core",
//		Password: "secret",
//	}
//	if err := db.Open(cfg); err != nil {
//	    log.Fatal(err)
//	}
//	defer db.Close()
//
//	// Access from anywhere - returns the same instance.
//	pool := db.Get()
//
// PoolConfig holds the connection parameters and pool tuning settings.
// All fields are required except the PoolTuning ones which have defaults.
package db

import (
	"context"
	"fmt"
	"net/url"
	"sync"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

// pool is the singleton. Protected by once + mu for initialization safety.
var (
	pool    *pgxpool.Pool
	once    sync.Once
	mu      sync.RWMutex
	initErr error
)

// PoolConfig holds all connection and pool parameters.
type PoolConfig struct {
	// Connection parameters.
	Host     string
	Port     int
	Database string
	User     string
	Password string

	// Pool tuning. Zero values trigger safe defaults.
	MaxConns              int           // default 10
	MinConns              int           // default 1
	MaxConnLifetime       time.Duration // default 1h
	MaxConnIdleTime       time.Duration // default 30m
	HealthCheckPeriod     time.Duration // default 1m
	MaxConnLifetimeJitter time.Duration // default 30s

	// Connection options.
	ConnectTimeout time.Duration // default 5s
}

// String returns a connection URL for debugging (password redacted).
func (c *PoolConfig) String() string {
	return fmt.Sprintf("postgres://%s@%s:%d/%s",
		c.User, c.Host, c.Port, c.Database)
}

// pgxConfig converts PoolConfig to pgxpool.Config, applying defaults.
func (c *PoolConfig) pgxConfig() (*pgxpool.Config, error) {
	connStr := c.connectionString()

	cfg, err := pgxpool.ParseConfig(connStr)
	if err != nil {
		return nil, fmt.Errorf("parse connection string: %w", err)
	}

	// Apply pool tuning with safe defaults.
	if c.MaxConns > 0 {
		cfg.MaxConns = int32(c.MaxConns)
	} else {
		cfg.MaxConns = 10
	}

	if c.MinConns > 0 {
		cfg.MinConns = int32(c.MinConns)
	} else {
		cfg.MinConns = 1
	}

	if c.MaxConnLifetime > 0 {
		cfg.MaxConnLifetime = c.MaxConnLifetime
	} else {
		cfg.MaxConnLifetime = 1 * time.Hour
	}

	if c.MaxConnIdleTime > 0 {
		cfg.MaxConnIdleTime = c.MaxConnIdleTime
	} else {
		cfg.MaxConnIdleTime = 30 * time.Minute
	}

	if c.HealthCheckPeriod > 0 {
		cfg.HealthCheckPeriod = c.HealthCheckPeriod
	} else {
		cfg.HealthCheckPeriod = 1 * time.Minute
	}

	if c.MaxConnLifetimeJitter > 0 {
		cfg.MaxConnLifetimeJitter = c.MaxConnLifetimeJitter
	} else {
		cfg.MaxConnLifetimeJitter = 30 * time.Second
	}

	return cfg, nil
}

// connectionString builds the postgres:// URL with proper escaping.
func (c *PoolConfig) connectionString() string {
	u := url.URL{
		Scheme: "postgres",
		User:   url.UserPassword(c.User, c.Password),
		Host:   fmt.Sprintf("%s:%d", c.Host, c.Port),
		Path:   "/" + c.Database,
	}
	q := u.Query()
	q.Set("sslmode", "disable")

	connectTimeout := c.ConnectTimeout
	if connectTimeout == 0 {
		connectTimeout = 5 * time.Second
	}
	q.Set("connect_timeout", fmt.Sprintf("%d", int(connectTimeout.Seconds())))

	u.RawQuery = q.Encode()
	return u.String()
}

// Open initializes the singleton pool. Subsequent calls return the same
// instance. Thread-safe via sync.Once.
//
// Open must be called once before Get(). The pool is closed via Close().
// Returns an error if initialization fails.
func Open(cfg *PoolConfig) error {
	once.Do(func() {
		pgxCfg, err := cfg.pgxConfig()
		if err != nil {
			initErr = err
			return
		}

		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()

		pool, err = pgxpool.NewWithConfig(ctx, pgxCfg)
		if err != nil {
			initErr = fmt.Errorf("create pool: %w", err)
			return
		}

		// Verify connectivity before exposing the pool.
		if err := pool.Ping(ctx); err != nil {
			pool.Close()
			pool = nil
			initErr = fmt.Errorf("ping pool: %w", err)
			return
		}
	})

	if initErr != nil {
		return initErr
	}
	return nil
}

// Get returns the singleton pool. Panics if Open has not been called.
// Returns the same *pgxpool.Pool instance on every call.
func Get() *pgxpool.Pool {
	mu.RLock()
	defer mu.RUnlock()
	if pool == nil {
		panic("db.Open must be called before db.Get")
	}
	return pool
}

// IsOpen returns true if the pool has been initialized and is ready.
// Useful for health checks and lazy initialization patterns.
func IsOpen() bool {
	mu.RLock()
	defer mu.RUnlock()
	return pool != nil
}

// Close closes the singleton pool and releases all connections.
// Safe to call multiple times; subsequent Get() calls panic after Close.
// After Close, Open can be called again to reinitialize the pool.
//
// For testing: resets the singleton state so tests can run in isolation.
// Use resetForTesting() instead of this in test code.
func Close() {
	mu.Lock()
	defer mu.Unlock()
	if pool != nil {
		pool.Close()
		pool = nil
	}
	// Reset sync.Once so Open can be called again after Close.
	// This is achieved by replacing the sync.Once entirely.
	once = sync.Once{}
	initErr = nil
}

// Ping verifies the pool is healthy. Returns error if the pool is closed
// or the database is unreachable.
func Ping(ctx context.Context) error {
	p := Get()
	return p.Ping(ctx)
}

// resetForTesting exists only for tests. It closes the pool and resets
// all internal state so tests can run in isolation without sharing
// pool state across test cases.
//
// Usage: call at the start of TestMain with defer resetForTesting()
func resetForTesting() {
	mu.Lock()
	defer mu.Unlock()
	if pool != nil {
		pool.Close()
		pool = nil
	}
	once = sync.Once{}
	initErr = nil
}
