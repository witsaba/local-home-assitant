// Package wsserver provides the HTTP/WebSocket server that serves camera
// streams to web clients. It uses Gin as the HTTP framework and
// gorilla/websocket for the WebSocket protocol.
package wsserver

import (
	"context"
	"errors"
	"net/http"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/gorilla/websocket"

	"github.com/witsaba/local-home-assitant/services/messaging-core/internal/application/ports"
	"github.com/witsaba/local-home-assitant/services/messaging-core/internal/infrastructure/devices"
)

const (
	// writeDeadline is the timeout for writing a WS message to a client.
	writeDeadline = 10 * time.Second

	// readDeadline is the timeout for reading a WS pong from a client.
	// Clients are expected to respond to server pings.
	readDeadline = 60 * time.Second

	// pingPeriod is how often the server sends ping frames to the client.
	pingPeriod = 50 * time.Second
)

// StreamHub is the interface to the camera hub, injected for testability.
// It handles viewer registration and deregistration.
type StreamHub interface {
	// RegisterViewer registers a web client's WS connection for a camera MAC.
	// The hub validates the MAC against the device DB, initiates a chip
	// connection if needed, and starts relaying frames to the client.
	// Returns an error if the MAC is unknown or unreachable.
	RegisterViewer(ctx context.Context, mac string, conn *websocket.Conn) error

	// DeregisterViewer removes a web client's WS connection from a camera's
	// viewer list. If no viewers remain for that MAC, the hub closes the
	// chip connection.
	DeregisterViewer(ctx context.Context, mac string, conn *websocket.Conn)
}

// DeviceRepo is the interface for querying device information.
type DeviceRepo interface {
	// GetByMAC returns a device by MAC address.
	// Returns devices.NotFoundError if not found.
	GetByMAC(ctx context.Context, mac string) (*devices.Device, error)
}

// Server is the HTTP/WS server that serves camera streams to web clients.
type Server struct {
	hub   StreamHub
	devs  DeviceRepo
	log   ports.Logger
	addr  string
	gin   *gin.Engine
	srv   *http.Server
	done  chan struct{}
}

// NewServer builds a Server that listens on addr and routes to h.
func NewServer(addr string, h StreamHub, d DeviceRepo, log ports.Logger) *Server {
	gin.SetMode(gin.ReleaseMode)
	r := gin.New()
	// Recover from panics so a single handler crash doesn't kill the server.
	r.Use(gin.Recovery())

	srv := &Server{
		hub:  h,
		devs: d,
		log:  log,
		addr: addr,
		gin:  r,
		done: make(chan struct{}),
	}

	r.GET("/healthz", srv.handleHealthz)
	r.GET("/stream/:mac", srv.handleStream)

	srv.srv = &http.Server{
		Addr:    addr,
		Handler: r,
	}

	return srv
}

// Start launches the HTTP server in a goroutine and returns immediately.
// Call Close() to shut it down.
func (s *Server) Start() {
	go func() {
		if err := s.srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			s.log.Error("wsserver: ListenAndServe failed",
				ports.Field{Key: "addr", Value: s.addr},
				ports.Field{Key: "err", Value: err.Error()},
			)
		}
		close(s.done)
	}()

	s.log.Info("wsserver: streaming gateway listening",
		ports.Field{Key: "addr", Value: s.addr},
	)
}

// Close gracefully shuts down the HTTP server.
func (s *Server) Close() error {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	return s.srv.Shutdown(ctx)
}

// handleHealthz returns 200 OK if the server is running.
func (s *Server) handleHealthz(c *gin.Context) {
	c.JSON(http.StatusOK, gin.H{"status": "ok"})
}

// handleStream upgrades the HTTP connection to WebSocket and registers
// the client as a viewer for the requested camera MAC.
func (s *Server) handleStream(c *gin.Context) {
	mac := c.Param("mac")
	if mac == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "mac is required"})
		return
	}

	// Validate the MAC exists in the DB before accepting the WS upgrade.
	_, err := s.devs.GetByMAC(c.Request.Context(), mac)
	if err != nil {
		if errors.Is(err, devices.NotFoundError) {
			c.JSON(http.StatusNotFound, gin.H{"error": "device_not_found", "mac": mac})
			return
		}
		s.log.Error("wsserver: device lookup failed",
			ports.Field{Key: "mac", Value: mac},
			ports.Field{Key: "err", Value: err.Error()},
		)
		c.JSON(http.StatusInternalServerError, gin.H{"error": "internal_error"})
		return
	}

	// Upgrade to WebSocket.
	upgrader := websocket.Upgrader{
		CheckOrigin: func(r *http.Request) bool {
			// Allow all origins on a local LAN for now.
			return true
		},
		ReadBufferSize:  1024,
		WriteBufferSize: 1024,
	}

	conn, err := upgrader.Upgrade(c.Writer, c.Request, nil)
	if err != nil {
		s.log.Warn("wsserver: WS upgrade failed",
			ports.Field{Key: "mac", Value: mac},
			ports.Field{Key: "err", Value: err.Error()},
		)
		return
	}

	// Register the viewer with the hub.
	if err := s.hub.RegisterViewer(c.Request.Context(), mac, conn); err != nil {
		s.log.Warn("wsserver: RegisterViewer failed",
			ports.Field{Key: "mac", Value: mac},
			ports.Field{Key: "err", Value: err.Error()},
		)
		conn.Close()
		return
	}

	// Start the ping/pong keepalive loop for this client.
	// The hub handles frame relay; this goroutine only manages WS health.
	go s.pingLoop(conn, mac)

	s.log.Info("wsserver: viewer connected",
		ports.Field{Key: "mac", Value: mac},
	)
}

// pingLoop sends periodic ping frames to the client and reads pong responses.
// If the client disappears, the WS read loop will fail and the hub will
// deregister the viewer via its own connection monitoring.
func (s *Server) pingLoop(conn *websocket.Conn, mac string) {
	ticker := time.NewTicker(pingPeriod)
	defer ticker.Stop()

	conn.SetReadDeadline(time.Now().Add(readDeadline))
	conn.SetPongHandler(func(_ string) error {
		conn.SetReadDeadline(time.Now().Add(readDeadline))
		return nil
	})

	for range ticker.C {
		if err := conn.WriteControl(websocket.PingMessage, nil, time.Now().Add(writeDeadline)); err != nil {
			s.log.Debug("wsserver: ping failed, closing",
				ports.Field{Key: "mac", Value: mac},
				ports.Field{Key: "err", Value: err.Error()},
			)
			// Trigger deregistration by closing the connection.
			// The hub's read goroutine will notice and call DeregisterViewer.
			conn.Close()
			return
		}
	}
}


