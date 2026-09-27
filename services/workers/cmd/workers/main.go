// workers is the composition root for the periodic-background-job host.
// It wires: config → logger → events channel → emit → scheduler (with
// discovery) → consumer → signal.NotifyContext → graceful drain → exit 0.
package main

import (
	"context"
	"fmt"
	"os"
	"os/signal"
	"sync"
	"syscall"
	"time"

	"github.com/witsaba/local-home-assitant/services/workers/internal/infrastructure/config"
	loggerinfra "github.com/witsaba/local-home-assitant/services/workers/internal/infrastructure/logger"
	"github.com/witsaba/local-home-assitant/services/workers/internal/jobs/discovery"
	"github.com/witsaba/local-home-assitant/services/workers/internal/types"
	"github.com/witsaba/local-home-assitant/services/workers/internal/worker"
	"go.uber.org/zap"
)

// version is stamped at build time via -ldflags.
var version = "0.1.0-dev"

// shutdownTimeout caps how long we give the consumer to drain pending
// events after a SIGTERM. Long enough for any plausible in-flight scan
// to finish, short enough that operators don't rage-quit.
const shutdownTimeout = 10 * time.Second

// eventsBufferSize is the capacity of the fan-in events channel.
const eventsBufferSize = 256

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
	consumer := discovery.NewConsumer(events, log)

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
