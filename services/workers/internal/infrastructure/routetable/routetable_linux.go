//go:build linux

package routetable

import (
	"bufio"
	"bytes"
	"errors"
	"net"
	"os"
	"strconv"
	"strings"
)

// ErrUnsupportedPlatform is returned by discoverAttachedSubnets on
// platforms that have no implementation.
var ErrUnsupportedPlatform = errors.New("routetable: platform not supported")

func discoverAttachedSubnets() ([]Subnet, error) {
	routes, err := parseProcNetRoute()
	if err != nil {
		return nil, err
	}

	ifaces, err := net.Interfaces()
	if err != nil {
		return nil, err
	}

	ifaceMap := make(map[string]net.Interface)
	for _, iface := range ifaces {
		ifaceMap[iface.Name] = iface
	}

	var subnets []Subnet
	for _, route := range routes {
		iface, ok := ifaceMap[route.Interface]
		if !ok {
			continue
		}
		// Match gateway to interface addresses (IPv4 only).
		subnet, err := matchInterfaceToSubnet(iface, route.Gateway)
		if err != nil {
			continue // skip non-matching or IPv6
		}
		subnets = append(subnets, subnet)
	}

	return subnets, nil
}

// routeEntry holds a parsed /proc/net/route line.
type routeEntry struct {
	Interface string
	Gateway   net.IP
}

// parseProcNetRoute reads /proc/net/route and returns gateway IPs per
// interface. Only lines with destination 0.0.0.0 (default route) are
// relevant for finding attached subnets.
func parseProcNetRoute() ([]routeEntry, error) {
	data, err := os.ReadFile("/proc/net/route")
	if err != nil {
		return nil, err
	}
	return ParseRouteTable(data)
}

// ParseRouteTable is exposed for testing with synthetic input.
func ParseRouteTable(data []byte) ([]routeEntry, error) {
	var routes []routeEntry
	scanner := bufio.NewScanner(bytes.NewReader(data))
	// Skip header line.
	if !scanner.Scan() {
		return nil, nil
	}
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" {
			continue
		}
		fields := strings.Fields(line)
		if len(fields) < 3 {
			continue
		}
		iface := fields[0]
		destHex := fields[1]
		gwHex := fields[2]

		// We want the default route (destination = 0.0.0.0 / destHex == "0").
		if destHex != "00000000" && destHex != "0" {
			continue
		}
		gw := hexToIP(gwHex)
		if gw == nil {
			continue
		}
		routes = append(routes, routeEntry{
			Interface: iface,
			Gateway:   gw,
		})
	}
	return routes, scanner.Err()
}

// hexToIP converts a little-endian hex string (e.g. "0100007F") to net.IP.
func hexToIP(hexStr string) net.IP {
	v, err := strconvParseUintHex(hexStr)
	if err != nil {
		return nil
	}
	ip := make(net.IP, 4)
	ip[0] = byte(v & 0xff)
	ip[1] = byte((v >> 8) & 0xff)
	ip[2] = byte((v >> 16) & 0xff)
	ip[3] = byte((v >> 24) & 0xff)
	return ip
}

func strconvParseUintHex(s string) (uint64, error) {
	return strconv.ParseUint(s, 16, 32)
}

// matchInterfaceToSubnet returns the subnet for the given interface
// whose address is in the same /24 (or whatever) as gateway. IPv4 only.
func matchInterfaceToSubnet(iface net.Interface, gateway net.IP) (Subnet, error) {
	addrs, err := iface.Addrs()
	if err != nil {
		return Subnet{}, err
	}
	for _, addr := range addrs {
		ipNet, ok := addr.(*net.IPNet)
		if !ok {
			continue
		}
		ip := ipNet.IP
		if ip4 := ip.To4(); ip4 == nil {
			// IPv6 — skip in v1.
			continue
		}
		// Check if gateway is in the same subnet as this interface address.
		if ipNet.Contains(gateway) {
			return Subnet{CIDR: *ipNet, Gateway: gateway}, nil
		}
	}
	return Subnet{}, errors.New("no matching IPv4 address")
}
