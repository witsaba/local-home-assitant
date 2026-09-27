// Package types holds cross-package domain types shared by the workers service.
package types

import "time"

// DiscoveryEvent is emitted when a witsaba device is successfully probed.
// SourceIP is stored as string because net.IP does not JSON-round-trip cleanly.
type DiscoveryEvent struct {
	DiscoveredAt time.Time `json:"discovered_at"`
	SourceIP     string    `json:"source_ip"`
	MAC          string    `json:"mac"`
	Name         string    `json:"name"`
	FW           string    `json:"fw"`
	Chip         string    `json:"chip"`
}
