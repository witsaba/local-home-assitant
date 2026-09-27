//go:build darwin

package routetable

import (
	"bufio"
	"bytes"
	"errors"
	"net"
	"os/exec"
	"regexp"
	"strconv"
	"strings"
)

// ErrUnsupportedPlatform is returned by discoverAttachedSubnets on
// platforms that have no implementation.
var ErrUnsupportedPlatform = errors.New("routetable: platform not supported")

func discoverAttachedSubnets() ([]Subnet, error) {
	gateways, err := parseDarwinDefaultRoute()
	if err != nil {
		return nil, err
	}

	ifaces, err := net.Interfaces()
	if err != nil {
		return nil, err
	}

	var subnets []Subnet
	for _, iface := range ifaces {
		gw, ok := gateways[iface.Name]
		if !ok {
			continue
		}
		subnet, err := matchInterfaceToSubnet(iface, gw)
		if err != nil {
			continue
		}
		subnets = append(subnets, subnet)
	}

	return subnets, nil
}

// parseDarwinDefaultRoute runs `route -n get default` and returns a map
// of interface name -> gateway IP.
func parseDarwinDefaultRoute() (map[string]net.IP, error) {
	out, err := exec.Command("route", "-n", "get", "default").Output()
	if err != nil {
		return nil, err
	}
	return ParseRouteOutput(out), nil
}

// ParseRouteOutput extracts interface name and gateway IP from the
// output of `route -n get default`. Exposed for testing.
//
// Order-independent: on real macOS the gateway: line appears BEFORE
// the interface: line, so we track each field as we see it and commit
// a (iface, gw) pair at end-of-input (or whenever both are present).
// macOS has exactly one default route so this is sufficient.
func ParseRouteOutput(data []byte) map[string]net.IP {
	result := make(map[string]net.IP)
	var iface, gwStr string
	scanner := bufio.NewScanner(bytes.NewReader(data))
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		switch {
		case strings.HasPrefix(line, "interface:"):
			iface = strings.TrimSpace(strings.TrimPrefix(line, "interface:"))
		case strings.HasPrefix(line, "gateway:"):
			gwStr = strings.TrimSpace(strings.TrimPrefix(line, "gateway:"))
		}
	}
	if iface != "" && gwStr != "" {
		if gw := net.ParseIP(gwStr); gw != nil {
			result[iface] = gw
		}
	}
	return result
}

// matchInterfaceToSubnet returns the subnet for the given interface
// whose address is in the same subnet as gateway. IPv4 only.
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
		if ipNet.Contains(gateway) {
			return Subnet{CIDR: *ipNet, Gateway: gateway}, nil
		}
	}
	return Subnet{}, errors.New("no matching IPv4 address")
}

var defaultRouteRE = regexp.MustCompile(`^default\s+(\S+)\s+\S+\s+\S+\s+\S+\s+(\S+)$`)

// ParseDarwinRouteLine parses a netstat -rn default route line.
func ParseDarwinRouteLine(line string) (gateway, iface string, ok bool) {
	m := defaultRouteRE.FindStringSubmatch(strings.TrimSpace(line))
	if m == nil {
		return "", "", false
	}
	return m[1], m[2], true
}

// ParseNetstatOutput parses `netstat -rn` output for testing.
func ParseNetstatOutput(data []byte) map[string]net.IP {
	result := make(map[string]net.IP)
	scanner := bufio.NewScanner(bytes.NewReader(data))
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		gw, iface, ok := ParseDarwinRouteLine(line)
		if !ok {
			continue
		}
		gwIP := net.ParseIP(gw)
		if gwIP == nil {
			continue
		}
		result[iface] = gwIP
	}
	return result
}

var _ = strconv.Atoi // used implicitly via regexp package internals; suppress unused warning
