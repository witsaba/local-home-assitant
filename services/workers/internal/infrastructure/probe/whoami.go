// Package probe implements HTTP probes against witsaba device /whoami endpoints.
package probe

import (
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"strings"
	"time"

	"github.com/witsaba/local-home-assitant/services/workers/internal/types"
)

// ProbeWhoami issues a GET /whoami against target with the given timeout.
// Returns (event, true, nil) if the response carries the X-Witsaba-Device:
// true header (case-insensitive value) AND a parseable body.
// Returns (zero, false, nil) for a benign miss.
// Returns (_, false, err) for a transport error worth logging.
func ProbeWhoami(target net.IP, timeout time.Duration) (types.DiscoveryEvent, bool, error) {
	return ProbeWithClient(target, timeout, newClient(timeout))
}

// ProbeWithClient is like ProbeWhoami but uses a caller-supplied http.Client.
// This allows tests to inject a redirecting transport.
func ProbeWithClient(target net.IP, timeout time.Duration, client *http.Client) (types.DiscoveryEvent, bool, error) {
	url := fmt.Sprintf("http://%s/whoami", target.String())

	client.Timeout = timeout
	resp, err := client.Get(url)
	if err != nil {
		return types.DiscoveryEvent{}, false, fmt.Errorf("GET %s: %w", url, err)
	}
	defer func() { _ = resp.Body.Close() }()

	if !hasWitsabaHeader(resp.Header) {
		return types.DiscoveryEvent{}, false, nil
	}

	var ev types.DiscoveryEvent
	if err := json.NewDecoder(resp.Body).Decode(&ev); err != nil {
		return types.DiscoveryEvent{}, false, nil
	}

	ev.SourceIP = target.String()
	ev.DiscoveredAt = time.Now().UTC()
	return ev, true, nil
}

// newClient creates a probe-appropriate http.Client.
func newClient(timeout time.Duration) *http.Client {
	return &http.Client{
		Timeout: timeout,
		Transport: &http.Transport{
			DisableKeepAlives: true,
		},
	}
}

// hasWitsabaHeader returns true when X-Witsaba-Device is present and its
// value equals "true" (case-insensitive).
func hasWitsabaHeader(h http.Header) bool {
	for k, values := range h {
		if strings.EqualFold(k, "X-Witsaba-Device") {
			for _, v := range values {
				if strings.EqualFold(strings.TrimSpace(v), "true") {
					return true
				}
			}
		}
	}
	return false
}
