package worker_test

import (
	"context"
	"sync/atomic"
	"testing"
	"time"

	"github.com/witsaba/local-home-assitant/services/workers/internal/types"
	"github.com/witsaba/local-home-assitant/services/workers/internal/worker"
	"go.uber.org/zap"
)

func newTestLogger() *zap.Logger { return zap.NewNop() }

func TestScheduler_PanicRecovered(t *testing.T) {
	t.Parallel()

	panicJob := &panickingJob{name: "panicker"}
	sched := worker.New([]worker.Job{panicJob}, noopEmit(), newTestLogger())

	ctx, cancel := context.WithCancel(context.Background())

	done := make(chan struct{})
	go func() {
		sched.Run(ctx)
		close(done)
	}()

	time.Sleep(50 * time.Millisecond)
	cancel()

	select {
	case <-done:
		// clean exit — the panic was recovered
	case <-time.After(2 * time.Second):
		t.Fatal("scheduler did not exit within 2 seconds")
	}
}

func TestScheduler_CtxCancelStopsAllJobs(t *testing.T) {
	t.Parallel()

	runCount := atomic.Int32{}
	job := &countingJob{name: "counter", runCount: &runCount, interval: 10 * time.Millisecond}
	sched := worker.New([]worker.Job{job}, noopEmit(), newTestLogger())

	ctx, cancel := context.WithCancel(context.Background())
	go sched.Run(ctx)
	time.Sleep(30 * time.Millisecond)
	cancel()

	select {
	case <-sched.Done():
	case <-time.After(2 * time.Second):
		t.Fatal("scheduler did not exit after ctx cancel")
	}

	if count := runCount.Load(); count == 0 {
		t.Error("expected at least one Run call before cancel")
	}
}

func TestScheduler_TickFires(t *testing.T) {
	t.Parallel()

	runCount := atomic.Int32{}
	job := &countingJob{name: "tick-test", runCount: &runCount, interval: 15 * time.Millisecond}
	sched := worker.New([]worker.Job{job}, noopEmit(), newTestLogger())

	ctx, cancel := context.WithCancel(context.Background())
	go sched.Run(ctx)

	time.Sleep(50 * time.Millisecond)
	cancel()
	<-sched.Done()

	count := runCount.Load()
	if count < 2 {
		t.Errorf("expected at least 2 ticks in 50ms with 15ms interval, got %d", count)
	}
}

func TestScheduler_Stop(t *testing.T) {
	t.Parallel()

	job := &countingJob{name: "stop-test", runCount: new(atomic.Int32), interval: 10 * time.Hour}
	sched := worker.New([]worker.Job{job}, noopEmit(), newTestLogger())

	ctx, cancel := context.WithCancel(context.Background())
	go sched.Run(ctx)
	time.Sleep(10 * time.Millisecond)

	sched.Stop()
	cancel()

	select {
	case <-sched.Done():
	case <-time.After(2 * time.Second):
		t.Fatal("scheduler did not exit after Stop()")
	}
}

func TestScheduler_EmitReachesProvidedClosure(t *testing.T) {
	t.Parallel()

	var emitted []types.DiscoveryEvent
	emitCh := make(chan types.DiscoveryEvent, 1)
	emit := func(ev types.DiscoveryEvent) {
		select {
		case emitCh <- ev:
		default:
		}
		emitted = append(emitted, ev)
	}

	producer := &emitJob{name: "producer", interval: 10 * time.Millisecond}
	sched := worker.New([]worker.Job{producer}, emit, newTestLogger())

	ctx, cancel := context.WithCancel(context.Background())
	go sched.Run(ctx)

	select {
	case ev := <-emitCh:
		if ev.Name != "evicted" {
			t.Errorf("unexpected event name: %q", ev.Name)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("emit was never invoked")
	}
	cancel()
	<-sched.Done()
}

// --- helpers ---

func noopEmit() func(types.DiscoveryEvent) {
	return func(types.DiscoveryEvent) {}
}

type panickingJob struct{ name string }

func (j *panickingJob) Name() string            { return j.name }
func (j *panickingJob) Interval() time.Duration { return time.Millisecond }
func (j *panickingJob) Run(_ context.Context, _ func(types.DiscoveryEvent)) error {
	panic("intentional panic in test")
}

type countingJob struct {
	name     string
	runCount *atomic.Int32
	interval time.Duration
}

func (j *countingJob) Name() string            { return j.name }
func (j *countingJob) Interval() time.Duration { return j.interval }
func (j *countingJob) Run(_ context.Context, _ func(types.DiscoveryEvent)) error {
	j.runCount.Add(1)
	return nil
}

type emitJob struct {
	name     string
	interval time.Duration
}

func (j *emitJob) Name() string            { return j.name }
func (j *emitJob) Interval() time.Duration { return j.interval }
func (j *emitJob) Run(_ context.Context, emit func(types.DiscoveryEvent)) error {
	emit(types.DiscoveryEvent{Name: "evicted", SourceIP: "10.0.0.1"})
	return nil
}
