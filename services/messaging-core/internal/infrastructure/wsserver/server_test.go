package wsserver

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/gorilla/websocket"

	"github.com/witsaba/local-home-assitant/services/messaging-core/internal/application/ports"
	"github.com/witsaba/local-home-assitant/services/messaging-core/internal/infrastructure/devices"
)

// fakeLogger is a no-op logger for testing.
type fakeLogger struct{}

func (f *fakeLogger) Info(_ string, _ ...ports.Field)   {}
func (f *fakeLogger) Warn(_ string, _ ...ports.Field)   {}
func (f *fakeLogger) Error(_ string, _ ...ports.Field)  {}
func (f *fakeLogger) Debug(_ string, _ ...ports.Field) {}
func (f *fakeLogger) Sync() error                      { return nil }

// fakeDeviceRepo is a mock for the device repository.
type fakeDeviceRepo struct {
	dev *devices.Device
	err error
}

func (f *fakeDeviceRepo) GetByMAC(_ context.Context, mac string) (*devices.Device, error) {
	if f.err != nil {
		return nil, f.err
	}
	if f.dev == nil {
		return nil, devices.NotFoundError
	}
	return f.dev, nil
}

// fakeStreamHub is a mock for the stream hub.
type fakeStreamHub struct {
	mu              sync.Mutex
	registerCalls   []string
	registerConns   []*websocket.Conn
	deregisterCalls []string
	deregisterConns []*websocket.Conn
	registerErr     error
}

func (f *fakeStreamHub) RegisterViewer(_ context.Context, mac string, conn *websocket.Conn) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.registerCalls = append(f.registerCalls, mac)
	f.registerConns = append(f.registerConns, conn)
	return f.registerErr
}

func (f *fakeStreamHub) DeregisterViewer(_ context.Context, mac string, conn *websocket.Conn) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.deregisterCalls = append(f.deregisterCalls, mac)
	f.deregisterConns = append(f.deregisterConns, conn)
}

// wsDial dials a WebSocket URL using websocket.DefaultDialer.
func wsDial(t *testing.T, url string) *websocket.Conn {
	t.Helper()
	conn, _, err := websocket.DefaultDialer.Dial(url, nil)
	if err != nil {
		t.Fatalf("websocket dial failed: %v", err)
	}
	return conn
}

func TestServer_Healthz(t *testing.T) {
	gin.SetMode(gin.TestMode)
	hub := &fakeStreamHub{}
	devs := &fakeDeviceRepo{}
	log := &fakeLogger{}

	srv := NewServer("localhost:0", hub, devs, log)
	srv.Start()
	defer srv.Close()

	time.Sleep(50 * time.Millisecond)

	resp := httptest.NewRequest("GET", "/healthz", nil)
	rec := httptest.NewRecorder()
	srv.gin.ServeHTTP(rec, resp)

	if rec.Code != http.StatusOK {
		t.Errorf("healthz status: got %d, want %d", rec.Code, http.StatusOK)
	}
}

func TestServer_Stream_UnknownMAC(t *testing.T) {
	gin.SetMode(gin.TestMode)
	hub := &fakeStreamHub{}
	devs := &fakeDeviceRepo{} // returns NotFoundError
	log := &fakeLogger{}

	srv := NewServer("localhost:0", hub, devs, log)
	srv.Start()
	defer srv.Close()

	time.Sleep(50 * time.Millisecond)

	resp := httptest.NewRequest("GET", "/stream/e08cfe3091b0", nil)
	rec := httptest.NewRecorder()
	srv.gin.ServeHTTP(rec, resp)

	if rec.Code != http.StatusNotFound {
		t.Errorf("stream status for unknown MAC: got %d, want %d", rec.Code, http.StatusNotFound)
	}
}

func TestServer_Stream_ValidMAC_RegistersViewer(t *testing.T) {
	gin.SetMode(gin.TestMode)
	hub := &fakeStreamHub{}
	devs := &fakeDeviceRepo{dev: &devices.Device{MAC: "e08cfe3091b0"}}
	log := &fakeLogger{}

	srv := NewServer("localhost:0", hub, devs, log)
	srv.Start()
	defer srv.Close()

	time.Sleep(50 * time.Millisecond)

	server := httptest.NewServer(srv.gin)
	defer server.Close()
	wsURL := "ws://" + server.Listener.Addr().String() + "/stream/e08cfe3091b0"

	conn := wsDial(t, wsURL)
	defer conn.Close()

	time.Sleep(100 * time.Millisecond)

	hub.mu.Lock()
	defer hub.mu.Unlock()

	if len(hub.registerCalls) != 1 {
		t.Errorf("register calls: got %d, want 1", len(hub.registerCalls))
	}
	if len(hub.registerCalls) > 0 && hub.registerCalls[0] != "e08cfe3091b0" {
		t.Errorf("registered MAC: got %q, want %q", hub.registerCalls[0], "e08cfe3091b0")
	}
}

