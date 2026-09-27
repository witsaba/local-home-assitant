// Package natsserver is the embedded-NATS implementation of
// ports.Server. It boots an in-process NATS server using
// github.com/nats-io/nats-server/v2 — no external broker required.
package natsserver

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"time"

	"github.com/nats-io/nats-server/v2/server"

	"github.com/witsaba/local-home-assitant/services/messaging-core/internal/application/ports"
)

// Config is the public configuration accepted by New.
//
// Port of -1 asks the OS to assign a free port (intended for tests).
// Any other value is taken literally.
type Config struct {
	Host string
	Port int
}

// serverName is the human-readable identifier embedded NATS exposes
// to clients via the INFO protocol.
const serverName = "messaging-core"

// bootDeadline caps the time we wait for the embedded server to
// become ready before giving up.
const bootDeadline = 5 * time.Second

// New constructs (but does not start) a NATS-backed ports.Server.
func New(cfg Config, log ports.Logger) (ports.Server, error) {
	if cfg.Host == "" {
		return nil, errors.New("natsserver: Host must not be empty")
	}
	if log == nil {
		return nil, errors.New("natsserver: logger must not be nil")
	}

	opts := &server.Options{
		ServerName: serverName,
		Host:       cfg.Host,
		Port:       cfg.Port,
		// Keep the embedded server quiet and out of the way of our
		// own signal handling and structured logger.
		NoLog:  true,
		NoSigs: true,
	}

	srv, err := server.NewServer(opts)
	if err != nil {
		return nil, fmt.Errorf("natsserver: NewServer: %w", err)
	}
	if srv == nil {
		return nil, errors.New("natsserver: server.NewServer returned nil server")
	}

	s := &embeddedServer{
		srv:  srv,
		host: cfg.Host,
		log:  log,
	}
	s.ready = make(chan struct{})
	return s, nil
}

// embeddedServer wraps *server.Server so it satisfies ports.Server.
type embeddedServer struct {
	srv *server.Server
	// host is the bind address requested by the caller; preserved
	// separately because srv.Addr() reports the listen-side net.Addr
	// and is not safe to read until the server is up.
	host  string
	log   ports.Logger
	ready chan struct{}

	mu      sync.Mutex
	started bool
	url     string
	err     error
}

func (s *embeddedServer) Start(ctx context.Context) error {
	s.mu.Lock()
	if s.started {
		s.mu.Unlock()
		return errors.New("natsserver: Start already called")
	}
	s.started = true
	s.mu.Unlock()

	s.log.Info("NATS server starting", ports.Field{Key: "host", Value: s.host})

	// server.Server.Start returns immediately and runs the listener
	// and accept loop in internal goroutines.
	startErrCh := make(chan error, 1)
	go func() {
		defer func() {
			if r := recover(); r != nil {
				startErrCh <- fmt.Errorf("natsserver: panic in Start: %v", r)
			}
		}()
		s.srv.Start()
		startErrCh <- nil
	}()

	// Poll until ReadyForConnections returns true or we hit the boot
	// deadline / caller's context.
	pollCtx, cancel := context.WithTimeout(ctx, bootDeadline)
	defer cancel()

	for {
		if s.srv.ReadyForConnections(50 * time.Millisecond) {
			break
		}
		select {
		case <-pollCtx.Done():
			return fmt.Errorf("natsserver: server did not become ready: %w", pollCtx.Err())
		case err := <-startErrCh:
			if err != nil {
				return err
			}
			// startErrCh fires when Start() returns; we ignore the
			// nil because the server runs in the background.
		case <-time.After(20 * time.Millisecond):
			// busy-loop a tiny bit then poll ReadyForConnections again.
		}
	}

	// Build the client dial URL. ClientURL() converts "0.0.0.0" into
	// "127.0.0.1" automatically so callers always get a usable host.
	s.mu.Lock()
	s.url = s.srv.ClientURL()
	s.mu.Unlock()

	close(s.ready)
	s.log.Info("NATS server listening",
		ports.Field{Key: "url", Value: s.url},
		ports.Field{Key: "name", Value: serverName},
	)
	return nil
}

func (s *embeddedServer) Ready() <-chan struct{} {
	return s.ready
}

func (s *embeddedServer) Addr() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.url
}

func (s *embeddedServer) Shutdown(ctx context.Context) error {
	s.mu.Lock()
	started := s.started
	s.mu.Unlock()
	if !started {
		// Never started — nothing to drain.
		return nil
	}

	s.log.Info("NATS server shutting down")
	s.srv.Shutdown() // non-blocking; signals the server to stop

	done := make(chan error, 1)
	go func() {
		s.srv.WaitForShutdown()
		done <- nil
	}()

	select {
	case err := <-done:
		s.log.Info("NATS server stopped")
		return err
	case <-ctx.Done():
		return fmt.Errorf("natsserver: shutdown deadline exceeded: %w", ctx.Err())
	}
}

// Compile-time guarantee that embeddedServer satisfies ports.Server.
var _ ports.Server = (*embeddedServer)(nil)
