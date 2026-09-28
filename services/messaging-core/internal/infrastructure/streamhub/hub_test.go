package streamhub

import (
	"context"
	"errors"
	"net"
	"sync"
	"testing"

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
	dev   *devices.Device
	err   error
	devs  map[string]*devices.Device // keyed by MAC
	getBy func(mac string) (*devices.Device, error)
}

func (f *fakeDeviceRepo) GetByMAC(_ context.Context, mac string) (*devices.Device, error) {
	if f.getBy != nil {
		return f.getBy(mac)
	}
	if f.devs != nil {
		if d, ok := f.devs[mac]; ok {
			return d, nil
		}
		return nil, devices.NotFoundError
	}
	if f.err != nil {
		return nil, f.err
	}
	if f.dev == nil {
		return nil, devices.NotFoundError
	}
	return f.dev, nil
}

func newFakeDevice(mac, ip string) *devices.Device {
	return &devices.Device{
		MAC:          mac,
		Name:         "test-cam",
		FW:           "0.1.0",
		Chip:         "esp32cam",
		LastSourceIP: net.ParseIP(ip),
	}
}

func TestCameraHub_RegisterViewer_UnknownMAC(t *testing.T) {
	hub := NewCameraHub(&fakeDeviceRepo{}, &fakeLogger{})
	hub.injectMockChipClient() // prevent real chip connection attempts

	conn := &websocket.Conn{}
	err := hub.RegisterViewer(context.Background(), "unknown", conn)
	if err == nil {
		t.Error("expected error for unknown MAC, got nil")
	}
}

func TestCameraHub_RegisterViewer_NoIP(t *testing.T) {
	hub := NewCameraHub(&fakeDeviceRepo{dev: &devices.Device{
		MAC: "abc123",
		// LastSourceIP is nil
	}}, &fakeLogger{})
	hub.injectMockChipClient()

	conn := &websocket.Conn{}
	err := hub.RegisterViewer(context.Background(), "abc123", conn)
	if err == nil {
		t.Error("expected error for device with no IP, got nil")
	}
}

func TestCameraHub_RegisterViewer_DBDown(t *testing.T) {
	hub := NewCameraHub(&fakeDeviceRepo{err: errors.New("connection lost")}, &fakeLogger{})
	hub.injectMockChipClient()

	conn := &websocket.Conn{}
	err := hub.RegisterViewer(context.Background(), "abc123", conn)
	if err == nil {
		t.Error("expected error when DB is down, got nil")
	}
}

func TestCameraHub_GetViewerCount(t *testing.T) {
	devs := &fakeDeviceRepo{devs: map[string]*devices.Device{
		"abc": newFakeDevice("abc", "192.168.1.50"),
	}}
	hub := NewCameraHub(devs, &fakeLogger{})
	hub.injectMockChipClient()

	if count := hub.GetViewerCount("abc"); count != 0 {
		t.Errorf("viewer count before registration: got %d, want 0", count)
	}
}

func TestCameraHub_GetChipIP(t *testing.T) {
	devs := &fakeDeviceRepo{devs: map[string]*devices.Device{
		"abc": newFakeDevice("abc", "192.168.1.50"),
	}}
	hub := NewCameraHub(devs, &fakeLogger{})
	hub.injectMockChipClient()

	if ip := hub.GetChipIP("abc"); ip != "" {
		t.Errorf("chip IP before registration: got %q, want empty", ip)
	}
}

func TestCameraHub_DeregisterViewer_UnknownCamera(t *testing.T) {
	hub := NewCameraHub(&fakeDeviceRepo{}, &fakeLogger{})
	hub.injectMockChipClient()
	// Must not panic.
	hub.DeregisterViewer(context.Background(), "unknown", &websocket.Conn{})
}

func TestCameraHub_Close(t *testing.T) {
	hub := NewCameraHub(&fakeDeviceRepo{}, &fakeLogger{})
	// Must not panic.
	hub.Close(context.Background())
	// Close is idempotent.
	hub.Close(context.Background())
}

func TestIPString(t *testing.T) {
	tests := []struct {
		name string
		ip   net.IP
		want string
	}{
		{"nil IP", nil, ""},
		{"valid IP", net.ParseIP("192.168.1.1"), "192.168.1.1"},
		{"IPv6", net.ParseIP("::1"), "::1"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := IPString(tt.ip); got != tt.want {
				t.Errorf("IPString(%v): got %q, want %q", tt.ip, got, tt.want)
			}
		})
	}
}

func TestCameraHub_ConcurrentRegisterDeregister(t *testing.T) {
	devs := &fakeDeviceRepo{devs: map[string]*devices.Device{
		"abc": newFakeDevice("abc", "192.168.1.50"),
	}}
	hub := NewCameraHub(devs, &fakeLogger{})
	hub.injectMockChipClient()

	const n = 10
	conns := make([]*websocket.Conn, n)

	// Register n viewers concurrently.
	var wg sync.WaitGroup
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func(idx int) {
			defer wg.Done()
			conn := &websocket.Conn{}
			conns[idx] = conn
			_ = hub.RegisterViewer(context.Background(), "abc", conn)
		}(i)
	}
	wg.Wait()

	if count := hub.GetViewerCount("abc"); count != n {
		t.Errorf("viewer count after %d registrations: got %d, want %d", n, count, n)
	}

	// Deregister the same connections.
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func(idx int) {
			defer wg.Done()
			hub.DeregisterViewer(context.Background(), "abc", conns[idx])
		}(i)
	}
	wg.Wait()

	if count := hub.GetViewerCount("abc"); count != 0 {
		t.Errorf("viewer count after deregistration: got %d, want 0", count)
	}
}

func TestCameraHub_RegisterViewer_ChipIPChange(t *testing.T) {
	mu := sync.Mutex{}
	currentIP := "192.168.1.50"

	devs := &fakeDeviceRepo{
		getBy: func(mac string) (*devices.Device, error) {
			mu.Lock()
			ip := currentIP
			mu.Unlock()
			return &devices.Device{
				MAC:          mac,
				LastSourceIP: net.ParseIP(ip),
			}, nil
		},
	}

	hub := NewCameraHub(devs, &fakeLogger{})
	hub.injectMockChipClient()
	conn := &websocket.Conn{}

	// First registration.
	err := hub.RegisterViewer(context.Background(), "abc", conn)
	if err != nil {
		t.Fatalf("first registration failed: %v", err)
	}

	// Simulate the device getting a new IP.
	mu.Lock()
	currentIP = "192.168.1.99"
	mu.Unlock()

	// Second registration should detect the IP change.
	err = hub.RegisterViewer(context.Background(), "abc", conn)
	if err != nil {
		t.Fatalf("second registration failed: %v", err)
	}

	if ip := hub.GetChipIP("abc"); ip != "192.168.1.99" {
		t.Errorf("chip IP after change: got %q, want %q", ip, "192.168.1.99")
	}
}

func TestCameraHub_GetViewerCount_UnknownCamera(t *testing.T) {
	hub := NewCameraHub(&fakeDeviceRepo{}, &fakeLogger{})
	if count := hub.GetViewerCount("unknown"); count != 0 {
		t.Errorf("viewer count for unknown camera: got %d, want 0", count)
	}
}

func TestCameraHub_GetChipIP_UnknownCamera(t *testing.T) {
	hub := NewCameraHub(&fakeDeviceRepo{}, &fakeLogger{})
	if ip := hub.GetChipIP("unknown"); ip != "" {
		t.Errorf("chip IP for unknown camera: got %q, want empty", ip)
	}
}
