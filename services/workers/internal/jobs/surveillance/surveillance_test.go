// surveillance_test.go — integration-style tests for the Job.Run flow.
// Uses httptest.Server for /capture, fakeDevicesRepo for witsaba.devices,
// fakeClock for deterministic boundary times, and t.TempDir() for
// the storage root.
package surveillance

import (
	"bytes"
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"go.uber.org/zap"

	"github.com/witsaba/local-home-assitant/services/workers/internal/infrastructure/devices"
	"github.com/witsaba/local-home-assitant/services/workers/internal/types"
)

// fakeDevicesRepo satisfies devices.Repository for Job tests.
type fakeDevicesRepo struct {
	mu      sync.Mutex
	rows    []types.DiscoveryEvent
	listErr error // returned by ListFresh
	cutoffs []time.Time
}

func (f *fakeDevicesRepo) Upsert(_ context.Context, _ types.DiscoveryEvent) error {
	return nil
}

func (f *fakeDevicesRepo) ListFresh(_ context.Context, cutoff time.Time) ([]types.DiscoveryEvent, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.cutoffs = append(f.cutoffs, cutoff)
	if f.listErr != nil {
		return nil, f.listErr
	}
	out := make([]types.DiscoveryEvent, len(f.rows))
	copy(out, f.rows)
	return out, nil
}

func (f *fakeDevicesRepo) Close() {}

// noopEmitter discards every DiscoveryEvent the Job would emit.
// The surveillance job does not emit events, but worker.Job
// requires the parameter; tests pass this to satisfy the type.
func noopEmitter(_ types.DiscoveryEvent) {}

// newTestJob returns a Job wired with the supplied dependencies.
// Pass nil client/clock to use the production defaults.
func newTestJob(t *testing.T, repo devices.Repository, clock Clock, client *http.Client, root string, window Window) *Job {
	t.Helper()
	j := NewJob(
		15*time.Minute,
		2*time.Second,
		5*time.Minute,
		root,
		repo,
		zap.NewNop(),
	)
	if client != nil {
		j.SetHTTPClient(client)
	}
	if clock != nil {
		j.SetClock(clock)
	}
	if window != (Window{}) {
		j.SetWindow(window)
	}
	return j
}

func TestJob_Run_NoDevicesLogsAtInfo(t *testing.T) {
	t.Parallel()

	repo := &fakeDevicesRepo{}
	j := newTestJob(t, repo, &fakeClock{now: time.Now()}, nil, t.TempDir(), Window{})

	if err := j.Run(context.Background(), noopEmitter); err != nil {
		t.Fatalf("Run: %v", err)
	}
	if len(repo.cutoffs) != 1 {
		t.Errorf("ListFresh called %d times, want 1", len(repo.cutoffs))
	}
}

// errRepoBoom is a sentinel error for fakeDevicesRepo.
var errRepoBoom = errors.New("repo boom")

func TestJob_Run_RepoErrorDoesNotFailTick(t *testing.T) {
	t.Parallel()

	repo := &fakeDevicesRepo{listErr: errRepoBoom}
	j := newTestJob(t, repo, &fakeClock{now: time.Now()}, nil, t.TempDir(), Window{})

	if err := j.Run(context.Background(), noopEmitter); err != nil {
		t.Fatalf("Run returned %v, want nil (per-camera failures are non-fatal)",
			err)
	}
}

// captureServer builds an httptest.Server that mimics the
// iot_cams /capture endpoint. The `failHosts` map (by original
// source IP) lists devices that should return 500; everyone else
// gets a 200 with `body` bytes. The handler also records hit
// counts so tests can verify which paths were exercised.
//
// The original source IP is recovered from the X-Test-Source-IP
// header that rewriteTransport stamps on every request — the
// test transport rewrites the URL.Host to point at the loopback
// server, so r.URL.Hostname() would always be 127.0.0.1 without
// this extra channel.
type captureServer struct {
	mu        sync.Mutex
	hits      int
	flashOn   int
	failHosts map[string]bool
	body      []byte
}

func (s *captureServer) handler(w http.ResponseWriter, r *http.Request) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.hits++
	if r.URL.Query().Get("flash") == "1" {
		s.flashOn++
	}
	sourceIP := r.Header.Get("X-Test-Source-IP")
	if s.failHosts[sourceIP] {
		http.Error(w, "boom", http.StatusInternalServerError)
		return
	}
	_, _ = w.Write(s.body)
}

