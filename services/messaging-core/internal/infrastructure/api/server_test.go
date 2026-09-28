package api

import (
	"context"
	"encoding/json"
	"errors"
	"net"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/witsaba/local-home-assitant/services/messaging-core/internal/application/ports"
	"github.com/witsaba/local-home-assitant/services/messaging-core/internal/infrastructure/devices"
)

// mockRepo is a fake DeviceRepository for handler tests.
type mockRepo struct {
	devs     []*devices.Device
	listErr  error
}

func (m *mockRepo) GetByMAC(ctx context.Context, mac string) (*devices.Device, error) {
	return nil, nil
}

func (m *mockRepo) ListActive(ctx context.Context, maxAge time.Duration) ([]*devices.Device, error) {
	if m.listErr != nil {
		return nil, m.listErr
	}
	return m.devs, nil
}

// mockLogger records log calls for assertions.
type mockLogger struct {
	calls []string
}

func (l *mockLogger) Debug(msg string, fields ...ports.Field) { l.calls = append(l.calls, "debug:"+msg) }
func (l *mockLogger) Info(msg string, fields ...ports.Field)  { l.calls = append(l.calls, "info:"+msg) }
func (l *mockLogger) Warn(msg string, fields ...ports.Field)  { l.calls = append(l.calls, "warn:"+msg) }
func (l *mockLogger) Error(msg string, fields ...ports.Field) { l.calls = append(l.calls, "error:"+msg) }
func (l *mockLogger) Sync() error                              { return nil }

func TestListActive_ReturnsTwoDevices(t *testing.T) {
	now := time.Now().Truncate(time.Second)
	repo := &mockRepo{
		devs: []*devices.Device{
			{
				MAC:          "e08cfe3091b0",
				Name:         "kitchen-cam",
				FW:           "0.1.0",
				Chip:         "esp32cam",
				LastSourceIP: net.ParseIP("192.168.1.100"),
				LastSeenAt:   now,
			},
			{
				MAC:          "d4e9f48d381c",
				Name:         "garage-cam",
				FW:           "0.2.0",
				Chip:         "esp32s3",
				LastSourceIP: nil,
				LastSeenAt:   now.Add(-30 * time.Second),
			},
		},
	}

	h := NewHandler(repo, &mockLogger{})
	req := httptest.NewRequest("GET", "/api/devices/active", nil)
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Errorf("status: got %d, want %d", rr.Code, http.StatusOK)
	}
	if ct := rr.Header().Get("Content-Type"); ct != "application/json" {
		t.Errorf("Content-Type: got %q, want %q", ct, "application/json")
	}

	var out []deviceResponse
	if err := json.NewDecoder(rr.Body).Decode(&out); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if len(out) != 2 {
		t.Fatalf("len(out): got %d, want 2", len(out))
	}
	if out[0].MAC != "e08cfe3091b0" {
		t.Errorf("out[0].MAC: got %q, want %q", out[0].MAC, "e08cfe3091b0")
	}
	if out[1].LastSourceIP != "" {
		t.Errorf("out[1].LastSourceIP: got %q, want empty (nil IP)", out[1].LastSourceIP)
	}
	if out[0].LastSeenAt == "" {
		t.Error("out[0].LastSeenAt: expected non-empty RFC3339 string")
	}
}

func TestListActive_EmptyResult(t *testing.T) {
	repo := &mockRepo{devs: nil}
	h := NewHandler(repo, &mockLogger{})
	req := httptest.NewRequest("GET", "/api/devices/active", nil)
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Errorf("status: got %d, want %d", rr.Code, http.StatusOK)
	}

	var out []deviceResponse
	if err := json.NewDecoder(rr.Body).Decode(&out); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if len(out) != 0 {
		t.Errorf("len(out): got %d, want 0", len(out))
	}
}

func TestListActive_InternalError(t *testing.T) {
	repo := &mockRepo{listErr: errors.New("database unavailable")}
	log := &mockLogger{}
	h := NewHandler(repo, log)
	req := httptest.NewRequest("GET", "/api/devices/active", nil)
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)

	if rr.Code != http.StatusInternalServerError {
		t.Errorf("status: got %d, want %d", rr.Code, http.StatusInternalServerError)
	}
	found := false
	for _, c := range log.calls {
		if c == "error:GET /api/devices/active: ListActive failed" {
			found = true
		}
	}
	if !found {
		t.Error("expected error log call")
	}
}

func TestListActive_WrongMethod(t *testing.T) {
	repo := &mockRepo{}
	h := NewHandler(repo, &mockLogger{})
	req := httptest.NewRequest("POST", "/api/devices/active", nil)
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)

	// Mux returns 405 for wrong method on registered path
	if rr.Code != http.StatusMethodNotAllowed {
		t.Errorf("status: got %d, want %d", rr.Code, http.StatusMethodNotAllowed)
	}
}
