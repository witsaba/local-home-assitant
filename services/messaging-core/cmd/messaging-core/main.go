// messaging-core is the Go backend service that boots its own
// embedded NATS server and exposes it to the rest of the local-home
// stack. This file is the composition root — it wires the
// environment-driven configuration, the zap logger (wired through
// the otelzap bridge), and the embedded NATS server, and handles
// SIGINT / SIGTERM for graceful shutdown.
package main

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/witsaba/local-home-assitant/services/messaging-core/internal/application/ports"
	"github.com/witsaba/local-home-assitant/services/messaging-core/internal/infrastructure/config"
	loggerinfra "github.com/witsaba/local-home-assitant/services/messaging-core/internal/infrastructure/logger"
	"github.com/witsaba/local-home-assitant/services/messaging-core/internal/infrastructure/natsserver"
)

// version is stamped into the binary. Override at build time with
// `go build -ldflags '-X main.version=v0.2.0'` if you need release
// builds.
var version = "0.1.0-dev"

// shutdownTimeout caps how long the composition root waits for the
// embedded server to drain after a shutdown signal.
const shutdownTimeout = 10 * time.Second

func main() {
	os.Exit(run())
}

// run is the real entry point; returning int keeps it unit-testable.
func run() int {
	cfg, err := config.Load()
	if err != nil {
		// Logger is not yet built; emit plain text to stderr and
		// exit with a distinct code so operators see config errors.
		fmt.Fprintf(os.Stderr, "messaging-core: config load failed: %v\n", err)
		return 2
	}

	log, err := loggerinfra.New(cfg.LogLevel)
	if err != nil {
		fmt.Fprintf(os.Stderr, "messaging-core: logger init failed: %v\n", err)
		return 2
	}
	defer func() { _ = log.Sync() }()

	log.Info("messaging-core starting",
		ports.Field{Key: "version", Value: version},
		ports.Field{Key: "host", Value: cfg.Host},
		ports.Field{Key: "port", Value: cfg.Port},
		ports.Field{Key: "log_level", Value: cfg.LogLevel},
	)

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	srv, err := natsserver.New(natsserver.Config{Host: cfg.Host, Port: cfg.Port}, log)
	if err != nil {
		log.Error("constructing NATS server failed",
			ports.Field{Key: "err", Value: err.Error()},
		)
		return 1
	}

	if err := srv.Start(ctx); err != nil {
		// If the caller's context was cancelled (SIGINT/SIGTERM
		// arrived during boot) we treat that as a graceful shutdown
		// request rather than a startup failure.
		if ctx.Err() != nil {
			log.Info("startup cancelled by signal",
				ports.Field{Key: "err", Value: err.Error()},
			)
			shutdownCtx, cancelShutdown := context.WithTimeout(context.Background(), shutdownTimeout)
			defer cancelShutdown()
			if err := srv.Shutdown(shutdownCtx); err != nil && !errors.Is(err, context.Canceled) {
				log.Error("shutdown error",
					ports.Field{Key: "err", Value: err.Error()},
				)
				return 1
			}
			log.Info("messaging-core stopped cleanly")
			return 0
		}
		log.Error("starting NATS server failed",
			ports.Field{Key: "err", Value: err.Error()},
		)
		return 1
	}

	// Block until SIGINT / SIGTERM arrives.
	<-ctx.Done()
	log.Info("shutdown signal received")

	shutdownCtx, cancel := context.WithTimeout(context.Background(), shutdownTimeout)
	defer cancel()

	if err := srv.Shutdown(shutdownCtx); err != nil && !errors.Is(err, context.Canceled) {
		log.Error("shutdown error",
			ports.Field{Key: "err", Value: err.Error()},
		)
		return 1
	}

	log.Info("messaging-core stopped cleanly")
	return 0
}
