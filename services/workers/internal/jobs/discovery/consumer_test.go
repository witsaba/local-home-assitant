package discovery_test

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/witsaba/local-home-assitant/services/workers/internal/jobs/discovery"
	"github.com/witsaba/local-home-assitant/services/workers/internal/types"
	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"
	"go.uber.org/zap/zaptest/observer"
)

func TestConsumer_LogsEvents(t *testing.T) {
	t.Parallel()

	core, recorded := observer.New(zapcore.InfoLevel)
	logger := zap.New(core)

	events := make(chan types.DiscoveryEvent, 3)
	consumer := discovery.NewConsumer(events, logger)

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

	all := recorded.All()
	if len(all) != 3 {
		t.Fatalf("expected 3 log records, got %d", len(all))
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
	}
}

func TestConsumer_ExitsOnCtxCancel(t *testing.T) {
	t.Parallel()

	events := make(chan types.DiscoveryEvent, 10)
	consumer := discovery.NewConsumer(events, zap.NewNop())

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
	consumer := discovery.NewConsumer(events, zap.NewNop())

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

func asString(v any) string {
	if v == nil {
		return ""
	}
	if s, ok := v.(string); ok {
		return s
	}
	return ""
}
