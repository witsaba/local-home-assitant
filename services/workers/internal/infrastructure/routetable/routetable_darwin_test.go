package routetable_test

import (
	"net"
	"testing"

	"github.com/witsaba/local-home-assitant/services/workers/internal/infrastructure/routetable"
)

func TestParseRouteOutput_SingleInterface(t *testing.T) {
	t.Parallel()
	// Synthetic `route -n get default` output for Darwin.
	input := `   route to: default
destination: default
       mask: default
  interface: en0
  gateway: 192.168.1.1
`
	result := routetable.ParseRouteOutput([]byte(input))
	if len(result) != 1 {
		t.Fatalf("expected 1 entry, got %d", len(result))
	}
	gw, ok := result["en0"]
	if !ok {
		t.Fatal("expected gateway for en0")
	}
	expected := net.ParseIP("192.168.1.1")
	if !gw.Equal(expected) {
		t.Errorf("expected gateway 192.168.1.1, got %v", gw)
	}
}

func TestParseRouteOutput_Empty(t *testing.T) {
	t.Parallel()
	result := routetable.ParseRouteOutput([]byte(""))
	if len(result) != 0 {
		t.Errorf("expected 0 entries for empty input, got %d", len(result))
	}
}

func TestParseRouteOutput_Malformed(t *testing.T) {
	t.Parallel()
	// No valid gateway lines.
	input := `   route to: default
gateway: not.an.ip.address
`
	result := routetable.ParseRouteOutput([]byte(input))
	if len(result) != 0 {
		t.Errorf("expected 0 entries for malformed input, got %d", len(result))
	}
}

func TestParseDarwinRouteLine(t *testing.T) {
	t.Parallel()
	// Also test the internal parser with synthetic netstat -rn output.
	input := `Internet:
Destination        Gateway            Flags         Refs      Use   Netif Expire
default            192.168.1.1        UGSc            1        0     en0
`
	result := routetable.ParseNetstatOutput([]byte(input))
	if len(result) != 1 {
		t.Fatalf("expected 1 entry, got %d", len(result))
	}
	gw, ok := result["en0"]
	if !ok {
		t.Fatal("expected gateway for en0 from netstat output")
	}
	expected := net.ParseIP("192.168.1.1")
	if !gw.Equal(expected) {
		t.Errorf("expected gateway 192.168.1.1, got %v", gw)
	}
}
