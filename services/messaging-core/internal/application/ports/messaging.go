package ports

import "context"

// Server is the embedded messaging-broker contract used by the
// application layer. The concrete implementation (currently NATS)
// lives under internal/infrastructure.
type Server interface {
	// Start brings up the embedded server. Implementations MUST block
	// until either the server reports ready or an error occurs.
	// Calling Start more than once is undefined.
	Start(ctx context.Context) error

	// Ready returns a channel that is closed once the server is
	// accepting client connections. Callers should wait on Ready()
	// before attempting to dial in.
	Ready() <-chan struct{}

	// Addr returns the dial address of the running server, e.g.
	// "nats://127.0.0.1:4222". The result is only meaningful after
	// the channel returned by Ready has been closed.
	Addr() string

	// Shutdown asks the server to stop accepting new clients, drain
	// existing connections, and exit. It is safe to call after a
	// failed Start.
	Shutdown(ctx context.Context) error
}
