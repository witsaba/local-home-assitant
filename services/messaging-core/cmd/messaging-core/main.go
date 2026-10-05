// messaging-core is the Go backend service that boots its own
// embedded NATS server and exposes it to the rest of the local-home
// stack. This file is the composition root — it wires the
// environment-driven configuration, the zap logger (wired through
// the otelzap bridge), the embedded NATS server, the Postgres pool,
// the camera streaming gateway, and the WS server, and handles
// SIGINT / SIGTERM for graceful shutdown.
package main

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/witsaba/local-home-assitant/services/messaging-core/internal/application/ports"
	"github.com/witsaba/local-home-assitant/services/messaging-core/internal/infrastructure/api"
	"github.com/witsaba/local-home-assitant/services/messaging-core/internal/infrastructure/config"
	"github.com/witsaba/local-home-assitant/services/messaging-core/internal/infrastructure/db"
	"github.com/witsaba/local-home-assitant/services/messaging-core/internal/infrastructure/devices"
	"github.com/witsaba/local-home-assitant/services/messaging-core/internal/infrastructure/gallery"
	loggerinfra "github.com/witsaba/local-home-assitant/services/messaging-core/internal/infrastructure/logger"
	"github.com/witsaba/local-home-assitant/services/messaging-core/internal/infrastructure/natsserver"
	"github.com/witsaba/local-home-assitant/services/messaging-core/internal/infrastructure/streamhub"
	"github.com/witsaba/local-home-assitant/services/messaging-core/internal/infrastructure/wsserver"
)

// version is stamped into the binary. Override at build time with
// `go build -ldflags '-X main.version=v0.2.0'` if you need release
// builds.
var version = "0.1.0-dev"

// shutdownTimeout caps how long the composition root waits for
// servers to drain after a shutdown signal.
const shutdownTimeout = 10 * time.Second

