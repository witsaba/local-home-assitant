// workers is the composition root for the periodic-background-job host.
// It wires: config → logger → singleton db pool → devices repo → events channel
// → emit → scheduler (with discovery) → consumer → signal.NotifyContext
// → graceful drain → repo close → exit 0.
package main

import (
	"context"
	"fmt"
	"os"
	"os/signal"
	"sync"
	"syscall"
	"time"

	"go.uber.org/zap"

	"github.com/witsaba/local-home-assitant/services/workers/internal/infrastructure/config"
	"github.com/witsaba/local-home-assitant/services/workers/internal/infrastructure/db"
	"github.com/witsaba/local-home-assitant/services/workers/internal/infrastructure/devices"
	loggerinfra "github.com/witsaba/local-home-assitant/services/workers/internal/infrastructure/logger"
	"github.com/witsaba/local-home-assitant/services/workers/internal/jobs/discovery"
	"github.com/witsaba/local-home-assitant/services/workers/internal/jobs/surveillance"
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
		zap.Int("surveillance_interval_min", cfg.SurveillanceIntervalMinutes),
		zap.Int("surveillance_capture_timeout_s", cfg.SurveillanceCaptureTimeoutSeconds),
		zap.Int("surveillance_device_freshness_min", cfg.SurveillanceDeviceFreshnessMinutes),
		zap.String("surveillance_root_dir", cfg.SurveillanceRootDir),
		zap.Stringer("db_url", cfg.ToPoolConfig()),
	)

	// Initialize the singleton pool once at startup.
	// sync.Once guarantees thread-safe one-time initialization.
	if err := db.Open(cfg.ToPoolConfig()); err != nil {
		log.Error("db pool open failed",
			zap.Error(err),
			zap.Stringer("db_url", cfg.ToPoolConfig()),
		)
		return 2
	}
	// Ensure pool is closed on shutdown.
	defer db.Close()

	// Verify connectivity.
	pingCtx, pingCancel := context.WithTimeout(context.Background(), pgPingTimeout)
	if err := db.Ping(pingCtx); err != nil {
		pingCancel()
		log.Error("db ping failed",
			zap.Duration("timeout", pgPingTimeout),
			zap.Error(err),
		)
		return 2
	}
	pingCancel()
	log.Info("db pool connected",
		zap.Stringer("db_url", cfg.ToPoolConfig()),
	)

	// Get the singleton pool instance for the repository.
	pool := db.Get()

	// Build the devices repository using the singleton pool.
	// The repository shares the pool's lifecycle (Close is called via db.Close).
	repo := devices.NewPgx(pool)

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

	// Surveillance job — reads witsaba.devices (populated by the
	// discovery job) and captures one JPEG per camera per tick.
	// The job does NOT emit DiscoveryEvents — the shared events
	// channel stays clean of non-discovery events. It writes
	// files to cfg.SurveillanceRootDir (default
	// ~/.witsaba/cameras/) and logs at INFO/WARN per tick.
	surveillanceJob := surveillance.NewJob(
		time.Duration(cfg.SurveillanceIntervalMinutes)*time.Minute,
		time.Duration(cfg.SurveillanceCaptureTimeoutSeconds)*time.Second,
		time.Duration(cfg.SurveillanceDeviceFreshnessMinutes)*time.Minute,
		cfg.SurveillanceRootDir,
		repo,
		log,
	)

	scheduler := worker.New([]worker.Job{discoveryJob, surveillanceJob}, emit, log)
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

	// db.Close() is called via defer above.
	log.Info("workers stopped")
	return 0
}
