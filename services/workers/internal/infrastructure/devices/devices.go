// Package devices persists witsaba device discovery events into a
// relational store. The package exposes a Repository interface and
// keeps the consumer (jobs/discovery) free of any concrete driver
// dependency, so tests can swap a noop implementation and so the
// choice of driver (pgx v5 today) can evolve without touching the
// jobs layer.
//
// The current Repository contract is intentionally minimal:
//   - Upsert persists the discovery event idempotently keyed by MAC,
//     refreshing last-seen / source-ip / metadata on conflict.
//   - Close releases any underlying resources (pool, etc.).
//
// The interface lives next to its concrete implementations so callers
// can pick a compile-time implementation or inject their own for tests.
package devices

import (
	"context"

	"github.com/witsaba/local-home-assitant/services/workers/internal/types"
)

// Repository persists DiscoveryEvent rows in a store keyed by MAC.
//
// Implementations MUST treat Upsert as idempotent: a second call for
// the same MAC must not create a duplicate row. Implementations MAY
// log, trace, or wrap the underlying error; they MUST surface the
// error to the caller unchanged otherwise.
//
// Close is called once on shutdown. Implementations with no resources
// (e.g. noop) may make it a no-op.
type Repository interface {
	// Upsert writes ev into the store. The MAC field is the primary key.
	// Returns the underlying error if the write fails.
	Upsert(ctx context.Context, ev types.DiscoveryEvent) error
	// Close releases any held resources. Safe to call multiple times.
	Close()
}
