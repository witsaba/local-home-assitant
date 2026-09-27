// Package discovery provides the discovery background job that probes
// witsaba devices on the local network.
package discovery

import "github.com/witsaba/local-home-assitant/services/workers/internal/types"

// DiscoveryEvent is re-exported from internal/types so job consumers don't
// need an extra import. Use types.DiscoveryEvent as the canonical type.
type DiscoveryEvent = types.DiscoveryEvent