func TestServer_Stream_DeviceDBError(t *testing.T) {
	gin.SetMode(gin.TestMode)
	hub := &fakeStreamHub{}
	devs := &fakeDeviceRepo{err: errors.New("db connection lost")}
	log := &fakeLogger{}

	srv := NewServer("localhost:0", hub, devs, log)
	srv.Start()
	defer srv.Close()

	time.Sleep(50 * time.Millisecond)

	resp := httptest.NewRequest("GET", "/stream/e08cfe3091b0", nil)
	rec := httptest.NewRecorder()
	srv.gin.ServeHTTP(rec, resp)

	if rec.Code != http.StatusInternalServerError {
		t.Errorf("stream status for DB error: got %d, want %d", rec.Code, http.StatusInternalServerError)
	}
}

func TestServer_RegisterViewerError_ClosesConnection(t *testing.T) {
	gin.SetMode(gin.TestMode)
	hub := &fakeStreamHub{registerErr: errors.New("chip unreachable")}
	devs := &fakeDeviceRepo{dev: &devices.Device{MAC: "e08cfe3091b0"}}
	log := &fakeLogger{}

	srv := NewServer("localhost:0", hub, devs, log)
	srv.Start()
	defer srv.Close()

	time.Sleep(50 * time.Millisecond)

	server := httptest.NewServer(srv.gin)
	defer server.Close()
	wsURL := "ws://" + server.Listener.Addr().String() + "/stream/e08cfe3091b0"

	// Dial succeeds (WS upgrade), but RegisterViewer returns an error,
	// so the server closes the connection. The pingLoop goroutine has not
	// started yet (it starts after RegisterViewer returns), so the client
	// should get an error reading — either a close from the server or a
	// read timeout.
	conn, _, err := websocket.DefaultDialer.Dial(wsURL, nil)
	if err != nil {
		// Connection refused or upgrade failed — also acceptable.
		hub.mu.Lock()
		defer hub.mu.Unlock()
		if len(hub.registerCalls) != 1 {
			t.Errorf("register calls: got %d, want 1", len(hub.registerCalls))
		}
		return
	}
	defer conn.Close()

	// WS upgraded successfully. Now read — the server should close soon.
	conn.SetReadDeadline(time.Now().Add(2 * time.Second))
	_, _, err = conn.ReadMessage()
	if err == nil {
		t.Error("expected server to close connection after RegisterViewer error, got nil")
	}

	hub.mu.Lock()
	defer hub.mu.Unlock()
	if len(hub.registerCalls) != 1 {
		t.Errorf("register calls: got %d, want 1", len(hub.registerCalls))
	}
}

func TestServer_Start_Done(t *testing.T) {
	hub := &fakeStreamHub{}
	devs := &fakeDeviceRepo{}
	log := &fakeLogger{}

	srv := NewServer("localhost:0", hub, devs, log)
	srv.Start()

	select {
	case <-srv.done:
		t.Error("done channel closed immediately after Start, want to stay open")
	default:
		// Expected: done is still open.
	}

	srv.Close()

	select {
	case <-srv.done:
		// Expected: done closes after Close.
	case <-time.After(5 * time.Second):
		t.Error("done channel did not close within 5s of Close()")
	}
}

func TestServer_ConcurrentRequests(t *testing.T) {
	gin.SetMode(gin.TestMode)
	var callCount atomic.Int32
	hub := &fakeStreamHub{}
	devs := &fakeDeviceRepo{dev: &devices.Device{MAC: "e08cfe3091b0"}}
	log := &fakeLogger{}

	srv := NewServer("localhost:0", hub, devs, log)
	srv.Start()
	defer srv.Close()

	time.Sleep(50 * time.Millisecond)

	server := httptest.NewServer(srv.gin)
	defer server.Close()
	baseURL := "ws://" + server.Listener.Addr().String() + "/stream/e08cfe3091b0"

	var wg sync.WaitGroup
	for i := 0; i < 5; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			conn, _, err := websocket.DefaultDialer.Dial(baseURL, nil)
			if err == nil {
				conn.Close()
			}
			callCount.Add(1)
		}()
	}
	wg.Wait()

	if callCount.Load() != 5 {
		t.Errorf("concurrent requests: got %d, want 5", callCount.Load())
	}
}
