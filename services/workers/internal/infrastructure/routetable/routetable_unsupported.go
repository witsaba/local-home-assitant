//go:build !linux && !darwin

package routetable

import "errors"

// ErrUnsupportedPlatform is returned on platforms where subnet discovery
// is not yet implemented.
var ErrUnsupportedPlatform = errors.New("routetable: platform not supported")

func discoverAttachedSubnets() ([]Subnet, error) {
	return nil, ErrUnsupportedPlatform
}
