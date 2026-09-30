package discovery_test

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"
	"go.uber.org/zap/zaptest/observer"

	"github.com/witsaba/local-home-assitant/services/workers/internal/infrastructure/devices"
	"github.com/witsaba/local-home-assitant/services/workers/internal/jobs/discovery"
	"github.com/witsaba/local-home-assitant/services/workers/internal/types"
)

// fakeRepo records every Upsert call and returns a configurable error.
type fakeRepo struct {
	mu      sync.Mutex
	calls   []types.DiscoveryEvent
	failErr error // returned by every Upsert; nil means success
}

func (f *fakeRepo) Upsert(_ context.Context, ev types.DiscoveryEvent) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls = append(f.calls, ev)
	return f.failErr
}

func (f *fakeRepo) ListFresh(_ context.Context, _ time.Time) ([]types.DiscoveryEvent, error) {
	// Unused by discovery tests. Return an empty slice so the
	// type still satisfies devices.Repository.
	return nil, nil
}

func (f *fakeRepo) Close() {}

func (f *fakeRepo) Calls() []types.DiscoveryEvent {
	f.mu.Lock()
	defer f.mu.Unlock()
	out := make([]types.DiscoveryEvent, len(f.calls))
	copy(out, f.calls)
	return out
}

func TestConsumer_PersistsAndLogsEvents(t *testing.T) {
	t.Parallel()

	core, recorded := observer.New(zapcore.InfoLevel)
	logger := zap.New(core)
	repo := &fakeRepo{}

	events := make(chan types.DiscoveryEvent, 3)
	consumer := discovery.NewConsumer(events, repo, logger)

	evts := []types.DiscoveryEvent{
		{SourceIP: "192.168.1.10", Name: "cam-01", MAC: "aa:bb:cc:dd:ee:01", FW: "1.0", Chip: "esp32", DiscoveredAt: time.Now().UTC()},
		{SourceIP: "192.168.1.11", Name: "cam-02", MAC: "aa:bb:cc:dd:ee:02", FW: "1.1", Chip: "esp32", DiscoveredAt: time.Now().UTC()},
		{SourceIP: "192.168.1.12", Name: "cam-03", MAC: "aa:bb:cc:dd:ee:03", FW: "1.2", Chip: "esp32", DiscoveredAt: time.Now().UTC()},
	}

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		consumer.Start(ctx)
		close(done)
	}()

	for _, ev := range evts {
		events <- ev
	}
	close(events)
	cancel()

	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("consumer did not exit")
	}
	wg.Wait()

	if got := len(repo.Calls()); got != len(evts) {
		t.Fatalf("expected %d Upsert calls, got %d", len(evts), got)
	}
	for i, ev := range evts {
		if repo.Calls()[i].MAC != ev.MAC {
			t.Errorf("Upsert[%d] MAC=%q want %q", i, repo.Calls()[i].MAC, ev.MAC)
		}
	}

	all := recorded.All()
	if len(all) != len(evts) {
		t.Fatalf("expected %d log records, got %d", len(evts), len(all))
	}
	for i, ev := range evts {
		r := all[i]
		if r.Message != "witsaba device found" {
			t.Errorf("record %d: message=%q", i, r.Message)
		}
		ctxMap := r.ContextMap()
		if got := asString(ctxMap["source_ip"]); got != ev.SourceIP {
			t.Errorf("record %d: source_ip=%q want %q", i, got, ev.SourceIP)
		}
		if got := asString(ctxMap["name"]); got != ev.Name {
			t.Errorf("record %d: name=%q want %q", i, got, ev.Name)
		}
		if got, want := ctxMap["upsert_ok"], true; got != want {
			t.Errorf("record %d: upsert_ok=%v want %v", i, got, want)
		}
	}
}

func TestConsumer_LogsUpsertFailureAtWarn(t *testing.T) {
	t.Parallel()

	core, recorded := observer.New(zapcore.DebugLevel)
	logger := zap.New(core)
	boom := errors.New("connection refused")
	repo := &fakeRepo{failErr: boom}

	events := make(chan types.DiscoveryEvent, 1)
	consumer := discovery.NewConsumer(events, repo, logger)

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		consumer.Start(ctx)
		close(done)
	}()

	events <- types.DiscoveryEvent{SourceIP: "192.168.1.10", MAC: "aa:bb:cc:dd:ee:01"}
	close(events)
	cancel()

	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("consumer did not exit")
	}

	all := recorded.All()
	if len(all) != 1 {
		t.Fatalf("expected 1 log record, got %d", len(all))
	}
	r := all[0]
	if r.Message != "witsaba device upsert failed" {
		t.Errorf("expected message 'witsaba device upsert failed', got %q", r.Message)
	}
	if r.Level != zapcore.WarnLevel {
		t.Errorf("expected WARN level, got %v", r.Level)
	}
	ctxMap := r.ContextMap()
	if got, want := ctxMap["upsert_ok"], false; got != want {
		t.Errorf("upsert_ok=%v want %v", got, want)
	}
}

func TestConsumer_KeepsRunningAfterUpsertError(t *testing.T) {
	t.Parallel()

	boom := errors.New("connection refused")
	repo := &fakeRepo{failErr: boom}

	events := make(chan types.DiscoveryEvent, 3)
	consumer := discovery.NewConsumer(events, repo, zap.NewNop())

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		consumer.Start(ctx)
		close(done)
	}()

	for i := 0; i < 3; i++ {
		events <- types.DiscoveryEvent{MAC: "aa:bb:cc:dd:ee:01"}
	}
	close(events)
	cancel()

	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("consumer did not exit")
	}

	if got := len(repo.Calls()); got != 3 {
		t.Fatalf("expected 3 Upsert calls despite errors, got %d", got)
	}
}

func TestConsumer_ExitsOnCtxCancel(t *testing.T) {
	t.Parallel()

	events := make(chan types.DiscoveryEvent, 10)
	consumer := discovery.NewConsumer(events, &fakeRepo{}, zap.NewNop())

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		consumer.Start(ctx)
		close(done)
	}()

	time.Sleep(10 * time.Millisecond)
	cancel()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("consumer did not exit after ctx cancel")
	}
}

func TestConsumer_ExitsWhenChannelClosed(t *testing.T) {
	t.Parallel()

	events := make(chan types.DiscoveryEvent, 3)
	consumer := discovery.NewConsumer(events, &fakeRepo{}, zap.NewNop())

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		consumer.Start(ctx)
		close(done)
	}()

	events <- types.DiscoveryEvent{SourceIP: "192.168.1.10", Name: "cam"}
	close(events)

	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("consumer did not exit when channel closed")
	}
	cancel()
}

// Compile-time assertion: devices.Noop satisfies the Repository
// contract the consumer depends on. If the interface drifts, this
// line fails to build before any test runs.
var _ devices.Repository = (*devices.Noop)(nil)

func asString(v any) string {
	if v == nil {
		return ""
	}
	if s, ok := v.(string); ok {
		return s
	}
	return ""
}
