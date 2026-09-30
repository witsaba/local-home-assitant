// capture.go — HTTP client and per-camera capture primitive for the
// surveillance job.
//
// One captureOne() call corresponds to one HTTP GET against one
// device. The worker calls it sequentially per tick. The HTTP
// client is built once at Job construction and reused across all
// cameras in a single tick (and across ticks until the worker
// shuts down). DisableKeepAlives=true mirrors the existing
// probe/whoami.go pattern; the embedded camera HTTP server does
// not benefit from connection reuse and stale keep-alive sockets
// can wedge on transient WiFi blips.
package surveillance

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"time"
)

// newHTTPClient returns the production HTTP client used to fetch
// /capture frames from each camera. Mirrors probe/whoami.go:
//
//   - Timeout is the per-request timeout (covers DNS + dial +
//     TLS + send + read; 10 s default is comfortable for an
//     800x600 JPEG over LAN).
//   - DisableKeepAlives=true: no socket reuse. Embedded httpd
//     implementations have a per-connection state machine that
//     can leak resources across keep-alive windows; resetting
//     the socket per request costs ~1 RTT but is safer for
//     long-running workers.
//   - ForceAttemptHTTP2=false: http2 on a plain LAN against a
//     tiny embedded httpd adds noise (ALPN negotiation, stream
//     frames) without any benefit.
//   - ExpectContinueTimeout=1s: defensive; a slow camera could
//     otherwise stall on the request write.
//
// The returned client is safe for concurrent use. The worker
// uses it sequentially so concurrency is not exercised in
// production, but a future fan-out across cameras should not
// have to re-derive the client.
func newHTTPClient(timeout time.Duration) *http.Client {
	if timeout <= 0 {
		timeout = 10 * time.Second
	}
	return &http.Client{
		Timeout: timeout,
		Transport: &http.Transport{
			Proxy:                 http.ProxyFromEnvironment,
			MaxIdleConns:          0, // 0 == Go default of 100, but with DisableKeepAlives these are unused
			MaxIdleConnsPerHost:   0,
			IdleConnTimeout:       30 * time.Second,
			TLSHandshakeTimeout:   5 * time.Second,
			ExpectContinueTimeout: 1 * time.Second,
			DisableKeepAlives:     true,
			DisableCompression:    true,
			ForceAttemptHTTP2:     false,
			DialContext: (&net.Dialer{
				Timeout:   5 * time.Second,
				KeepAlive: 0,
			}).DialContext,
		},
	}
}

// shouldFlash reports whether the worker should request a flash
// capture from the camera at the given moment. Thin wrapper over
// Window.Contains(now) — kept as a free function so tests can
// exercise the policy without instantiating a Job.
func shouldFlash(now time.Time, w Window) bool {
	return w.Contains(now)
}

// captureOne performs a single GET /capture against the device
// identified by req. The HTTP client is supplied so tests can
// inject a transport backed by httptest.Server. The context is
// honored — if ctx is cancelled mid-request, captureOne returns
// promptly with a wrapped error.
//
// On a clean HTTP 200, the JPEG body is returned (caller takes
// ownership and writes it to disk). On any other outcome, body
// is nil and err is non-nil; the caller logs and continues.
//
// The 5xx responses are NOT retried — the operator's plan is
// "every 15 minutes, accept whatever you get, move on". A
// failing camera produces 503 (busy) or 500 (sensor error) and
// those are surfaced as a single CapturedResult with the relevant
// error.
func captureOne(ctx context.Context, client *http.Client, req CaptureRequest) CaptureResult {
	if client == nil {
		return CaptureResult{
			MAC:      req.MAC,
			SourceIP: req.SourceIP,
			Flash:    req.Flash,
			Err:      errors.New("captureOne: nil http client"),
		}
	}
	if req.SourceIP == "" {
		return CaptureResult{
			MAC:      req.MAC,
			SourceIP: req.SourceIP,
			Flash:    req.Flash,
			Err:      errors.New("captureOne: empty source IP"),
		}
	}

	url := buildCaptureURL(req)

	// Per-request timeout layered on top of the client timeout.
	// If req.Timeout > 0 we wrap the context; the client's
	// own Timeout is left intact because http.Client.Timeout
	// covers the whole request lifetime including connection
	// setup, and the context-based deadline is additive.
	reqCtx := ctx
	if req.Timeout > 0 {
		var cancel context.CancelFunc
		reqCtx, cancel = context.WithTimeout(ctx, req.Timeout)
		defer cancel()
	}

	start := time.Now()
	httpReq, err := http.NewRequestWithContext(reqCtx, http.MethodGet, url, nil)
	if err != nil {
		return CaptureResult{
			MAC:      req.MAC,
			SourceIP: req.SourceIP,
			Flash:    req.Flash,
			Elapsed:  time.Since(start),
			Err:      fmt.Errorf("captureOne: build request: %w", err),
		}
	}

	resp, err := client.Do(httpReq)
	if err != nil {
		return CaptureResult{
			MAC:      req.MAC,
			SourceIP: req.SourceIP,
			Flash:    req.Flash,
			Elapsed:  time.Since(start),
			Err:      fmt.Errorf("captureOne: do request: %w", err),
		}
	}
	defer func() {
		// Drain any remaining body so the connection can be
		// returned to the pool (with DisableKeepAlives=true the
		// pool is unused, but the drain is still good hygiene
		// in case that flag ever flips).
		_, _ = io.Copy(io.Discard, resp.Body)
		_ = resp.Body.Close()
	}()

	if resp.StatusCode != http.StatusOK {
		return CaptureResult{
			MAC:        req.MAC,
			SourceIP:   req.SourceIP,
			Flash:      req.Flash,
			StatusCode: resp.StatusCode,
			Elapsed:    time.Since(start),
			Err: fmt.Errorf("captureOne: unexpected status %d from %s",
				resp.StatusCode, url),
		}
	}

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return CaptureResult{
			MAC:        req.MAC,
			SourceIP:   req.SourceIP,
			Flash:      req.Flash,
			StatusCode: resp.StatusCode,
			Elapsed:    time.Since(start),
			Err: fmt.Errorf("captureOne: read body from %s: %w",
				url, err),
		}
	}

	return CaptureResult{
		MAC:        req.MAC,
		SourceIP:   req.SourceIP,
		Flash:      req.Flash,
		StatusCode: resp.StatusCode,
		Body:       body,
		Elapsed:    time.Since(start),
	}
}

// buildCaptureURL assembles the full /capture URL for a device.
// Centralized so the "?flash=1" append logic is testable in
// isolation (no need to spin up an HTTP server to verify the
// query string).
//
// Output examples:
//   - "http://192.168.1.20/capture"
//   - "http://192.168.1.20/capture?flash=1"
//
// IPv6 hosts are bracketed to satisfy RFC 3986.
func buildCaptureURL(req CaptureRequest) string {
	host := req.SourceIP
	if net.ParseIP(host) != nil && net.ParseIP(host).To4() == nil {
		host = "[" + host + "]"
	}
	if req.Flash {
		return fmt.Sprintf("http://%s/capture?flash=1", host)
	}
	return fmt.Sprintf("http://%s/capture", host)
}
