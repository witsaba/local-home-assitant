package discovery

import (
	"context"

	"github.com/witsaba/local-home-assitant/services/workers/internal/types"
	"go.uber.org/zap"
)

// Consumer drains the events channel and logs each event at INFO.
type Consumer struct {
	events <-chan types.DiscoveryEvent
	logger *zap.Logger
}

// NewConsumer returns a Consumer that reads from events and logs each
// at INFO via zap.
func NewConsumer(events <-chan types.DiscoveryEvent, logger *zap.Logger) *Consumer {
	return &Consumer{events: events, logger: logger}
}

// Start runs the consumer loop until ctx is cancelled OR the events
// channel is closed. It is the caller's responsibility to close the
// events channel once no more writes can happen (i.e. after the
// scheduler has fully drained).
func (c *Consumer) Start(ctx context.Context) {
	for {
		select {
		case <-ctx.Done():
			return
		case ev, ok := <-c.events:
			if !ok {
				return
			}
			c.log(ev)
		}
	}
}

func (c *Consumer) log(ev types.DiscoveryEvent) {
	c.logger.Info("witsaba device found",
		zap.String("source_ip", ev.SourceIP),
		zap.String("name", ev.Name),
		zap.String("mac", ev.MAC),
		zap.String("fw", ev.FW),
		zap.String("chip", ev.Chip),
		zap.Time("discovered_at", ev.DiscoveredAt),
	)
}
