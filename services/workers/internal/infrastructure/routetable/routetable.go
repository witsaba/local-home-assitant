// Package routetable enumerates directly-attached IPv4 subnets by
// consulting the host's routing table. Implementation is OS-specific
// via build tags.
package routetable

import (
	"net"
)

// Subnet is a directly-attached IPv4 network plus the gateway IP the
// host would use to reach it.
type Subnet struct {
	CIDR    net.IPNet
	Gateway net.IP
}

// DiscoverAttachedSubnets returns every directly-attached IPv4 subnet
// on this host. The implementation is build-tagged per OS.
func DiscoverAttachedSubnets() ([]Subnet, error) {
	return discoverAttachedSubnets()
}