// pgPingTimeout caps how long startup waits for Postgres to accept a connection.
const pgPingTimeout = 5 * time.Second

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
		ports.Field{Key: "nats_host", Value: cfg.Host},
		ports.Field{Key: "nats_port", Value: cfg.Port},
		ports.Field{Key: "stream_port", Value: cfg.STREAMPort},
		ports.Field{Key: "log_level", Value: cfg.LogLevel},
		ports.Field{Key: "pg_host", Value: cfg.PGHost},
		ports.Field{Key: "pg_port", Value: cfg.PGPort},
		ports.Field{Key: "pg_database", Value: cfg.PGDatabase},
	)

	// — SIGINT/SIGTERM context —
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	// — NATS server —
	natssrv, err := natsserver.New(natsserver.Config{Host: cfg.Host, Port: cfg.Port}, log)
	if err != nil {
		log.Error("constructing NATS server failed",
			ports.Field{Key: "err", Value: err.Error()},
		)
		return 1
	}

	if err := natssrv.Start(ctx); err != nil {
		if ctx.Err() != nil {
			// Signal arrived during boot — treat as graceful shutdown.
			log.Info("startup cancelled by signal",
				ports.Field{Key: "err", Value: err.Error()},
			)
			shutdownCtx, cancelShutdown := context.WithTimeout(context.Background(), shutdownTimeout)
			defer cancelShutdown()
			if err := natssrv.Shutdown(shutdownCtx); err != nil && !errors.Is(err, context.Canceled) {
				log.Error("shutdown error",
					ports.Field{Key: "err", Value: err.Error()},
				)
			}
			log.Info("messaging-core stopped cleanly")
			return 0
		}
		log.Error("starting NATS server failed",
			ports.Field{Key: "err", Value: err.Error()},
		)
		return 1
	}

	// — Postgres pool —
	poolCfg := &db.PoolConfig{
		Host:            cfg.PGHost,
		Port:            cfg.PGPort,
		Database:        cfg.PGDatabase,
		User:            cfg.PGUser,
		Password:        cfg.PGPassword,
		MaxConns:        cfg.PGMaxConns,
		MinConns:        cfg.PGMinConns,
		MaxConnLifetime: cfg.PGMaxConnLifetime,
		MaxConnIdleTime: cfg.PGMaxConnIdleTime,
	}
	if err := db.Open(poolCfg); err != nil {
		log.Error("opening Postgres pool failed",
			ports.Field{Key: "err", Value: err.Error()},
		)
		return 1
	}

	// Verify connectivity.
	pingCtx, pingCancel := context.WithTimeout(context.Background(), pgPingTimeout)
	if err := db.Ping(pingCtx); err != nil {
		pingCancel()
		log.Error("pinging Postgres failed",
			ports.Field{Key: "err", Value: err.Error()},
		)
		return 1
	}
	pingCancel()

	log.Info("Postgres pool connected",
		ports.Field{Key: "pg_host", Value: cfg.PGHost},
		ports.Field{Key: "pg_port", Value: cfg.PGPort},
	)

	// — Camera streaming gateway —
	devRepo := devices.NewPgx(db.Get())
	camHub := streamhub.NewCameraHub(devRepo, log)
	wsSrv := wsserver.NewServer(
		fmt.Sprintf(":%d", cfg.STREAMPort),
		camHub,
		devRepo,
		log,
	)
	wsSrv.Start()

	// — REST API server (GET /api/devices/active, /api/gallery/*) —
	//
	// The gallery store is read-only over the capture root the workers
	// service writes. A missing root is not fatal: it only means no
	// captures exist yet, and the endpoints report an empty gallery.
	galleryStore := gallery.NewStore(cfg.GalleryRootDir)
	apiHandler := api.NewHandler(devRepo, galleryStore, log)
	apiSrv := &http.Server{
		Addr:    fmt.Sprintf(":%d", cfg.APIPort),
		Handler: apiHandler,
	}
	go func() {
		if err := apiSrv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			log.Error("API server failed",
				ports.Field{Key: "err", Value: err.Error()},
				ports.Field{Key: "addr", Value: apiSrv.Addr},
			)
		}
	}()
	log.Info("API server started",
		ports.Field{Key: "addr", Value: apiSrv.Addr},
		ports.Field{Key: "gallery_root", Value: cfg.GalleryRootDir},
	)

	// — Block on SIGINT / SIGTERM —
	log.Info("messaging-core fully started",
		ports.Field{Key: "nats", Value: fmt.Sprintf("%s:%d", cfg.Host, cfg.Port)},
		ports.Field{Key: "stream", Value: fmt.Sprintf(":%d", cfg.STREAMPort)},
		ports.Field{Key: "api", Value: fmt.Sprintf(":%d", cfg.APIPort)},
	)

	<-ctx.Done()
	log.Info("shutdown signal received")

	// — Graceful shutdown sequence —
	shutdownCtx, cancel := context.WithTimeout(context.Background(), shutdownTimeout)
	defer cancel()

	// 1. Shutdown the API server (drains in-flight requests).
	if err := apiSrv.Shutdown(shutdownCtx); err != nil && !errors.Is(err, context.Canceled) {
		log.Error("API server shutdown error",
			ports.Field{Key: "err", Value: err.Error()},
		)
	}

	// 2. Close the stream hub (disconnects all chip clients, notifies viewers).
	camHub.Close(shutdownCtx)

	// 3. Close the WS server.
	if err := wsSrv.Close(); err != nil {
		log.Error("closing WS server failed",
			ports.Field{Key: "err", Value: err.Error()},
		)
	}

	// 4. Close the Postgres pool.
	db.Close()

	// 5. Shutdown NATS.
	if err := natssrv.Shutdown(shutdownCtx); err != nil && !errors.Is(err, context.Canceled) {
		log.Error("NATS shutdown error",
			ports.Field{Key: "err", Value: err.Error()},
		)
	}

	log.Info("messaging-core stopped cleanly")
	return 0
}