func TestJob_Run_HappyPath_WritesFiles(t *testing.T) {
	t.Parallel()

	cs := &captureServer{
		failHosts: map[string]bool{},
		body:      []byte("fake-jpeg"),
	}
	srv := httptest.NewServer(http.HandlerFunc(cs.handler))
	defer srv.Close()

	addr := srv.Listener.Addr().String() // "127.0.0.1:NNNNN"
	transport := &rewriteTransport{addr: addr, base: http.DefaultTransport}

	repo := &fakeDevicesRepo{
		rows: []types.DiscoveryEvent{
			{MAC: "aabbccddeeff", SourceIP: "10.0.0.1", DiscoveredAt: time.Now()},
			{MAC: "112233445566", SourceIP: "10.0.0.2", DiscoveredAt: time.Now()},
		},
	}
	// 21:30 = inside the default 17:45→05:45 window.
	clock := &fakeClock{now: time.Date(2026, 9, 30, 21, 30, 0, 0, time.Local)}
	root := t.TempDir()

	client := &http.Client{Timeout: 2 * time.Second, Transport: transport}
	j := newTestJob(t, repo, clock, client, root, Window{})

	if err := j.Run(context.Background(), noopEmitter); err != nil {
		t.Fatalf("Run: %v", err)
	}

	if cs.hits != 2 {
		t.Errorf("server hit count = %d, want 2", cs.hits)
	}
	if cs.flashOn != 2 {
		t.Errorf("server saw ?flash=1 = %d, want 2", cs.flashOn)
	}

	dayDir := filepath.Join(root, "2026-09-30")
	entries, err := os.ReadDir(dayDir)
	if err != nil {
		t.Fatalf("readDir %s: %v", dayDir, err)
	}
	if len(entries) != 2 {
		names := make([]string, len(entries))
		for i, e := range entries {
			names[i] = e.Name()
		}
		t.Errorf("dayDir entries = %v, want 2 files", names)
	}
	for _, e := range entries {
		body, err := os.ReadFile(filepath.Join(dayDir, e.Name()))
		if err != nil {
			t.Fatalf("readback %s: %v", e.Name(), err)
		}
		if !bytes.Equal(body, []byte("fake-jpeg")) {
			t.Errorf("body = %q, want 'fake-jpeg'", body)
		}
	}
}

func TestJob_Run_OutsideFlashWindow_NoFlashQueryParam(t *testing.T) {
	t.Parallel()

	cs := &captureServer{failHosts: map[string]bool{}, body: []byte("x")}
	srv := httptest.NewServer(http.HandlerFunc(cs.handler))
	defer srv.Close()

	transport := &rewriteTransport{addr: srv.Listener.Addr().String(), base: http.DefaultTransport}

	repo := &fakeDevicesRepo{
		rows: []types.DiscoveryEvent{
			{MAC: "aabbccddeeff", SourceIP: "10.0.0.1", DiscoveredAt: time.Now()},
		},
	}
	// 12:00 noon = OUTSIDE the 17:45→05:45 window.
	clock := &fakeClock{now: time.Date(2026, 9, 30, 12, 0, 0, 0, time.Local)}
	client := &http.Client{Timeout: 2 * time.Second, Transport: transport}

	j := newTestJob(t, repo, clock, client, t.TempDir(), Window{})

	if err := j.Run(context.Background(), noopEmitter); err != nil {
		t.Fatalf("Run: %v", err)
	}
	if cs.flashOn != 0 {
		t.Errorf("server saw ?flash=1 = %d, want 0 (noon, outside window)", cs.flashOn)
	}
}

