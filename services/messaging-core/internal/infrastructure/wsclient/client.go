// Package wsclient provides a WebSocket client that connects to ESP32
// camera chips' /ws/cams endpoint. It reads JPEG frames and relays
// them via a callback, logging hello/status frames for observability.
package wsclient

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/gorilla/websocket"

	"github.com/witsaba/local-home-assitant/services/messaging-core/internal/application/ports"
)

const (
	// wsHandshakeTimeout is the maximum time to complete the WS upgrade.
	wsHandshakeTimeout = 5 * time.Second

	// wsReadTimeout is the maximum time between messages.
	// The chip sends a JPEG frame every 100ms (10fps) plus a status frame
	// every 30s. 45s gives headroom for GC pauses on the chip.
	wsReadTimeout = 45 * time.Second

	// maxReconnectAttempts limits how many times we retry after a disconnect.
	maxReconnectAttempts = 5
)

// Dialer is the websocket dialer used for chip connections.
// Exposed so tests can inject a mock or test dialer.
var Dialer = &websocket.Dialer{
	HandshakeTimeout: wsHandshakeTimeout,
	// NetDialContext is nil — use the default TCP stack.
	// ReadBufferSize/WriteBufferSize are 0 → sizes chosen by websocket lib.
}

// ChipClient connects to a chip's /ws/cams endpoint and reads frames.
type ChipClient struct {
	chipIP  string
	onFrame func([]byte) // called with each binary JPEG frame; nil means disconnected
	onClose func()       // called when the connection closes (error or clean)
	log     ports.Logger

	mu     sync.Mutex
	conn   *websocket.Conn
	done   chan struct{} // closed when the read loop exits
	closed bool
}

// NewChipClient builds a ChipClient that will connect to ws://ip/ws/cams.
func NewChipClient(chipIP string, onFrame func([]byte), onClose func(), log ports.Logger) *ChipClient {
	return &ChipClient{
		chipIP:  chipIP,
		onFrame:  onFrame,
		onClose:  onClose,
		log:     log,
		done:    make(chan struct{}),
	}
}

// URL returns the WebSocket URL for the chip's /ws/cams endpoint.
func (c *ChipClient) URL() string {
	return fmt.Sprintf("ws://%s/ws/cams", c.chipIP)
}

// Connect establishes the WebSocket connection to the chip and starts
// a goroutine that reads frames and calls onFrame. It is safe to call
// Connect multiple times; subsequent calls return an error if the
// client is already connected or closed.
func (c *ChipClient) Connect(ctx context.Context) error {
	c.mu.Lock()
	if c.closed {
		c.mu.Unlock()
		return fmt.Errorf("wsclient: client is closed")
	}
	if c.conn != nil {
		c.mu.Unlock()
		return fmt.Errorf("wsclient: already connected")
	}
	c.mu.Unlock()

	url := c.URL()
	conn, resp, err := Dialer.DialContext(ctx, url, nil)
	if err != nil {
		if resp != nil {
			resp.Body.Close()
		}
		return fmt.Errorf("wsclient: dial %s: %w", url, err)
	}

	// Configure read deadline and ping handler.
	conn.SetReadDeadline(time.Now().Add(wsReadTimeout))
	conn.SetPingHandler(func(_ string) error {
		// Automatically respond to ping frames so the connection stays alive.
		conn.SetReadDeadline(time.Now().Add(wsReadTimeout))
		return nil
	})

	c.mu.Lock()
	c.conn = conn
	c.done = make(chan struct{})
	c.mu.Unlock()

	go c.readLoop()

	return nil
}

// readLoop reads messages from the chip WS until the connection closes.
// It is run in a single goroutine per ChipClient instance, so onFrame
// calls are serialized (no external locking needed).
func (c *ChipClient) readLoop() {
	defer func() {
		c.mu.Lock()
		if c.conn != nil {
			c.conn.Close()
			c.conn = nil
		}
		c.onFrame = nil
		close(c.done)
		c.mu.Unlock()

		if c.onClose != nil {
			c.onClose()
		}
	}()

	for {
		msgType, data, err := c.conn.ReadMessage()
		if err != nil {
			// Check if this is a clean close.
			if isCleanClose(err) {
				c.log.Debug("wsclient: chip disconnected cleanly",
					ports.Field{Key: "url", Value: c.URL()})
				return
			}
			c.log.Warn("wsclient: read error",
				ports.Field{Key: "url", Value: c.URL()},
				ports.Field{Key: "err", Value: err.Error()})
			return
		}

		switch msgType {
		case websocket.TextMessage:
			c.handleText(data)

		case websocket.BinaryMessage:
			c.handleBinary(data)

		default:
			// Ignore unknown message types (pings/pongs handled by SetPingHandler).
		}
	}
}

// handleText logs hello and status frames for observability.
// The hello is the first text frame; status frames arrive every 30s.
func (c *ChipClient) handleText(text []byte) {
	// Surface hello at info level — useful for confirming chip identity.
	if strings.Contains(string(text), `"type":"hello"`) {
		c.log.Info("wsclient: chip hello",
			ports.Field{Key: "url", Value: c.URL()},
			ports.Field{Key: "hello", Value: string(text)})
	} else {
		c.log.Debug("wsclient: received text frame",
			ports.Field{Key: "url", Value: c.URL()},
			ports.Field{Key: "text", Value: string(text)})
	}
}

// handleBinary relays a JPEG frame to the registered callback.
// c.onFrame is protected by the c.mu lock only at write time;
// readLoop is single-threaded so no additional locking is needed here.
func (c *ChipClient) handleBinary(data []byte) {
	c.mu.Lock()
	onFrame := c.onFrame
	c.mu.Unlock()

	if onFrame == nil {
		// No handler registered (disconnected while reading) — drop the frame.
		return
	}
	onFrame(data)
}

// Close gracefully closes the chip WebSocket connection.
// It waits for the read loop to exit before returning.
// Safe to call multiple times.
func (c *ChipClient) Close(ctx context.Context) error {
	c.mu.Lock()
	if c.closed {
		c.mu.Unlock()
		return nil
	}
	c.closed = true
	conn := c.conn
	done := c.done
	c.mu.Unlock()

	if conn == nil {
		return nil
	}

	// Signal the read loop to exit and close the connection.
	conn.SetReadDeadline(time.Now().Add(1 * time.Second))

	// Wait for the read loop to exit.
	select {
	case <-done:
	case <-ctx.Done():
		return ctx.Err()
	}

	return nil
}

// isCleanClose returns true when err indicates a normal WebSocket close
// initiated by the remote peer or a local close.
func isCleanClose(err error) bool {
	if err == nil {
		return false
	}
	if strings.Contains(err.Error(), "use of closed network connection") {
		return true
	}
	if _, ok := err.(*websocket.CloseError); ok {
		return true
	}
	return false
}

// IsConnected reports whether the client has an active chip connection.
func (c *ChipClient) IsConnected() bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.conn != nil && !c.closed
}
