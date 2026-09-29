// Package streamhub manages per-camera state for the camera streaming gateway.
// It maintains a viewer registry per camera MAC and lazily connects to chip
// WebSocket endpoints when viewers request a stream.
package streamhub

import (
	"context"
	"fmt"
	"net"
	"sync"
	"time"

	"github.com/gorilla/websocket"

	"github.com/witsaba/local-home-assitant/services/messaging-core/internal/application/ports"
	"github.com/witsaba/local-home-assitant/services/messaging-core/internal/infrastructure/devices"
	"github.com/witsaba/local-home-assitant/services/messaging-core/internal/infrastructure/wsclient"
)

const (
	// reconnectMaxAttempts is the maximum number of reconnection attempts
	// after a chip disconnect.
	reconnectMaxAttempts = 5

	// reconnectBaseDelay is the initial delay between reconnection attempts.
	// Each subsequent attempt doubles the delay: 1s, 2s, 4s, 8s, 16s.
	reconnectBaseDelay = 1 * time.Second

	// viewerWriteTimeout bounds a single frame write to one viewer.
	//
	// Without it, a viewer that vanished without the server noticing (which
	// used to be the normal case: nothing deregistered, see the DeregisterViewer
	// wiring) leaves a socket whose kernel buffers eventually fill. The write
	// then blocks FOREVER inside WriteMessage, and because this loop is
	// synchronous and holds no lock, the chip read loop is never drained, the
	// chip's own send buffer backs up, and the chip stops streaming. One dead
	// viewer therefore silenced the camera for everybody.
	viewerWriteTimeout = 5 * time.Second
)

// DeviceRepo is the interface for querying device information.
type DeviceRepo interface {
	// GetByMAC returns a device by MAC address.
	// Returns devices.NotFoundError if not found.
	GetByMAC(ctx context.Context, mac string) (*devices.Device, error)
}

// ChipClientFactory creates wsclient.WSClient instances.
type ChipClientFactory func(
	chipIP string,
	onFrame func([]byte),
	onClose func(),
	log ports.Logger,
) wsclient.WSClient

// CameraHub is the central registry for camera streams. It maps each camera
// MAC to a set of web viewer connections and manages the chip WebSocket
// connection lifecycle.
//
// Thread safety: All methods are safe for concurrent use. The hub uses
// per-camera mutexes for fine-grained locking; global mu protects the
// camera map.
type CameraHub struct {
	devs      DeviceRepo
	log       ports.Logger
	newClient ChipClientFactory

	mu      sync.RWMutex
	cameras map[string]*cameraState // keyed by MAC
}

// NewCameraHub builds a new CameraHub.
func NewCameraHub(devs DeviceRepo, log ports.Logger) *CameraHub {
	return &CameraHub{
		devs: devs,
		log:  log,
		newClient: func(ip string, onFrame func([]byte), onClose func(), log ports.Logger) wsclient.WSClient {
			return wsclient.NewChipClient(ip, onFrame, onClose, log)
		},
		cameras: make(map[string]*cameraState),
	}
}

// WithChipClientFactory overrides the chip client factory (for testing).
func (h *CameraHub) WithChipClientFactory(f ChipClientFactory) *CameraHub {
	h.newClient = f
	return h
}

// injectMockChipClient is a test helper that injects a no-op chip client
// so tests don't need a real chip. Defined locally so tests can use it
// without importing wsclient.
func (h *CameraHub) injectMockChipClient() {
	h.newClient = func(_ string, _ func([]byte), _ func(), _ ports.Logger) wsclient.WSClient {
		return &mockWSClient{}
	}
}

// cameraState holds all state for a single camera MAC.
type cameraState struct {
	mu     sync.Mutex
	mac    string
	ip     string // current chip IP
	viewers map[*websocket.Conn]struct{}
	client wsclient.WSClient // nil if not connected
	closed bool                // true when the camera is torn down
}

