// Package ports declares the contracts the application layer requires
// from its infrastructure. Concrete implementations live under
// internal/infrastructure and satisfy these interfaces.
package ports

// Field is a single structured log key/value pair. Adapters translate
// this into the underlying logger's native field type.
type Field struct {
	Key   string
	Value any
}

// Logger is the structured logging contract used throughout the
// service. The application layer logs through this interface, never
// through a concrete logger library.
type Logger interface {
	Info(msg string, fields ...Field)
	Warn(msg string, fields ...Field)
	Error(msg string, fields ...Field)
	Debug(msg string, fields ...Field)

	// Sync flushes any buffered log records. Implementations should
	// be safe to call multiple times.
	Sync() error
}
