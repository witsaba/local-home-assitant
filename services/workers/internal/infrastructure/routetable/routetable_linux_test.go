package routetable_test

import (
	"net"
	"testing"

	"github.com/witsaba/local-home-assitant/services/workers/internal/infrastructure/routetable"
)

func TestParseRouteTable_SingleV4DefaultRoute(t *testing.T) {
	t.Parallel()
	// /proc/net/route on Linux. Gateway "0100007F" = 127.0.0.1 in little-endian hex.
	input := `Iface   Destination     Gateway     Flags   RefCnt  Use     Metric  Mask            MTU     Window  IRTT
eth0    00000000        0100007F    0003    0       0       1024    00000000        0       0       0
`
	routes, err := routetable.ParseRouteTable([]byte(input))
	if err != nil {
		t.Fatalf("ParseRouteTable returned error: %v", err)
	}
	if len(routes) != 1 {
		t.Fatalf("expected 1 route, got %d", len(routes))
	}
	if routes[0].Interface != "eth0" {
		t.Errorf("expected interface eth0, got %q", routes[0].Interface)
	}
	expectedGW := net.ParseIP("127.0.0.1")
	if !routes[0].Gateway.Equal(expectedGW) {
		t.Errorf("expected gateway 127.0.0.1, got %v", routes[0].Gateway)
	}
}

func TestParseRouteTable_MultiRoute(t *testing.T) {
	t.Parallel()
	// Two default routes — both should be returned.
	input := `Iface   Destination     Gateway     Flags   RefCnt  Use     Metric  Mask            MTU     Window  IRTT
eth0    00000000        0100007F    0003    0       0       1024    00000000        0       0       0
wlan0   00000000        C0A80101    0003    0       0       600     00000000        0       0       0
`
	routes, err := routetable.ParseRouteTable([]byte(input))
	if err != nil {
		t.Fatalf("ParseRouteTable returned error: %v", err)
	}
	if len(routes) != 2 {
		t.Fatalf("expected 2 routes, got %d", len(routes))
	}
}

func TestParseRouteTable_Malformed(t *testing.T) {
	t.Parallel()
	input := `Iface   Destination     Gateway     Flags
eth0    NOT_A_HEX        0100007F    0003
wlan0   00000000        GARBAGE     0003
`
	routes, err := routetable.ParseRouteTable([]byte(input))
	if err != nil {
		t.Fatalf("ParseRouteTable returned error: %v", err)
	}
	// Both lines have invalid gateway hex — neither should appear.
	if len(routes) != 0 {
		t.Errorf("expected 0 routes for malformed input, got %d", len(routes))
	}
}

func TestParseRouteTable_NonDefaultRoute(t *testing.T) {
	t.Parallel()
	// Non-default route (destination != 0) should be skipped.
	input := `Iface   Destination     Gateway     Flags   RefCnt  Use     Metric  Mask            MTU     Window  IRTT
eth0    C0A80100        0100007F    0003    0       0       1024    FFFFFF00        0       0       0
`
	routes, err := routetable.ParseRouteTable([]byte(input))
	if err != nil {
		t.Fatalf("ParseRouteTable returned error: %v", err)
	}
	if len(routes) != 0 {
		t.Errorf("expected 0 routes for non-default destination, got %d", len(routes))
	}
}
