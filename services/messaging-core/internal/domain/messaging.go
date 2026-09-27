// Package domain holds the platform-agnostic types the rest of the
// service reasons about. The domain MUST NOT import infrastructure
// (zap, NATS, OS-level packages); it stays pure Go.
package domain

// Subject identifies a NATS publish/subscribe channel. Producers and
// consumers (added in follow-up branches) reason in terms of Subjects.
//
// Example: Subject("devices.telemetry.v1").
type Subject string

// Message is a generic value carried on a Subject. Concrete payload
// schemas will be introduced by later features; this skeleton only
// names the shape.
type Message struct {
	Subject Subject
	Payload []byte
}
