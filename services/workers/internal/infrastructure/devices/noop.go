package devices

import (
	"context"

	"go.uber.org/zap"

	"github.com/witsaba/local-home-assitant/services/workers/internal/types"
)

// Noop is a Repository that drops every Upsert after logging it.
// Useful for unit tests, for a dev-mode run without Postgres, and as a
// reference implementation that always succeeds.
//
// Noop is safe for concurrent use; it holds no state.
type Noop struct {
	logger *zap.Logger
}

// NewNoop returns a Noop that logs every event at DEBUG with the
// "would-upsert" message. Pass nil to disable logging.
func NewNoop(logger *zap.Logger) *Noop {
	return &Noop{logger: logger}
}

// Upsert logs the event at DEBUG and returns nil. If the receiver's
// logger is nil the call is a complete no-op.
func (n *Noop) Upsert(_ context.Context, ev types.DiscoveryEvent) error {
	if n.logger == nil {
		return nil
	}
	n.logger.Debug("would-upsert device",
		zap.String("mac", ev.MAC),
		zap.String("source_ip", ev.SourceIP),
		zap.String("name", ev.Name),
		zap.String("fw", ev.FW),
		zap.String("chip", ev.Chip),
		zap.Time("discovered_at", ev.DiscoveredAt),
	)
	return nil
}

// Close is a no-op for Noop.
func (n *Noop) Close() {}