// RegisterViewer registers a web client's WebSocket connection as a viewer
// for the given camera MAC. It lazily initiates a chip connection if no
// viewer is currently watching this camera.
//
// Returns an error if the MAC is unknown, has no known IP, or the chip
// is unreachable.
func (h *CameraHub) RegisterViewer(ctx context.Context, mac string, conn *websocket.Conn) error {
	// 1. Look up the device in the DB.
	dev, err := h.devs.GetByMAC(ctx, mac)
	if err != nil {
		return fmt.Errorf("streamhub: device %q not found: %w", mac, err)
	}

	if dev.LastSourceIP == nil {
		return fmt.Errorf("streamhub: device %q has no known IP address", mac)
	}

	chipIP := dev.LastSourceIP.String()

	// 2. Get or create the camera state.
	state := h.getOrCreateCamera(mac, chipIP)

	// 3. Register the viewer.
	state.mu.Lock()
	if state.closed {
		state.mu.Unlock()
		return fmt.Errorf("streamhub: camera %q is closed", mac)
	}
	state.viewers[conn] = struct{}{}
	// Need a chip connection if there is none, OR if the one we hold is no
	// longer connected. The second condition is the important one: a client
	// object can outlive its socket, and a stale-but-non-nil state.client
	// makes every later viewer wait on a connection that can never deliver,
	// with no error to observe. Measured on the Pi: viewer gets 101 and then
	// nothing, forever, while the previous socket sits ESTAB with 1.3MB
	// unread because its reader is gone.
	needConnect := state.client == nil ||
		!state.client.IsConnected()
	state.mu.Unlock()

	if needConnect {
		h.log.Info("streamhub: first viewer for camera, connecting to chip",
			ports.Field{Key: "mac", Value: mac},
			ports.Field{Key: "chip_ip", Value: chipIP},
		)
		if err := h.connectChip(state, chipIP); err != nil {
			// Rollback: remove the viewer.
			state.mu.Lock()
			delete(state.viewers, conn)
			state.mu.Unlock()
			return fmt.Errorf("streamhub: chip connection failed for %s: %w", mac, err)
		}
	}

	h.log.Info("streamhub: viewer registered",
		ports.Field{Key: "mac", Value: mac},
		ports.Field{Key: "viewer_count", Value: func() int {
			state.mu.Lock()
			defer state.mu.Unlock()
			return len(state.viewers)
		}()},
	)

	return nil
}

// DeregisterViewer removes a web client's connection from a camera's
// viewer list. If no viewers remain, the chip connection is closed.
func (h *CameraHub) DeregisterViewer(_ context.Context, mac string, conn *websocket.Conn) {
	state := h.getCamera(mac)
	if state == nil {
		h.log.Debug("streamhub: DeregisterViewer called on unknown camera",
			ports.Field{Key: "mac", Value: mac},
		)
		return
	}

	state.mu.Lock()
	delete(state.viewers, conn)
	viewerCount := len(state.viewers)
	state.mu.Unlock()

	h.log.Info("streamhub: viewer deregistered",
		ports.Field{Key: "mac", Value: mac},
		ports.Field{Key: "viewer_count", Value: viewerCount},
	)

	// If no viewers remain, close the chip connection.
	if viewerCount == 0 {
		h.closeChip(state)
	}
}

// Close cleanly shuts down all chip connections and viewer state.
// It is safe to call multiple times.
func (h *CameraHub) Close(_ context.Context) {
	h.mu.Lock()
	defer h.mu.Unlock()

	for mac, state := range h.cameras {
		state.mu.Lock()
		if state.client != nil {
			state.client.Close(context.Background())
		}
		state.closed = true
		state.mu.Unlock()
		delete(h.cameras, mac)
	}
}

// — private helpers —

// getOrCreateCamera returns the cameraState for mac, creating it if needed.
// If the chip IP has changed since the last known state, it closes the old
// chip connection so a new one will be established with the updated IP.
func (h *CameraHub) getOrCreateCamera(mac, chipIP string) *cameraState {
	h.mu.Lock()
	defer h.mu.Unlock()

	state, ok := h.cameras[mac]
	if ok {
		// Check if the chip IP changed.
		state.mu.Lock()
		if state.ip != chipIP && state.client != nil {
			h.log.Info("streamhub: chip IP changed, reconnecting",
				ports.Field{Key: "mac", Value: mac},
				ports.Field{Key: "old_ip", Value: state.ip},
				ports.Field{Key: "new_ip", Value: chipIP},
			)
			state.client.Close(context.Background())
			state.client = nil
		}
		state.ip = chipIP
		state.mu.Unlock()
		return state
	}

	state = &cameraState{
		mac:     mac,
		ip:      chipIP,
		viewers: make(map[*websocket.Conn]struct{}),
	}
	h.cameras[mac] = state
	return state
}

