package discovery_test

import (
	"context"
	"encoding/json"
	"net"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	routetable "github.com/witsaba/local-home-assitant/services/workers/internal/infrastructure/routetable"
	"github.com/witsaba/local-home-assitant/services/workers/internal/jobs/discovery"
	"github.com/witsaba/local-home-assitant/services/workers/internal/types"
	"go.uber.org/zap"
)

func TestDiscovery_HitEvents(t *testing.T) {
	t.Parallel()

	var hitCount atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		hitCount.Add(1)
		w.Header().Set("X-Witsaba-Device", "true")
		_, _ = w.Write([]byte(`{"name":"cam-01","mac":"aa:bb:cc:dd:ee:ff","fw":"1.2.3","chip":"esp32"}`))
	}))
	defer srv.Close()

	host := mustParseIP(t, srv.Listener.Addr().String())
	ip := host.To4()
	network := net.IPNet{
		IP:   net.IP{ip[0], ip[1], ip[2], ip[3] & 0xFC},
		Mask: net.CIDRMask(30, 32),
	}

	fakeProvider := &fakeSubnetProvider{subnets: []routetable.Subnet{
		{CIDR: network, Gateway: ip},
	}}

	job := discovery.NewJob(10*time.Hour, 4, 5*time.Second, zap.NewNop())
	job.SetSubnetProvider(fakeProvider)
	job.SetProbe(func(target net.IP, timeout time.Duration) (types.DiscoveryEvent, bool, error) {
		client := &http.Client{Timeout: timeout}
		resp, err := client.Get(srv.URL + "/whoami")
		if err != nil {
			return types.DiscoveryEvent{}, false, err
		}
		defer func() { _ = resp.Body.Close() }()
		if resp.Header.Get("X-Witsaba-Device") != "true" {
			return types.DiscoveryEvent{}, false, nil
		}
		var ev types.DiscoveryEvent
		if err := json.NewDecoder(resp.Body).Decode(&ev); err != nil {
			return types.DiscoveryEvent{}, false, nil
		}
		ev.SourceIP = target.String()
		ev.DiscoveredAt = time.Now().UTC()
		return ev, true, nil
	})

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	var emitted []types.DiscoveryEvent
	if err := job.Run(ctx, func(ev types.DiscoveryEvent) {
		emitted = append(emitted, ev)
	}); err != nil && err != context.Canceled {
		t.Fatalf("Run returned error: %v", err)
	}

	// /30 has 2 usable hosts (network and broadcast skipped).
	if len(emitted) != 2 {
		t.Errorf("expected 2 events (2 usable hosts), got %d", len(emitted))
	}
	for _, ev := range emitted {
		if ev.Name != "cam-01" {
			t.Errorf("expected Name=cam-01, got %q", ev.Name)
		}
		if ev.SourceIP == "" {
			t.Error("expected SourceIP to be set")
		}
	}
}

func TestDiscovery_MissPlainHost(t *testing.T) {
	t.Parallel()

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	host := mustParseIP(t, srv.Listener.Addr().String())
	ip := host.To4()
	network := net.IPNet{
		IP:   net.IP{ip[0], ip[1], ip[2], ip[3] & 0xFC},
		Mask: net.CIDRMask(30, 32),
	}

	fakeProvider := &fakeSubnetProvider{subnets: []routetable.Subnet{
		{CIDR: network, Gateway: ip},
	}}

	job := discovery.NewJob(10*time.Hour, 4, 5*time.Second, zap.NewNop())
	job.SetSubnetProvider(fakeProvider)
	job.SetProbe(func(net.IP, time.Duration) (types.DiscoveryEvent, bool, error) {
		return types.DiscoveryEvent{}, false, nil
	})

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	var emitted []types.DiscoveryEvent
	_ = job.Run(ctx, func(ev types.DiscoveryEvent) { emitted = append(emitted, ev) })

	if len(emitted) != 0 {
		t.Errorf("expected 0 events for plain host, got %d", len(emitted))
	}
}

