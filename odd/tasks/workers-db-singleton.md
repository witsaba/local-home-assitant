# Workers DB Singleton - Task Tracking

## Feature: Workers Database Connection Singleton

### Goals
1. Replace raw pgxpool creation in main.go with a controlled singleton pattern
2. Ensure thread-safe one-time pool initialization
3. Expose pool tuning parameters via environment variables
4. Keep existing upsert logic and Repository interface

### Implementation Details

#### 1. New `db` package (`internal/infrastructure/db/pool.go`)
- Singleton pool managed via `sync.Once`
- `Open(cfg *PoolConfig)` - thread-safe initialization
- `Get()` - returns singleton pool (panics if not initialized)
- `Close()` - closes pool and allows re-initialization
- `IsOpen()` - health check
- `Ping(ctx)` - verify connectivity
- `resetForTesting()` - test isolation helper

#### 2. Pool Configuration
Default tuning values:
- `MaxConns`: 10
- `MinConns`: 1
- `MaxConnLifetime`: 1h
- `MaxConnIdleTime`: 30m
- `HealthCheckPeriod`: 1m

New environment variables:
- `PG_MAX_CONNS` (default: 10)
- `PG_MIN_CONNS` (default: 1)
- `PG_MAX_CONN_LIFETIME` (default: 1h)
- `PG_MAX_CONN_IDLE_TIME` (default: 30m)

#### 3. Updated Files
- `cmd/workers/main.go` - uses `db.Open()` / `db.Get()` / `db.Close()`
- `internal/infrastructure/config/config.go` - exposes pool tuning env vars
- `internal/infrastructure/devices/store.go` - simplified `NewPgx(q)` (single arg)
- `internal/infrastructure/db/pool.go` - new singleton implementation
- `internal/infrastructure/db/pool_test.go` - singleton lifecycle tests
- `internal/infrastructure/devices/store_test.go` - updated for new signature
- `internal/infrastructure/devices/integration_test.go` - concurrent upsert tests

### Tests Added
1. **Unit tests for db package:**
   - `TestPoolConfigConnectionString`
   - `TestPoolConfigConnectionStringEscapesSpecialChars`
   - `TestPoolConfigStringRedactsPassword`
   - `TestPoolConfigPgxConfigDefaults`
   - `TestPoolConfigPgxConfigTuning`
   - `TestIsOpenBeforeOpen`
   - `TestGetBeforeOpenPanics`
   - `TestOpenIdempotentWithInvalidConfig`
   - `TestCloseIdempotent`
   - `TestResetForTesting`
   - `TestConcurrentAccess`
   - `TestPingWithoutPool`

2. **Integration tests for concurrent upserts:**
   - `TestIntegration_ConcurrentUpserts` - 50 different devices
   - `TestIntegration_ConcurrentUpsertsSameDevice` - 20 goroutines same MAC

### Commit History
1. feat: add db package with thread-safe singleton pool using sync.Once
2. feat: add pool tuning environment variables to config
3. refactor: update main.go to use db singleton
4. refactor: simplify NewPgx signature (single Querier arg)
5. test: add singleton lifecycle and concurrent upsert tests

### Notes
- GORM was not used per user's decision (current pgx implementation is sufficient)
- Pool lifecycle is managed by db package, not by repository
- Repository now has no Close() responsibility (satisfies interface but no-op)