// getCamera returns the cameraState for mac without creating it.
func (h *CameraHub) getCamera(mac string) *cameraState {
	h.mu.RLock()
	defer h.mu.RUnlock()
	return h.cameras[mac]
}

// connectChip initiates a WebSocket connection to the chip and registers
// the onFrame callback to fan-out to all viewers.
func (h *CameraHub) connectChip(state *cameraState, chipIP string) error {
	state.mu.Lock()
	if state.client != nil && state.client.IsConnected() {
		state.mu.Unlock()
		return nil // already connected
	}
	// Any client object still parked here is detached and replaced. Leaving it
	// in place would let RegisterViewer's !IsConnected() check treat the dead
	// connection as the live one on the next pass.
	stale := state.client
	state.client = nil
	state.mu.Unlock()

	if stale != nil {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		_ = stale.Close(ctx)
		cancel()
	}

	client := h.newClient(
		chipIP,
		func(frame []byte) {
			h.broadcastToViewers(state, frame)
		},
		func() {
			h.onChipDisconnected(state)
		},
		h.log,
	)

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	err := client.Connect(ctx)
	cancel()

	if err != nil {
		h.log.Warn("streamhub: chip connection failed",
			ports.Field{Key: "mac", Value: state.mac},
			ports.Field{Key: "ip", Value: chipIP},
			ports.Field{Key: "err", Value: err.Error()},
		)
		return err
	}

	state.mu.Lock()
	state.client = client
	state.mu.Unlock()

	return nil
}

// closeChip gracefully closes the chip connection for a camera.
//
// The client is detached from the state UNDER the lock and closed OUTSIDE it.
// Closing a ChipClient waits for its read loop to exit, and that read loop's
// deferred onClose callback re-enters this cameraState to take state.mu -- so
// closing while holding the lock risks a self-deadlock. The context is bounded
// for the same reason: an unbounded wait on a wedged socket would block every
// later viewer for that camera indefinitely.
func (h *CameraHub) closeChip(state *cameraState) {
	state.mu.Lock()
	client := state.client
	state.client = nil
	hadClient := client != nil
	state.mu.Unlock()

	if !hadClient {
		return
	}

	h.log.Info("streamhub: last viewer left, closing chip connection",
		ports.Field{Key: "mac", Value: state.mac},
	)

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := client.Close(ctx); err != nil {
		h.log.Warn("streamhub: chip close did not complete cleanly",
			ports.Field{Key: "mac", Value: state.mac},
			ports.Field{Key: "err", Value: err.Error()},
		)
	}
}

// broadcastToViewers sends a frame to all registered web viewers for a camera.
// It removes any viewer whose write fails.
func (h *CameraHub) broadcastToViewers(state *cameraState, frame []byte) {
	state.mu.Lock()
	viewers := make([]*websocket.Conn, 0, len(state.viewers))
	for conn := range state.viewers {
		viewers = append(viewers, conn)
	}
	state.mu.Unlock()

	for _, conn := range viewers {
		// Bound every write. A viewer that has gone away must fail this call
		// rather than wedge the relay for everyone else.
		if err := conn.SetWriteDeadline(time.Now().Add(viewerWriteTimeout)); err != nil {
			state.mu.Lock()
			delete(state.viewers, conn)
			state.mu.Unlock()
			go conn.Close()
			continue
		}
		if err := conn.WriteMessage(websocket.BinaryMessage, frame); err != nil {
			h.log.Debug("streamhub: failed to write to viewer, removing",
				ports.Field{Key: "mac", Value: state.mac},
				ports.Field{Key: "err", Value: err.Error()},
			)
			// Remove the viewer; the WS close is asynchronous.
			state.mu.Lock()
			delete(state.viewers, conn)
			state.mu.Unlock()
			// The viewer will trigger DeregisterViewer on their side.
			go conn.Close()
		}
	}
}