func TestDiscovery_CtxCancelled(t *testing.T) {
	t.Parallel()
	fakeProvider := &fakeSubnetProvider{subnets: nil}

	job := discovery.NewJob(10*time.Hour, 4, 5*time.Second, zap.NewNop())
	job.SetSubnetProvider(fakeProvider)

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	err := job.Run(ctx, func(types.DiscoveryEvent) {})
	if err != context.Canceled {
		t.Errorf("expected context.Canceled, got %v", err)
	}
}

func TestDiscovery_NoTargets(t *testing.T) {
	t.Parallel()
	fakeProvider := &fakeSubnetProvider{subnets: []routetable.Subnet{
		{CIDR: net.IPNet{IP: net.IPv4(10, 0, 0, 0), Mask: net.CIDRMask(31, 32)}, Gateway: net.IPv4(10, 0, 0, 1)},
		// /31 has no usable hosts; expandCIDR returns empty.
	}}

	job := discovery.NewJob(10*time.Hour, 4, 5*time.Second, zap.NewNop())
	job.SetSubnetProvider(fakeProvider)

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	var emitted []types.DiscoveryEvent
	err := job.Run(ctx, func(ev types.DiscoveryEvent) { emitted = append(emitted, ev) })
	if err != nil && err != context.Canceled {
		t.Fatalf("Run error: %v", err)
	}
	if len(emitted) != 0 {
		t.Errorf("expected 0 events, got %d", len(emitted))
	}
}

// TestDiscovery_ExpandCIDR_NormalizesHighInterfaceIP is the regression
// test for the bug caught on the operator's Mac. When the interface IP
// sits high in the subnet (e.g. 192.168.1.244/24), expandCIDR used to
// start from the interface IP and miss every host below it. Old code
// produced only 10 targets (.245-.254); the new code masks the IP
// against the mask and produces 254 targets (.1-.254).
func TestDiscovery_ExpandCIDR_NormalizesHighInterfaceIP(t *testing.T) {
	t.Parallel()
	network := net.IPNet{
		IP:   net.IPv4(192, 168, 1, 244),
		Mask: net.CIDRMask(24, 32),
	}
	fakeProvider := &fakeSubnetProvider{
		subnets: []routetable.Subnet{{CIDR: network, Gateway: net.IPv4(192, 168, 1, 1)}},
	}

	var probes atomic.Int32
	job := discovery.NewJob(10*time.Hour, 64, 5*time.Second, zap.NewNop())
	job.SetSubnetProvider(fakeProvider)
	job.SetProbe(func(ip net.IP, _ time.Duration) (types.DiscoveryEvent, bool, error) {
		probes.Add(1)
		return types.DiscoveryEvent{}, false, nil
	})

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	var emitted []types.DiscoveryEvent
	_ = job.Run(ctx, func(ev types.DiscoveryEvent) { emitted = append(emitted, ev) })

	if got := probes.Load(); got != 254 {
		t.Errorf("expected 254 probes (all usable /24 hosts: .1-.254), got %d", got)
	}
	if len(emitted) != 0 {
		t.Errorf("expected 0 events (all probes are misses), got %d", len(emitted))
	}
}

// --- helpers ---

type fakeSubnetProvider struct {
	subnets []routetable.Subnet
	err     error
}

func (f *fakeSubnetProvider) DiscoverAttachedSubnets() ([]routetable.Subnet, error) {
	return f.subnets, f.err
}

func mustParseIP(t *testing.T, addr string) net.IP {
	t.Helper()
	host, _, err := net.SplitHostPort(addr)
	if err != nil {
		t.Fatalf("SplitHostPort(%q): %v", addr, err)
	}
	ip := net.ParseIP(host)
	if ip == nil {
		t.Fatalf("ParseIP(%q) returned nil", host)
	}
	return ip
}
