package wsclient

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/gorilla/websocket"

	"github.com/witsaba/local-home-assitant/services/messaging-core/internal/application/ports"
)

// fakeLogger is a no-op logger for testing.
type fakeLogger struct{}

func (f *fakeLogger) Info(_ string, _ ...ports.Field)   {}
func (f *fakeLogger) Warn(_ string, _ ...ports.Field)   {}
func (f *fakeLogger) Error(_ string, _ ...ports.Field) {}
func (f *fakeLogger) Debug(_ string, _ ...ports.Field) {}
func (f *fakeLogger) Sync() error                      { return nil }

func TestChipClient_Connect_Success(t *testing.T) {
	// Start a test WS server that sends hello + one binary frame.
	var server *httptest.Server
	var upgrader = websocket.Upgrader{}

	helloSent := make(chan struct{})
	binarySent := make(chan struct{})

	server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, _ := upgrader.Upgrade(w, r, nil)
		_ = conn
		defer conn.Close()

		// Send hello text frame.
		conn.WriteMessage(websocket.TextMessage, []byte(`{"type":"hello","mac":"abc123"}`))
		close(helloSent)

		// Send one binary frame.
		conn.WriteMessage(websocket.BinaryMessage, []byte{0xFF, 0xD8, 0xFF, 0xE0})
		<-binarySent
	}))
	defer server.Close()

	// Extract host from server URL (ws://HOST:PORT).
	addr := server.Listener.Addr().String()

	var frames [][]byte
	var mu sync.Mutex

	onFrame := func(data []byte) {
		mu.Lock()
		frames = append(frames, data)
		mu.Unlock()
	}

	log := &fakeLogger{}
	client := NewChipClient(addr, onFrame, func() {}, log)

	ctx := context.Background()
	if err := client.Connect(ctx); err != nil {
		t.Fatalf("Connect failed: %v", err)
	}

	// Wait for hello to be received.
	<-helloSent

	// Signal binary was sent.
	close(binarySent)

	// Give the client a moment to process.
	time.Sleep(100 * time.Millisecond)

	mu.Lock()
	if len(frames) != 1 {
		t.Errorf("frames: got %d, want 1", len(frames))
	}
	if len(frames) > 0 && string(frames[0]) != string([]byte{0xFF, 0xD8, 0xFF, 0xE0}) {
		t.Errorf("frame: got %x, want %x", frames[0], []byte{0xFF, 0xD8, 0xFF, 0xE0})
	}
	mu.Unlock()

	// Close client.
	client.Close(context.Background())
}

func TestChipClient_Connect_InvalidHost(t *testing.T) {
	log := &fakeLogger{}
	client := NewChipClient("localhost:99999", func([]byte) {}, func() {}, log)

	ctx := context.Background()
	err := client.Connect(ctx)
	if err == nil {
		t.Fatal("expected error for unreachable host, got nil")
	}
}

func TestChipClient_IsConnected(t *testing.T) {
	upgrader := websocket.Upgrader{}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, _ := upgrader.Upgrade(w, r, nil)
		_ = conn
		// Hold the connection open.
		<-make(chan struct{})
	}))
	defer server.Close()

	addr := server.Listener.Addr().String()
	log := &fakeLogger{}
	client := NewChipClient(addr, func([]byte) {}, func() {}, log)

	if client.IsConnected() {
		t.Error("IsConnected: got true before Connect, want false")
	}

	ctx := context.Background()
	if err := client.Connect(ctx); err != nil {
		t.Fatalf("Connect failed: %v", err)
	}

	if !client.IsConnected() {
		t.Error("IsConnected: got false after Connect, want true")
	}

	client.Close(context.Background())

	if client.IsConnected() {
		t.Error("IsConnected: got true after Close, want false")
	}
}

func TestChipClient_Close_AlreadyClosed(t *testing.T) {
	log := &fakeLogger{}
	client := NewChipClient("localhost:99999", func([]byte) {}, func() {}, log)

	// Close twice should not panic.
	client.Close(context.Background())
	client.Close(context.Background())
}

func TestIsCleanClose(t *testing.T) {
	tests := []struct {
		name string
		err  error
		want bool
	}{
		{"nil error", nil, false},
		{"network closed", errors.New("use of closed network connection"), true},
		{"close error normal", &websocket.CloseError{Code: 1000, Text: "bye"}, true},
		{"random error", errors.New("something broke"), false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := isCleanClose(tt.err); got != tt.want {
				t.Errorf("isCleanClose(%v): got %v, want %v", tt.err, got, tt.want)
			}
		})
	}
}

func TestChipClient_DialerExposed(t *testing.T) {
	// Verify that the Dialer variable is accessible and is a websocket.Dialer.
	if Dialer == nil {
		t.Error("Dialer: got nil, want non-nil *websocket.Dialer")
	}
}

func TestChipClient_Connect_AfterClose(t *testing.T) {
	upgrader := websocket.Upgrader{}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, _ := upgrader.Upgrade(w, r, nil)
		_ = conn
		conn.Close()
	}))
	defer server.Close()

	addr := server.Listener.Addr().String()
	log := &fakeLogger{}
	client := NewChipClient(addr, func([]byte) {}, func() {}, log)

	ctx := context.Background()
	if err := client.Connect(ctx); err != nil {
		t.Fatalf("Connect failed: %v", err)
	}

	// Give the read loop time to see the close.
	time.Sleep(50 * time.Millisecond)

	client.Close(context.Background())

	// Re-connecting should fail because the client is closed.
	err := client.Connect(ctx)
	if err == nil {
		t.Error("expected error on re-connect after Close, got nil")
	}
}

func TestChipClient_ChipDisconnected_Callback(t *testing.T) {
	var callCount atomic.Int32
	upgrader := websocket.Upgrader{}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, _ := upgrader.Upgrade(w, r, nil)
		_ = conn
		// Simulate chip disconnect after 50ms.
		time.Sleep(50 * time.Millisecond)
		conn.Close()
	}))
	defer server.Close()

	addr := server.Listener.Addr().String()
	log := &fakeLogger{}

	onCloseCalled := make(chan struct{})
	client := NewChipClient(addr, func([]byte) {}, func() {
		callCount.Add(1)
		close(onCloseCalled)
	}, log)

	ctx := context.Background()
	if err := client.Connect(ctx); err != nil {
		t.Fatalf("Connect failed: %v", err)
	}

	select {
	case <-onCloseCalled:
		// onClose was called as expected.
	case <-time.After(2 * time.Second):
		t.Fatal("onClose callback was not called within 2s")
	}

	if callCount.Load() != 1 {
		t.Errorf("onClose call count: got %d, want 1", callCount.Load())
	}
}
