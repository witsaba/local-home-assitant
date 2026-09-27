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
// channel is closed. When ctx is cancelled, any events still buffered
// in the channel are drained (non-blocking) and logged so we don't
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
					c.log(ev)
				default:
					return
				}
			}
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
