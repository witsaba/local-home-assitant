// Package logger provides a zap.Logger bridged to the official otelzap
// bridge so structured log records flow into the OTel log SDK. Output
// always goes to os.Stderr — never os.Stdout (stdout is block-buffered
// and can drop logs when the process is killed by SIGTERM; see the
// locked-pattern note from PR #11).
package logger

import (
	"fmt"
	"os"
	"strings"

	"go.opentelemetry.io/contrib/bridges/otelzap"
	"go.opentelemetry.io/otel/log/noop"
	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"
)

// instrumentationName identifies this package in OTel log records.
const instrumentationName = "github.com/witsaba/local-home-assitant/services/workers"

// New returns a *zap.Logger using a JSON encoder to stderr. Records are
// also bridged to the OTel log SDK via otelzap using a noop provider so
// log calls don't crash in the absence of an exporter.
//
// logLevel must be one of "debug", "info", "warn", "error"
// (case-insensitive; empty defaults to "info"). Returns an error if the
// level string is unrecognised.
func New(logLevel string) (*zap.Logger, error) {
	lvl, err := parseLevel(logLevel)
	if err != nil {
		return nil, err
	}

	encoderCfg := zap.NewProductionEncoderConfig()
	encoderCfg.TimeKey = "ts"
	encoderCfg.LevelKey = "level"
	encoderCfg.MessageKey = "msg"
	encoderCfg.EncodeLevel = zapcore.LowercaseLevelEncoder
	encoderCfg.EncodeTime = zapcore.ISO8601TimeEncoder

	stderrCore := zapcore.NewCore(
		zapcore.NewJSONEncoder(encoderCfg),
		zapcore.AddSync(os.Stderr),
		lvl,
	)

	otelCore := otelzap.NewCore(
		instrumentationName,
		otelzap.WithLoggerProvider(noop.NewLoggerProvider()),
	)

	core := zapcore.NewTee(stderrCore, otelCore)
	return zap.New(core), nil
}

// parseLevel maps a level string to a zapcore.Level. Empty string defaults
// to info to cover the env-var unset case.
func parseLevel(s string) (zapcore.Level, error) {
	switch strings.ToLower(strings.TrimSpace(s)) {
	case "", "info":
		return zapcore.InfoLevel, nil
	case "debug":
		return zapcore.DebugLevel, nil
	case "warn":
		return zapcore.WarnLevel, nil
	case "error":
		return zapcore.ErrorLevel, nil
	default:
		return zapcore.InfoLevel, fmt.Errorf("logger: invalid level %q", s)
	}
}
