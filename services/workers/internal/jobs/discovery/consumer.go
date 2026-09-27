package discovery

import (
	"context"

	"go.uber.org/zap"

	"github.com/witsaba/local-home-assitant/services/workers/internal/infrastructure/devices"
	"github.com/witsaba/local-home-assitant/services/workers/internal/types"
)

// Consumer drains the events channel and persists each event into the
// devices repository before logging it. The repository call is best
// effort: a failed Upsert is logged at WARN but does not stop the
// consumer loop. The classic 'log every device' line is preserved for
// operator visibility, with an extra upsert_ok field that signals
// whether the row landed in the database.
type Consumer struct {
	events <-chan types.DiscoveryEvent
	repo   devices.Repository
	logger *zap.Logger
}

// NewConsumer returns a Consumer that reads from events, persists each
// event into repo, and logs each at INFO via zap.
//
// repo must be non-nil; pass devices.NewNoop(logger) if persistence is
// not desired (dev/test runs without Postgres).
func NewConsumer(events <-chan types.DiscoveryEvent, repo devices.Repository, logger *zap.Logger) *Consumer {
	return &Consumer{events: events, repo: repo, logger: logger}
}

// Start runs the consumer loop until ctx is cancelled OR the events
// channel is closed. When ctx is cancelled, any events still buffered
// in the channel are drained (non-blocking) and processed so we don't
// lose hits that arrived in the same scan tick as the shutdown signal.
func (c *Consumer) Start(ctx context.Context) {
	for {
		select {
		case <-ctx.Done():
			for {
				select {
				case ev, ok := <-c.events:
					if !ok {
						return
					}
					c.process(ctx, ev)
				default:
					return
				}
			}
		case ev, ok := <-c.events:
			if !ok {
				return
			}
			c.process(ctx, ev)
		}
	}
}

// process runs the persistence path and the operator log line for one
// event. The Upsert error is captured into the log line (upsert_ok /
// upsert_error) and never returned; the consumer is resilient to a
// misbehaving database.
func (c *Consumer) process(ctx context.Context, ev types.DiscoveryEvent) {
	upsertErr := c.repo.Upsert(ctx, ev)
	fields := []zap.Field{
		zap.String("source_ip", ev.SourceIP),
		zap.String("name", ev.Name),
		zap.String("mac", ev.MAC),
		zap.String("fw", ev.FW),
		zap.String("chip", ev.Chip),
		zap.Time("discovered_at", ev.DiscoveredAt),
	}
	if upsertErr != nil {
		fields = append(fields,
			zap.Bool("upsert_ok", false),
			zap.Error(upsertErr),
		)
		c.logger.Warn("witsaba device upsert failed", fields...)
		return
	}
	fields = append(fields, zap.Bool("upsert_ok", true))
	c.logger.Info("witsaba device found", fields...)
}
