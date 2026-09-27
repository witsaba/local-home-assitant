// Package worker provides the Job interface and the Scheduler that
// drives periodic background jobs.
package worker

import (
	"context"
	"time"

	"github.com/witsaba/local-home-assitant/services/workers/internal/types"
)

// Job is implemented by every plug-in worker the host will schedule.
// Run is invoked once per tick. The host owns the lifecycle and the
// shared event channel so multiple jobs can co-exist without touching
// each other.
type Job interface {
	// Name returns a stable identifier for this job.
	Name() string
	// Interval returns the minimum duration between two invocations of Run.
	Interval() time.Duration
	// Run is called once per tick. It receives the job-wide context and
	// an emit function to push DiscoveryEvents into the shared channel.
	// Run must return promptly when ctx is cancelled.
	Run(ctx context.Context, emit func(types.DiscoveryEvent)) error
}
