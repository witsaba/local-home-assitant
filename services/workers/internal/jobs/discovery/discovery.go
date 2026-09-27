package discovery

import (
	"context"
	"net"
	"sync"
	"time"

	"github.com/witsaba/local-home-assitant/services/workers/internal/infrastructure/probe"
	routetable "github.com/witsaba/local-home-assitant/services/workers/internal/infrastructure/routetable"
	"github.com/witsaba/local-home-assitant/services/workers/internal/types"
	"go.uber.org/zap"
)

// SubnetProvider abstracts routetable for testability.
type SubnetProvider interface {
	DiscoverAttachedSubnets() ([]routetable.Subnet, error)
}

// SubnetProviderFunc adapts a plain function to SubnetProvider.
type SubnetProviderFunc func() ([]routetable.Subnet, error)

func (f SubnetProviderFunc) DiscoverAttachedSubnets() ([]routetable.Subnet, error) {
	return f()
}

// Job implements worker.Job for periodic network discovery.
type Job struct {
	name           string
	interval       time.Duration
	poolSize       int
	probeTimeout   time.Duration
	subnetProvider SubnetProvider
	logger         *zap.Logger
	probeFn        func(net.IP, time.Duration) (types.DiscoveryEvent, bool, error)
}

// NewJob returns a configured discovery job using the real probe and the
// real routetable.
func NewJob(interval time.Duration, poolSize int, probeTimeout time.Duration, logger *zap.Logger) *Job {
	return &Job{
		name:         "discovery",
		interval:     interval,
		poolSize:     poolSize,
		probeTimeout: probeTimeout,
		subnetProvider: SubnetProviderFunc(func() ([]routetable.Subnet, error) {
			return routetable.DiscoverAttachedSubnets()
		}),
		logger:  logger,
		probeFn: probe.ProbeWhoami,
	}
}

// Name implements worker.Job.
func (j *Job) Name() string { return j.name }

// Interval implements worker.Job.
func (j *Job) Interval() time.Duration { return j.interval }

// SetSubnetProvider replaces the routetable provider. Test seam.
func (j *Job) SetSubnetProvider(p SubnetProvider) { j.subnetProvider = p }

// SetProbe overrides the probe function. Test seam.
func (j *Job) SetProbe(fn func(net.IP, time.Duration) (types.DiscoveryEvent, bool, error)) {
	j.probeFn = fn
}

// Run implements worker.Job. At each tick it enumerates attached subnets,
// expands each into host IPs, runs a bounded worker pool of probes, and
// pushes every matched DiscoveryEvent through emit.
func (j *Job) Run(ctx context.Context, emit func(types.DiscoveryEvent)) error {
	if err := ctx.Err(); err != nil {
		return err
	}

	subnets, err := j.subnetProvider.DiscoverAttachedSubnets()
	if err != nil {
		j.logger.Warn("failed to enumerate subnets", zap.Error(err))
		return nil
	}

	var targets []net.IP
	for _, sn := range subnets {
		targets = append(targets, expandCIDR(&sn.CIDR)...)
	}
	if len(targets) == 0 {
		j.logger.Info("scan complete",
			zap.Int("subnets", len(subnets)),
			zap.Int("targets", 0),
			zap.Int("devices_found", 0),
		)
		return nil
	}

	type result struct {
		event   types.DiscoveryEvent
		matched bool
	}
	results := make(chan result, len(targets))

	var wg sync.WaitGroup
	sem := make(chan struct{}, j.poolSize)

dispatch:
	for _, ip := range targets {
		select {
		case <-ctx.Done():
			break dispatch
		case sem <- struct{}{}:
		}
		ip := ip
		wg.Add(1)
		go func() {
			defer wg.Done()
			defer func() { <-sem }()

			ev, matched, err := j.probeFn(ip, j.probeTimeout)
			if err != nil {
				j.logger.Debug("probe failed",
					zap.String("ip", ip.String()),
					zap.Error(err),
				)
				return
			}
			if matched {
				select {
				case results <- result{event: ev, matched: true}:
				default:
				}
			}
		}()
	}
	wg.Wait()
	close(results)

	var count int
	for res := range results {
		if res.matched {
			emit(res.event)
			count++
		}
	}

	j.logger.Info("scan complete",
		zap.Int("subnets", len(subnets)),
		zap.Int("targets", len(targets)),
		zap.Int("devices_found", count),
	)
	return nil
}

// expandCIDR returns every usable host IP in the given IPv4 subnet.
// Skips the network and broadcast addresses. For subnets smaller than
// /30 the expansion is empty (no usable host addresses).
func expandCIDR(cidr *net.IPNet) []net.IP {
	if cidr.IP.To4() == nil {
		return nil
	}
	ones, bits := cidr.Mask.Size()
	if ones > 30 {
		return nil
	}

	network := cidr.IP.To4()
	broadcast := make(net.IP, 4)
	for i := range network {
		broadcast[i] = network[i] | ^cidr.Mask[i]
	}

	// Start at the first host (network + 1) and stop before broadcast.
	cursor := make(net.IP, 4)
	copy(cursor, network)
	increment(cursor)

	maxHosts := (1 << (bits - ones)) - 1
	var hosts []net.IP
	for !equalIP(cursor, broadcast) && len(hosts) < maxHosts {
		host := make(net.IP, 4)
		copy(host, cursor)
		hosts = append(hosts, host)
		increment(cursor)
	}
	return hosts
}

func increment(ip net.IP) {
	for i := len(ip) - 1; i >= 0; i-- {
		ip[i]++
		if ip[i] != 0 {
			return
		}
	}
}

func equalIP(a, b net.IP) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}
