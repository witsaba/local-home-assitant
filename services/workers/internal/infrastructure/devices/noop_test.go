package devices

import (
	"context"
	"testing"
	"time"

	"go.uber.org/zap"

	"github.com/witsaba/local-home-assitant/services/workers/internal/types"
)

func TestNoopUpsertReturnsNilWithLogger(t *testing.T) {
	n := NewNoop(zap.NewNop())
	ev := types.DiscoveryEvent{
		DiscoveredAt: time.Now(),
		SourceIP:     "192.168.1.51",
		MAC:          "aa:bb:cc:dd:ee:ff",
		Name:         "kitchen-cam",
		FW:           "1.2.3",
		Chip:         "esp32-s3",
	}
	if err := n.Upsert(context.Background(), ev); err != nil {
		t.Fatalf("Upsert returned unexpected error: %v", err)
	}
}

func TestNoopUpsertReturnsNilWithoutLogger(t *testing.T) {
	n := NewNoop(nil)
	ev := types.DiscoveryEvent{
		DiscoveredAt: time.Now(),
		MAC:          "11:22:33:44:55:66",
	}
	if err := n.Upsert(context.Background(), ev); err != nil {
		t.Fatalf("Upsert returned unexpected error: %v", err)
	}
}

func TestNoopCloseIsIdempotent(t *testing.T) {
	n := NewNoop(zap.NewNop())
	n.Close()
	n.Close() // second call must not panic
}
