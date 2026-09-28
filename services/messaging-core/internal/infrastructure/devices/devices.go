// Package devices provides the device repository for querying witsaba.devices.
// It mirrors the workers service repository but is read-only (SELECT only).
package devices

import (
	"context"
	"errors"
	"net"
)

// NotFoundError is returned when a device is not found in the database.
var NotFoundError = errors.New("device not found")

// Device represents a row from witsaba.devices.
type Device struct {
	// MAC is the device's unique identifier (primary key).
	MAC string
	// Name is the human-readable device name (may be empty).
	Name string
	// FW is the firmware version (may be empty).
	FW string
	// Chip is the silicon identifier (may be empty).
	Chip string
	// LastSourceIP is the last observed IP address of the device.
	LastSourceIP net.IP
}

// DeviceRepository queries the witsaba.devices table.
type DeviceRepository interface {
	// GetByMAC returns a device by its MAC address.
	// Returns NotFoundError if no device matches.
	GetByMAC(ctx context.Context, mac string) (*Device, error)
}
