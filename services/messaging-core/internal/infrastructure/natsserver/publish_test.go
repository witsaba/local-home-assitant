package natsserver_test

import (
	"context"
	"testing"
	"time"

	"github.com/nats-io/nats.go"

	"github.com/witsaba/local-home-assitant/services/messaging-core/internal/infrastructure/natsserver"
)

// TestEmbeddedServer_ReceivesPublishedMessage is the acceptance test
// for the whole branch: an embedded NATS server boots, accepts a
// client connection, and a NATS publish-and-subscribe round-trip
// succeeds against it. This is what "the server must be able to
// receive messages" means in practice.
func TestEmbeddedServer_ReceivesPublishedMessage(t *testing.T) {
	s, err := natsserver.New(
		natsserver.Config{Host: "127.0.0.1", Port: -1},
		discardLogger(t),
	)
	if err != nil {
		t.Fatalf("natsserver.New: %v", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	if err := s.Start(ctx); err != nil {
		t.Fatalf("Start: %v", err)
	}
	t.Cleanup(func() {
		shutdownCtx, c := context.WithTimeout(context.Background(), 5*time.Second)
		defer c()
		_ = s.Shutdown(shutdownCtx)
	})

	<-s.Ready()

	nc, err := nats.Connect(s.Addr(),
		nats.Timeout(2*time.Second),
		nats.Name("test-publisher"),
	)
	if err != nil {
		t.Fatalf("nats.Connect(%s): %v", s.Addr(), err)
	}
	t.Cleanup(func() {
		if err := nc.Drain(); err != nil {
			t.Logf("nc.Drain: %v", err)
		}
	})

	subject := "test.subject"
	payload := []byte(`{"hello":"world"}`)

	received := make(chan []byte, 1)

	sub, err := nc.Subscribe(subject, func(msg *nats.Msg) {
		// Copy the payload — the underlying buffer is reused by NATS
		// when the handler returns on some connection types.
		buf := make([]byte, len(msg.Data))
		copy(buf, msg.Data)
		select {
		case received <- buf:
		default:
		}
	})
	if err != nil {
		t.Fatalf("nc.Subscribe: %v", err)
	}
	t.Cleanup(func() { _ = sub.Unsubscribe() })

	// Give the server a beat to register the subscription before we
	// publish; with NoSigs/NoLog the embedded broker is quiet but
	// the subscription registration is still asynchronous.
	time.Sleep(50 * time.Millisecond)

	if err := nc.Publish(subject, payload); err != nil {
		t.Fatalf("nc.Publish: %v", err)
	}
	if err := nc.FlushTimeout(2 * time.Second); err != nil {
		t.Fatalf("nc.FlushTimeout: %v", err)
	}

	select {
	case got := <-received:
		if string(got) != string(payload) {
			t.Fatalf("payload mismatch: got %q, want %q", got, payload)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("did not receive published message within 2s")
	}
}

// TestEmbeddedServer_AcceptsMultipleSubscribers extends the
// round-trip above with N subscribers and confirms fan-out works
// inside a single embedded server.
func TestEmbeddedServer_AcceptsMultipleSubscribers(t *testing.T) {
	s, err := natsserver.New(
		natsserver.Config{Host: "127.0.0.1", Port: -1},
		discardLogger(t),
	)
	if err != nil {
		t.Fatalf("natsserver.New: %v", err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := s.Start(ctx); err != nil {
		t.Fatalf("Start: %v", err)
	}
	t.Cleanup(func() {
		ctx, c := context.WithTimeout(context.Background(), 5*time.Second)
		defer c()
		_ = s.Shutdown(ctx)
	})
	<-s.Ready()

	nc, err := nats.Connect(s.Addr())
	if err != nil {
		t.Fatalf("nats.Connect: %v", err)
	}
	t.Cleanup(func() { _ = nc.Drain() })

	const N = 5
	subject := "test.fanout"
	subs := make([]*nats.Subscription, 0, N)
	chans := make([]chan []byte, 0, N)
	for i := 0; i < N; i++ {
		ch := make(chan []byte, 1)
		sub, err := nc.Subscribe(subject, func(msg *nats.Msg) {
			buf := make([]byte, len(msg.Data))
			copy(buf, msg.Data)
			select {
			case ch <- buf:
			default:
			}
		})
		if err != nil {
			t.Fatalf("nc.Subscribe[%d]: %v", i, err)
		}
		subs = append(subs, sub)
		chans = append(chans, ch)
	}
	t.Cleanup(func() {
		for _, sub := range subs {
			_ = sub.Unsubscribe()
		}
	})
	time.Sleep(50 * time.Millisecond)

	if err := nc.Publish(subject, []byte("payload")); err != nil {
		t.Fatalf("nc.Publish: %v", err)
	}
	if err := nc.FlushTimeout(2 * time.Second); err != nil {
		t.Fatalf("nc.FlushTimeout: %v", err)
	}

	for i, ch := range chans {
		select {
		case got := <-ch:
			if string(got) != "payload" {
				t.Fatalf("subscriber %d payload mismatch: got %q", i, got)
			}
		case <-time.After(2 * time.Second):
			t.Fatalf("subscriber %d did not receive the message", i)
		}
	}
}
