//go:build integration
// +build integration

package devices_test

import (
	"context"
	"os"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/testcontainers/testcontainers-go"
	tcpostgres "github.com/testcontainers/testcontainers-go/modules/postgres"
	"github.com/testcontainers/testcontainers-go/wait"

	"github.com/witsaba/local-home-assitant/services/workers/internal/infrastructure/devices"
	"github.com/witsaba/local-home-assitant/services/workers/internal/types"
)

// integrationGate is the env var operators set to opt in to the
// integration tests. Anything else (unset, empty, "0", "false") keeps
// the tests skipped. This prevents accidental runs on machines without
// a reachable Docker daemon.
const integrationGate = "INTEGRATION"

func integrationEnabled() bool {
	switch os.Getenv(integrationGate) {
	case "postgres", "1", "true", "TRUE", "True":
		return true
	default:
		return false
	}
}

// pgImage is the image the integration container runs. It matches the
// production docker-compose.yml image exactly so the SQL the
// repository emits is exercised against the same engine the stack
// will deploy with.
const pgImage = "pgvector/pgvector:pg16-trixie"

// ddlSchema is the schema + table the test creates. Identical shape
// to the production migration that will land in a follow-up feature.
const ddlSchema = `
CREATE SCHEMA witsaba;

CREATE TABLE witsaba.devices (
    mac            TEXT        PRIMARY KEY,
    name           TEXT,
    fw             TEXT,
    chip           TEXT,
    last_source_ip INET,
    first_seen_at  TIMESTAMPTZ NOT NULL DEFAULT now(),
    last_seen_at   TIMESTAMPTZ NOT NULL
);
`

// newPool spins up a pgvector Postgres container, creates the witsaba
// schema and devices table, and returns a pgxpool.Pool wired to it.
// The container and pool are torn down via t.Cleanup.
func newPool(t *testing.T) *pgxpool.Pool {
	t.Helper()

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()

	pgC, err := tcpostgres.RunContainer(ctx,
		testcontainers.WithImage(pgImage),
		testcontainers.WithWaitStrategy(
			wait.ForLog("database system is ready to accept connections").
				WithOccurrence(2).
				WithStartupTimeout(60*time.Second),
		),
	)
	if err != nil {
		t.Fatalf("start postgres container: %v", err)
	}

	t.Cleanup(func() {
		stopCtx, stopCancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer stopCancel()
		if err := pgC.Terminate(stopCtx); err != nil {
			t.Logf("terminate postgres container: %v", err)
		}
	})

	connStr, err := pgC.ConnectionString(ctx, "sslmode=disable")
	if err != nil {
		t.Fatalf("connection string: %v", err)
	}

	pool, err := pgxpool.New(ctx, connStr)
	if err != nil {
		t.Fatalf("pgxpool.New: %v", err)
	}
	t.Cleanup(pool.Close)

	if _, err := pool.Exec(ctx, ddlSchema); err != nil {
		t.Fatalf("create schema: %v", err)
	}

	return pool
}

func TestIntegration_UpsertInsertsNewRow(t *testing.T) {
	if !integrationEnabled() {
		t.Skipf("set %s=postgres to enable integration tests", integrationGate)
	}

	pool := newPool(t)
	repo := devices.NewPgx(pool, nil) // nil pool closer; t.Cleanup closes it

	ev := types.DiscoveryEvent{
		DiscoveredAt: time.Date(2026, 9, 27, 12, 0, 0, 0, time.UTC),
		SourceIP:     "192.168.1.51",
		MAC:          "aa:bb:cc:dd:ee:01",
		Name:         "kitchen-cam",
		FW:           "1.2.3",
		Chip:         "esp32-s3",
	}

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	if err := repo.Upsert(ctx, ev); err != nil {
		t.Fatalf("first Upsert: %v", err)
	}

	var (
		name, fw, chip        string
		lastSourceIP          string
		firstSeen, lastSeen   time.Time
	)
	err := pool.QueryRow(ctx, `
		SELECT name, fw, chip, host(last_source_ip), first_seen_at, last_seen_at
		FROM witsaba.devices WHERE mac = $1
	`, ev.MAC).Scan(&name, &fw, &chip, &lastSourceIP, &firstSeen, &lastSeen)
	if err != nil {
		t.Fatalf("select: %v", err)
	}
	if name != "kitchen-cam" {
		t.Errorf("name=%q want %q", name, "kitchen-cam")
	}
	if fw != "1.2.3" {
		t.Errorf("fw=%q want %q", fw, "1.2.3")
	}
	if chip != "esp32-s3" {
		t.Errorf("chip=%q want %q", chip, "esp32-s3")
	}
	if lastSourceIP != "192.168.1.51" {
		t.Errorf("last_source_ip=%q want %q", lastSourceIP, "192.168.1.51")
	}
	if !firstSeen.Equal(ev.DiscoveredAt) {
		t.Errorf("first_seen_at=%v want %v", firstSeen, ev.DiscoveredAt)
	}
	if !lastSeen.Equal(ev.DiscoveredAt) {
		t.Errorf("last_seen_at=%v want %v", lastSeen, ev.DiscoveredAt)
	}
}

