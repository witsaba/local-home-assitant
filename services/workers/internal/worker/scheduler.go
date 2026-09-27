// Package worker provides the Job interface (in job.go) and the
// Scheduler that drives periodic background jobs.
package worker

import (
	"context"
	"sync"
	"time"

	"github.com/witsaba/local-home-assitant/services/workers/internal/types"
	"go.uber.org/zap"
)

// Scheduler runs registered jobs on their own tickers, recovers
// panics inside Run, and stops cleanly when the parent context is
// cancelled.
type Scheduler struct {
	jobs   []Job
	emit   func(types.DiscoveryEvent)
	logger *zap.Logger

	done    chan struct{}
	stopMu  sync.Mutex
	stop    context.CancelFunc
}

// New returns a Scheduler that will run the supplied jobs. The same emit
// closure is forwarded to every job's Run call.
func New(jobs []Job, emit func(types.DiscoveryEvent), logger *zap.Logger) *Scheduler {
	return &Scheduler{
		jobs:   jobs,
		emit:   emit,
		logger: logger,
		done:   make(chan struct{}),
	}
}

// Run starts every job's goroutine and blocks until they have all
// exited (typically because ctx was cancelled).
func (s *Scheduler) Run(ctx context.Context) {
	runCtx, cancel := context.WithCancel(ctx)

	s.stopMu.Lock()
	s.stop = cancel
	s.stopMu.Unlock()

	var wg sync.WaitGroup
	for _, job := range s.jobs {
		j := job
		wg.Add(1)
		go s.runJob(runCtx, &wg, j)
	}

	s.logger.Info("scheduler running", zap.Int("job_count", len(s.jobs)))
	wg.Wait()
	close(s.done)
	s.logger.Info("scheduler stopped")
}

// Stop signals all job goroutines to exit. Idempotent. Safe before and
// after Run.
func (s *Scheduler) Stop() {
	s.stopMu.Lock()
	defer s.stopMu.Unlock()
	if s.stop != nil {
		s.stop()
	}
}

// Done returns a channel that is closed when Run has fully exited.
func (s *Scheduler) Done() <-chan struct{} { return s.done }

// runJob drives one job's ticker. The blocking ticker.C read inside the
// loop only fires after Run() of the previous tick has returned, which
// guarantees no overlapping executions of the same job.
func (s *Scheduler) runJob(ctx context.Context, wg *sync.WaitGroup, job Job) {
	defer wg.Done()

	ticker := time.NewTicker(job.Interval())
	defer ticker.Stop()

	s.logger.Info("job started", zap.String("job", job.Name()))

	for {
		select {
		case <-ctx.Done():
			s.logger.Info("job stopped", zap.String("job", job.Name()))
			return
		case <-ticker.C:
			s.tick(ctx, job)
		}
	}
}

// tick calls job.Run wrapped in panic recovery. The per-tick WaitGroup
// blocks the next tick until the current tick's goroutine has exited,
// so even a panicking job cannot overlap itself.
func (s *Scheduler) tick(ctx context.Context, job Job) {
	var tickWg sync.WaitGroup
	tickWg.Add(1)

	func() {
		defer func() {
			if r := recover(); r != nil {
				s.logger.Error("job panicked",
					zap.String("job", job.Name()),
					zap.Any("panic", r),
				)
			}
			tickWg.Done()
		}()
		if err := job.Run(ctx, s.emit); err != nil && ctx.Err() == nil {
			s.logger.Warn("job returned error",
				zap.String("job", job.Name()),
				zap.Error(err),
			)
		}
	}()

	tickWg.Wait()
}
