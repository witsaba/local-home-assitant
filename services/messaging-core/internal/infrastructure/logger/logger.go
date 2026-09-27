// Package logger is the zap-backed implementation of ports.Logger.
//
// The adapter is wired through the official otelzap bridge so every
// log line can carry trace_id / span_id as soon as the OTel SDK is
// initialised. Until then the bridge uses the global no-op log
// provider and behaves like a plain zap logger.
package logger

import (
	"os"
	"strings"

	"go.opentelemetry.io/contrib/bridges/otelzap"
	"go.opentelemetry.io/otel/log/global"
	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"

	"github.com/witsaba/local-home-assitant/services/messaging-core/internal/application/ports"
)

const instrumentationName = "github.com/witsaba/local-home-assitant/services/messaging-core"

// New returns a zap-backed ports.Logger configured for the given
// level ("debug", "info", "warn", "error"). Output is JSON on stdout
// and the otelzap bridge is attached as an additional sink so OTel
// can consume the same records with trace context.
func New(level string) (ports.Logger, error) {
	encCfg := zapcore.EncoderConfig{
		TimeKey:        "ts",
		LevelKey:       "level",
		NameKey:        "logger",
		CallerKey:      "caller",
		MessageKey:     "msg",
		StacktraceKey:  "stacktrace",
		LineEnding:     zapcore.DefaultLineEnding,
		EncodeLevel:    zapcore.LowercaseLevelEncoder,
		EncodeTime:     zapcore.ISO8601TimeEncoder,
		EncodeDuration: zapcore.SecondsDurationEncoder,
		EncodeCaller:   zapcore.ShortCallerEncoder,
	}

	core := zapcore.NewCore(
		zapcore.NewJSONEncoder(encCfg),
		zapcore.Lock(os.Stdout),
		levelToZap(level),
	)

	// otelzap.NewCore installs an additional zapcore.Core that emits
	// the same records into the configured LoggerProvider. The
	// global provider defaults to a no-op until an OTel SDK is
	// initialised (planned in a follow-up branch), so this is safe
	// to leave in place.
	otelCore := otelzap.NewCore(
		instrumentationName,
		otelzap.WithLoggerProvider(global.GetLoggerProvider()),
	)

	tee := zapcore.NewTee(core, otelCore)

	z := zap.New(tee, zap.AddCaller(), zap.AddStacktrace(zapcore.ErrorLevel))
	return &zapLogger{z: z}, nil
}

type zapLogger struct {
	z *zap.Logger
}

func (l *zapLogger) Info(msg string, fields ...ports.Field)  { l.z.Info(msg, toZap(fields)...) }
func (l *zapLogger) Warn(msg string, fields ...ports.Field)  { l.z.Warn(msg, toZap(fields)...) }
func (l *zapLogger) Error(msg string, fields ...ports.Field) { l.z.Error(msg, toZap(fields)...) }
func (l *zapLogger) Debug(msg string, fields ...ports.Field) { l.z.Debug(msg, toZap(fields)...) }
func (l *zapLogger) Sync() error                             { return l.z.Sync() }

// toZap translates ports.Field values into zap.Field values. Unknown
// types fall back to zap.Any so adapters don't need to enumerate
// every possible value type.
func toZap(ff []ports.Field) []zap.Field {
	out := make([]zap.Field, 0, len(ff))
	for _, f := range ff {
		if f.Key == "" {
			continue
		}
		out = append(out, zap.Any(f.Key, f.Value))
	}
	return out
}

func levelToZap(level string) zapcore.Level {
	switch strings.ToLower(level) {
	case "debug":
		return zapcore.DebugLevel
	case "warn", "warning":
		return zapcore.WarnLevel
	case "error":
		return zapcore.ErrorLevel
	default:
		return zapcore.InfoLevel
	}
}

// Compile-time guarantee that zapLogger satisfies ports.Logger.
var _ ports.Logger = (*zapLogger)(nil)