func TestIntegration_UpsertRefreshesOnConflict(t *testing.T) {
	if !integrationEnabled() {
		t.Skipf("set %s=postgres to enable integration tests", integrationGate)
	}

	pool := newPool(t)
	repo := devices.NewPgx(pool, nil)

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	first := types.DiscoveryEvent{
		DiscoveredAt: time.Date(2026, 9, 27, 12, 0, 0, 0, time.UTC),
		SourceIP:     "192.168.1.51",
		MAC:          "aa:bb:cc:dd:ee:01",
		Name:         "kitchen-cam",
		FW:           "1.2.3",
		Chip:         "esp32-s3",
	}
	if err := repo.Upsert(ctx, first); err != nil {
		t.Fatalf("first Upsert: %v", err)
	}

	second := types.DiscoveryEvent{
		DiscoveredAt: time.Date(2026, 9, 27, 13, 30, 0, 0, time.UTC),
		SourceIP:     "192.168.1.61",
		MAC:          "aa:bb:cc:dd:ee:01", // same MAC
		Name:         "kitchen-cam",
		FW:           "1.2.4",
		Chip:         "esp32-s3",
	}
	if err := repo.Upsert(ctx, second); err != nil {
		t.Fatalf("second Upsert: %v", err)
	}

	var (
		fw         string
		firstSeen  time.Time
		lastSeen   time.Time
		lastSrcIP  string
	)
	err := pool.QueryRow(ctx, `
		SELECT fw, first_seen_at, last_seen_at, host(last_source_ip)
		FROM witsaba.devices WHERE mac = $1
	`, first.MAC).Scan(&fw, &firstSeen, &lastSeen, &lastSrcIP)
	if err != nil {
		t.Fatalf("select: %v", err)
	}
	if fw != "1.2.4" {
		t.Errorf("fw=%q want %q (refresh on conflict)", fw, "1.2.4")
	}
	if lastSrcIP != "192.168.1.61" {
		t.Errorf("last_source_ip=%q want %q (refresh on conflict)", lastSrcIP, "192.168.1.61")
	}
	if !lastSeen.Equal(second.DiscoveredAt) {
		t.Errorf("last_seen_at=%v want %v (refresh on conflict)", lastSeen, second.DiscoveredAt)
	}
	if !firstSeen.Equal(first.DiscoveredAt) {
		t.Errorf("first_seen_at=%v want %v (preserved across upsert)", firstSeen, first.DiscoveredAt)
	}
}

func TestIntegration_UpsertAcceptsEmptyOptionalFields(t *testing.T) {
	if !integrationEnabled() {
		t.Skipf("set %s=postgres to enable integration tests", integrationGate)
	}

	pool := newPool(t)
	repo := devices.NewPgx(pool, nil)

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	ev := types.DiscoveryEvent{
		DiscoveredAt: time.Date(2026, 9, 27, 12, 0, 0, 0, time.UTC),
		MAC:          "aa:bb:cc:dd:ee:02",
		// SourceIP, Name, FW, Chip all empty
	}
	if err := repo.Upsert(ctx, ev); err != nil {
		t.Fatalf("Upsert: %v", err)
	}

	var (
		name, fw, chip    *string
		lastSourceIP      *string
	)
	err := pool.QueryRow(ctx, `
		SELECT name, fw, chip, host(last_source_ip)
		FROM witsaba.devices WHERE mac = $1
	`, ev.MAC).Scan(&name, &fw, &chip, &lastSourceIP)
	if err != nil {
		t.Fatalf("select: %v", err)
	}
	if name != nil || fw != nil || chip != nil {
		t.Errorf("expected NULL optional fields, got name=%v fw=%v chip=%v", name, fw, chip)
	}
	if lastSourceIP != nil {
		t.Errorf("expected NULL last_source_ip, got %v", *lastSourceIP)
	}
}

func TestIntegration_UpsertRejectsEmptyMAC(t *testing.T) {
	if !integrationEnabled() {
		t.Skipf("set %s=postgres to enable integration tests", integrationGate)
	}

	pool := newPool(t)
	repo := devices.NewPgx(pool, nil)

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	err := repo.Upsert(ctx, types.DiscoveryEvent{SourceIP: "10.0.0.1"})
	if err == nil {
		t.Fatal("expected error for empty MAC, got nil")
	}
}
