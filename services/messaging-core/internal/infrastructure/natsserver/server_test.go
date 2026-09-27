package natsserver_test

import (
	"context"
	"net"
	"strings"
	"testing"
	"time"

	"github.com/nats-io/nats-server/v2/server"

	"github.com/witsaba/local-home-assitant/services/messaging-core/internal/application/ports"
	loggerinfra "github.com/witsaba/local-home-assitant/services/messaging-core/internal/infrastructure/logger"
	"github.com/witsaba/local-home-assitant/services/messaging-core/internal/infrastructure/natsserver"
)

// _ silences "imported and not used" if the helpers above change.
// Kept narrow intentionally — only the symbols we actually depend on.
var (
	_ = server.NewServer
	_ = net.Listen
)

// discardLogger silences everything during tests so they don't
// interleave output between parallel runs.
func discardLogger(t *testing.T) ports.Logger {
	t.Helper()
	l, err := loggerinfra.New("error")
	if err != nil {
		t.Fatalf("loggerinfra.New: %v", err)
	}
	t.Cleanup(func() { _ = l.Sync() })
	return l
}

func TestEmbeddedServer_StartsAndReportsReady(t *testing.T) {
	log := discardLogger(t)
	s, err := natsserver.New(natsserver.Config{Host: "127.0.0.1", Port: -1}, log)
	if err != nil {
		t.Fatalf("natsserver.New: %v", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	startErr := make(chan error, 1)
	go func() { startErr <- s.Start(ctx) }()

	select {
	case err := <-startErr:
		if err != nil {
			t.Fatalf("Start returned error: %v", err)
		}
	case <-time.After(8 * time.Second):
		t.Fatal("Start did not return within 8s")
	}

	// Ready() must be closed once the server is accepting clients.
	select {
	case <-s.Ready():
	case <-time.After(time.Second):
		t.Fatal("Ready channel was not closed")
	}

	addr := s.Addr()
	if !strings.HasPrefix(addr, "nats://") {
		t.Fatalf("Addr() should be a nats:// URL, got %q", addr)
	}
	if strings.Contains(addr, ":0") {
		t.Fatalf("Addr still has placeholder port: %q", addr)
	}

	if err := s.Shutdown(context.Background()); err != nil {
		t.Fatalf("Shutdown: %v", err)
	}
}

func TestEmbeddedServer_RejectsReuseOfStart(t *testing.T) {
	s, err := natsserver.New(natsserver.Config{Host: "127.0.0.1", Port: -1}, discardLogger(t))
	if err != nil {
		t.Fatalf("natsserver.New: %v", err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	if err := s.Start(ctx); err != nil {
		t.Fatalf("first Start: %v", err)
	}
	t.Cleanup(func() { _ = s.Shutdown(context.Background()) })

	if err := s.Start(ctx); err == nil {
		t.Fatal("expected error on second Start, got nil")
	}
}

func TestEmbeddedServer_ShutdownWithoutStartIsNoop(t *testing.T) {
	s, err := natsserver.New(natsserver.Config{Host: "127.0.0.1", Port: -1}, discardLogger(t))
	if err != nil {
		t.Fatalf("natsserver.New: %v", err)
	}
	if err := s.Shutdown(context.Background()); err != nil {
		t.Fatalf("Shutdown without Start: %v", err)
	}
}

func TestNew_RejectsEmptyHost(t *testing.T) {
	if _, err := natsserver.New(natsserver.Config{Host: "", Port: 4222}, discardLogger(t)); err == nil {
		t.Fatal("expected error for empty host, got nil")
	}
}

func TestNew_RejectsNilLogger(t *testing.T) {
	if _, err := natsserver.New(natsserver.Config{Host: "127.0.0.1", Port: 4222}, nil); err == nil {
		t.Fatal("expected error for nil logger, got nil")
	}
}
