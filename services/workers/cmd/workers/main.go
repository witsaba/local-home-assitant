// workers is the composition root for the periodic-background-job host.
// It wires: config → logger → pgx pool → devices repo → events channel
// → emit → scheduler (with discovery) → consumer → signal.NotifyContext
// → graceful drain → repo close → exit 0.
package main

import (
	"context"
	"fmt"
	"net/url"
	"os"
	"os/signal"
	"sync"
	"syscall"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"go.uber.org/zap"

	"github.com/witsaba/local-home-assitant/services/workers/internal/infrastructure/config"
	"github.com/witsaba/local-home-assitant/services/workers/internal/infrastructure/devices"
	loggerinfra "github.com/witsaba/local-home-assitant/services/workers/internal/infrastructure/logger"
	"github.com/witsaba/local-home-assitant/services/workers/internal/jobs/discovery"
	"github.com/witsaba/local-home-assitant/services/workers/internal/types"
	"github.com/witsaba/local-home-assitant/services/workers/internal/worker"
)

// version is stamped at build time via -ldflags.
var version = "0.1.0-dev"

// shutdownTimeout caps how long we give the consumer to drain pending
// events after a SIGTERM. Long enough for any plausible in-flight scan
// to finish, short enough that operators don't rage-quit.
const shutdownTimeout = 10 * time.Second

// eventsBufferSize is the capacity of the fan-in events channel.
const eventsBufferSize = 256

// pgPingTimeout caps how long startup waits for Postgres to accept a
// connection. The compose file gates the workers container on the
// postgres healthcheck, so in practice this returns immediately; the
// timeout exists to fail fast if the operator misconfigures PG_*.
const pgPingTimeout = 5 * time.Second

func main() { os.Exit(run()) }

func run() int {
	cfg, err := config.Load()
	if err != nil {
		fmt.Fprintf(os.Stderr, "workers: config load failed: %v\n", err)
		return 2
	}

	log, err := loggerinfra.New(cfg.LogLevel)
	if err != nil {
		fmt.Fprintf(os.Stderr, "workers: logger init failed: %v\n", err)
		return 2
	}

	log.Info("workers starting",
		zap.String("version", version),
		zap.String("log_level", cfg.LogLevel),
		zap.Int("discovery_interval_s", cfg.DiscoveryIntervalSeconds),
		zap.Int("discovery_pool_size", cfg.DiscoveryWorkerPoolSize),
		zap.Int("discovery_probe_timeout_ms", cfg.DiscoveryProbeTimeoutMs),
		zap.String("pg_host", cfg.PGHost),
		zap.Int("pg_port", cfg.PGPort),
		zap.String("pg_database", cfg.PGDatabase),
		zap.String("pg_user", cfg.PGUser),
	)

	// Build the pgxpool against the host loopback. Postgres binds to
	// 127.0.0.1 only (listen_addresses=127.0.0.1 in docker-compose.yml),
	// so sslmode=disable is acceptable here: no TLS in the loopback
	// hop, and the listener is not reachable from the LAN.
	connStr := pgConnString(cfg)
	pool, err := pgxpool.New(context.Background(), connStr)
	if err != nil {
		log.Error("postgres pool init failed",
			zap.String("host", cfg.PGHost),
			zap.Int("port", cfg.PGPort),
			zap.String("database", cfg.PGDatabase),
			zap.String("user", cfg.PGUser),
			zap.Error(err),
		)
		return 2
	}
	// The devices repository owns the pool's lifecycle; we hand it
	// both as Querier (for Exec) and as io.Closer (for shutdown).
	// repo.Close() runs from the deferred call below.
	repo := devices.NewPgx(pool, pool)
	defer repo.Close()

	pingCtx, pingCancel := context.WithTimeout(context.Background(), pgPingTimeout)
	if err := pool.Ping(pingCtx); err != nil {
		pingCancel()
		log.Error("postgres ping failed",
			zap.String("host", cfg.PGHost),
			zap.Int("port", cfg.PGPort),
			zap.Duration("timeout", pgPingTimeout),
			zap.Error(err),
		)
		return 2
	}
	pingCancel()
	log.Info("postgres connected",
		zap.String("host", cfg.PGHost),
		zap.Int("port", cfg.PGPort),
		zap.String("database", cfg.PGDatabase),
	)

	// The fan-in events channel. Owned by main. The scheduler's emit
	// closure writes into it; the consumer drains it.
	events := make(chan types.DiscoveryEvent, eventsBufferSize)

	emit := func(ev types.DiscoveryEvent) {
		select {
		case events <- ev:
		default:
			log.Warn("events channel full, dropping event",
				zap.String("name", ev.Name),
				zap.String("source_ip", ev.SourceIP),
			)
		}
	}

	discoveryJob := discovery.NewJob(
		time.Duration(cfg.DiscoveryIntervalSeconds)*time.Second,
		cfg.DiscoveryWorkerPoolSize,
		time.Duration(cfg.DiscoveryProbeTimeoutMs)*time.Millisecond,
		log,
	)

	scheduler := worker.New([]worker.Job{discoveryJob}, emit, log)
	consumer := discovery.NewConsumer(events, repo, log)

	// SIGINT/SIGTERM cancels ctx via signal.NotifyContext.
	ctx, stopSignals := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stopSignals()

	var schedWg sync.WaitGroup
	schedWg.Add(1)
	go func() {
		defer schedWg.Done()
		scheduler.Run(ctx)
	}()

	consumerDone := make(chan struct{})
	go func() {
		consumer.Start(ctx)
		close(consumerDone)
	}()

	// Block until a termination signal arrives.
	<-ctx.Done()
	log.Info("workers shutting down")

	// Idempotent — also covered by ctx propagation.
	scheduler.Stop()
	schedWg.Wait()

	// No more emit() calls possible; safe to close the events channel
	// so the consumer drains and exits cleanly.
	close(events)

	select {
	case <-consumerDone:
	case <-time.After(shutdownTimeout):
		log.Warn("consumer drain timeout",
			zap.Duration("timeout", shutdownTimeout),
		)
	}

	log.Info("workers stopped")
	return 0
}

// pgConnString builds the postgres:// URL consumed by pgxpool.New.
// Uses url.UserPassword so special characters in the password are
// percent-encoded automatically. sslmode=disable is intentional: the
// server is bound to 127.0.0.1 only and not reachable from the LAN,
// so loopback plaintext is acceptable until a follow-up adds TLS.
func pgConnString(cfg *config.Config) string {
	u := url.URL{
		Scheme: "postgres",
		User:   url.UserPassword(cfg.PGUser, cfg.PGPassword),
		Host:   fmt.Sprintf("%s:%d", cfg.PGHost, cfg.PGPort),
		Path:   "/" + cfg.PGDatabase,
	}
	q := u.Query()
	q.Set("sslmode", "disable")
	u.RawQuery = q.Encode()
	return u.String()
}