func TestJob_Run_PerCameraFailureIsLoggedNotFatal(t *testing.T) {
	t.Parallel()

	// 10.0.0.2 always returns 500; everyone else returns 200.
	cs := &captureServer{
		failHosts: map[string]bool{"10.0.0.2": true},
		body:      []byte("ok"),
	}
	srv := httptest.NewServer(http.HandlerFunc(cs.handler))
	defer srv.Close()

	transport := &rewriteTransport{addr: srv.Listener.Addr().String(), base: http.DefaultTransport}

	repo := &fakeDevicesRepo{
		rows: []types.DiscoveryEvent{
			{MAC: "good", SourceIP: "10.0.0.1", DiscoveredAt: time.Now()},
			{MAC: "fail", SourceIP: "10.0.0.2", DiscoveredAt: time.Now()},
		},
	}
	clock := &fakeClock{now: time.Date(2026, 9, 30, 12, 0, 0, 0, time.Local)}
	client := &http.Client{Timeout: 2 * time.Second, Transport: transport}

	j := newTestJob(t, repo, clock, client, t.TempDir(), Window{})

	// The "fail" camera returns 500 → Job logs WARN and continues.
	// The "good" camera returns 200 → file written.
	if err := j.Run(context.Background(), noopEmitter); err != nil {
		t.Fatalf("Run: %v", err)
	}

	dayDir := filepath.Join(j.storage.Root, "2026-09-30")
	entries, err := os.ReadDir(dayDir)
	if err != nil {
		t.Fatalf("readDir %s: %v", dayDir, err)
	}
	if len(entries) != 1 {
		names := make([]string, len(entries))
		for i, e := range entries {
			names[i] = e.Name()
		}
		t.Errorf("expected exactly 1 file (good camera), got %d: %v",
			len(entries), names)
	}
}

func TestJob_Run_DeviceMissingMACIsSkipped(t *testing.T) {
	t.Parallel()

	repo := &fakeDevicesRepo{
		rows: []types.DiscoveryEvent{
			{MAC: "", SourceIP: "10.0.0.1", DiscoveredAt: time.Now()},
			{MAC: "ok", SourceIP: ""}, // missing SourceIP too
		},
	}
	j := newTestJob(t, repo, &fakeClock{now: time.Now()},
		&http.Client{Timeout: 1 * time.Second}, t.TempDir(), Window{})

	if err := j.Run(context.Background(), noopEmitter); err != nil {
		t.Fatalf("Run: %v", err)
	}
	root := j.storage.Root
	dayDir := filepath.Join(root, time.Now().Format("2006-01-02"))
	if _, err := os.Stat(dayDir); err == nil {
		t.Errorf("dayDir %s unexpectedly exists — empty-MAC devices should be skipped",
			dayDir)
	}
}

func TestJob_Run_ContextCancelledMidTick(t *testing.T) {
	t.Parallel()

	repo := &fakeDevicesRepo{
		rows: []types.DiscoveryEvent{
			{MAC: "aabb", SourceIP: "10.0.0.1", DiscoveredAt: time.Now()},
		},
	}
	j := newTestJob(t, repo, &fakeClock{now: time.Now()},
		&http.Client{Timeout: 1 * time.Second}, t.TempDir(), Window{})

	ctx, cancel := context.WithCancel(context.Background())
	cancel() // pre-cancel

	err := j.Run(ctx, noopEmitter)
	if err == nil {
		t.Fatal("Run(cancelled ctx) returned nil, want ctx error")
	}
}

func TestJob_Defaults(t *testing.T) {
	t.Parallel()

	j := NewJob(0, 0, 0, "/tmp", &fakeDevicesRepo{}, zap.NewNop())
	if j.Interval() != 15*time.Minute {
		t.Errorf("Interval() = %v, want 15m", j.Interval())
	}
	if j.Name() != "surveillance" {
		t.Errorf("Name() = %q, want 'surveillance'", j.Name())
	}
	if j.captureTimeout != 10*time.Second {
		t.Errorf("captureTimeout = %v, want 10s", j.captureTimeout)
	}
	if j.freshness != 5*time.Minute {
		t.Errorf("freshness = %v, want 5m", j.freshness)
	}
	if j.window.StartHour != 17 || j.window.EndHour != 5 {
		t.Errorf("window = %v, want 17→5", j.window)
	}
}

// --- helpers ---

// rewriteTransport rewrites every request's URL.Host to the
// supplied test server address. The path/query is preserved so
// buildCaptureURL's output (with or without ?flash=1) is
// exercised end-to-end against the test handler.
//
// Also stamps the original SourceIP into X-Test-Source-IP so
// the captureServer handler can decide per-camera behavior
// without parsing the rewritten URL.
type rewriteTransport struct {
	addr string
	base http.RoundTripper
}

func (r *rewriteTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	c := req.Clone(req.Context())
	u := *req.URL
	u.Scheme = "http"
	u.Host = r.addr
	c.URL = &u
	c.Host = r.addr
	if u.Hostname() != "" {
		c.Header.Set("X-Test-Source-IP", req.URL.Hostname())
	}
	return r.base.RoundTrip(c)
}
