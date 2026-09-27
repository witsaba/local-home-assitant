package worker_test

import (
	"context"
	"testing"
	"time"

	"github.com/witsaba/local-home-assitant/services/workers/internal/types"
	"github.com/witsaba/local-home-assitant/services/workers/internal/worker"
)

type minimalJob struct{}

func (j *minimalJob) Name() string            { return "minimal" }
func (j *minimalJob) Interval() time.Duration { return time.Second }
func (j *minimalJob) Run(ctx context.Context, emit func(types.DiscoveryEvent)) error {
	<-ctx.Done()
	return ctx.Err()
}

func TestJobInterface(t *testing.T) {
	t.Parallel()
	var j worker.Job = &minimalJob{}
	if j.Name() != "minimal" {
		t.Errorf("Name() = %q, want %q", j.Name(), "minimal")
	}
	if j.Interval() != time.Second {
		t.Errorf("Interval() = %v, want %v", j.Interval(), time.Second)
	}
}