// onChipDisconnected is called by the chip client when the chip disconnects.
// It clears state.client and attempts to reconnect with exponential backoff.
func (h *CameraHub) onChipDisconnected(state *cameraState) {
	state.mu.Lock()
	mac := state.mac
	chipIP := state.ip
	client := state.client
	// Drop the stale pointer.
	//
	// This is load-bearing. RegisterViewer decides whether to dial the chip
	// with `needsConnect := state.client == nil`, so leaving a dead client
	// parked here means every LATER viewer is registered onto a connection
	// that can never produce a frame: the socket upgrades 101, looks healthy
	// in every log and in the nginx access log, and then delivers nothing,
	// forever. There is no error to see, because nothing failed. Measured on
	// the Pi: viewer_count stuck above zero, zero chip sockets held, zero
	// reconnect attempts, zero frames, while all three chips answered
	// /capture with valid JPEGs.
	state.client = nil
	state.mu.Unlock()

	if client == nil {
		return
	}

	// Check if there are still viewers — if not, no need to reconnect.
	state.mu.Lock()
	hasViewers := len(state.viewers) > 0
	state.mu.Unlock()

	if !hasViewers {
		h.log.Debug("streamhub: chip disconnected but no viewers, skipping reconnect",
			ports.Field{Key: "mac", Value: mac},
		)
		return
	}

	h.log.Warn("streamhub: chip disconnected, reconnecting",
		ports.Field{Key: "mac", Value: mac},
		ports.Field{Key: "ip", Value: chipIP},
	)

	h.reconnectWithBackoff(state, chipIP)
}

// reconnectWithBackoff retries the chip connection up to reconnectMaxAttempts
// with exponential backoff (1s, 2s, 4s, 8s, 16s).
func (h *CameraHub) reconnectWithBackoff(state *cameraState, chipIP string) {
	delay := reconnectBaseDelay

	for attempt := 1; attempt <= reconnectMaxAttempts; attempt++ {
		// Check if we should still be trying.
		state.mu.Lock()
		hasViewers := len(state.viewers) > 0
		state.mu.Unlock()
		if !hasViewers {
			h.log.Info("streamhub: reconnect cancelled (no viewers)",
				ports.Field{Key: "mac", Value: state.mac},
				ports.Field{Key: "attempt", Value: attempt},
			)
			return
		}

		time.Sleep(delay)

		state.mu.Lock()
		if state.client != nil {
			state.mu.Unlock()
			return
		}
		state.mu.Unlock()

		h.log.Info("streamhub: reconnect attempt",
			ports.Field{Key: "mac", Value: state.mac},
			ports.Field{Key: "attempt", Value: attempt},
			ports.Field{Key: "delay", Value: delay.String()},
		)

		client := h.newClient(
			chipIP,
			func(frame []byte) {
				h.broadcastToViewers(state, frame)
			},
			func() {
				h.onChipDisconnected(state)
			},
			h.log,
		)

		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		err := client.Connect(ctx)
		cancel()

		if err == nil {
			state.mu.Lock()
			state.client = client
			state.mu.Unlock()

			h.log.Info("streamhub: chip reconnected",
				ports.Field{Key: "mac", Value: state.mac},
				ports.Field{Key: "attempt", Value: attempt},
			)
			return
		}

		h.log.Warn("streamhub: reconnect attempt failed",
			ports.Field{Key: "mac", Value: state.mac},
			ports.Field{Key: "attempt", Value: attempt},
			ports.Field{Key: "err", Value: err.Error()},
		)

		delay *= 2
	}

	h.log.Error("streamhub: chip reconnect exhausted all attempts",
		ports.Field{Key: "mac", Value: state.mac},
		ports.Field{Key: "max_attempts", Value: reconnectMaxAttempts},
	)
}

// mockWSClient is a no-op wsclient.WSClient for testing.
type mockWSClient struct{}

func (m *mockWSClient) Connect(_ context.Context) error { return nil }
func (m *mockWSClient) Close(_ context.Context) error  { return nil }
func (m *mockWSClient) IsConnected() bool               { return true }

// GetViewerCount returns the number of active viewers for a camera MAC.
// Returns 0 if the camera is not registered.
func (h *CameraHub) GetViewerCount(mac string) int {
	state := h.getCamera(mac)
	if state == nil {
		return 0
	}
	state.mu.Lock()
	defer state.mu.Unlock()
	return len(state.viewers)
}

// GetChipIP returns the current chip IP for a camera MAC.
// Returns empty string if the camera is not registered.
func (h *CameraHub) GetChipIP(mac string) string {
	state := h.getCamera(mac)
	if state == nil {
		return ""
	}
	state.mu.Lock()
	defer state.mu.Unlock()
	return state.ip
}

// IPString returns the string representation of a net.IP.
func IPString(ip net.IP) string {
	if ip == nil {
		return ""
	}
	return ip.String()
}
