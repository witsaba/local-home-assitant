package logger

import (
	"bytes"
	"encoding/json"
	"strings"
	"sync"
	"testing"

	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"

	"github.com/witsaba/local-home-assitant/services/messaging-core/internal/application/ports"
)

// safeBuffer is a goroutine-safe wrapper around bytes.Buffer so
// parallel tests don't corrupt each other's stdout capture.
type safeBuffer struct {
	mu  sync.Mutex
	buf bytes.Buffer
}

func (b *safeBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.Write(p)
}

func (b *safeBuffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.String()
}

// Sync satisfies zapcore.WriteSyncer. In-memory buffer needs no
// flushing, so this is a no-op.
func (b *safeBuffer) Sync() error { return nil }

// newCapturedLogger wires a logger that writes to the captured buffer
// instead of the real stdout. The otelzap bridge is intentionally
// detached so this unit test stays a true unit test (no global OTel
// state required).
func newCapturedLogger(t *testing.T, level string) (ports.Logger, *safeBuffer) {
	t.Helper()
	encCfg := zap.NewProductionEncoderConfig()
	encCfg.TimeKey = "ts"
	encCfg.MessageKey = "msg"

	buf := &safeBuffer{}
	core := zapcore.NewCore(
		zapcore.NewJSONEncoder(encCfg),
		zapcore.Lock(buf),
		levelToZap(level),
	)
	z := zap.New(core, zap.AddCaller())
	return &zapLogger{z: z}, buf
}

func TestLogger_EmitsJSON(t *testing.T) {
	log, buf := newCapturedLogger(t, "info")
	log.Info("hello", ports.Field{Key: "name", Value: "world"})

	out := buf.String()
	if out == "" {
		t.Fatal("expected log output, got empty buffer")
	}
	var line map[string]any
	if err := json.Unmarshal([]byte(strings.TrimSpace(out)), &line); err != nil {
		t.Fatalf("output is not valid JSON: %v\nraw: %s", err, out)
	}
	if line["msg"] != "hello" {
		t.Errorf("msg: got %v, want hello", line["msg"])
	}
	if line["name"] != "world" {
		t.Errorf("name: got %v, want world", line["name"])
	}
}

func TestLogger_RespectsLevel(t *testing.T) {
	log, buf := newCapturedLogger(t, "warn")
	log.Debug("debug-line")
	log.Info("info-line")
	log.Warn("warn-line")
	log.Error("error-line")

	out := buf.String()
	if strings.Contains(out, "debug-line") {
		t.Errorf("debug line should be filtered at warn level\n%s", out)
	}
	if strings.Contains(out, "info-line") {
		t.Errorf("info line should be filtered at warn level\n%s", out)
	}
	if !strings.Contains(out, "warn-line") {
		t.Errorf("expected warn line\n%s", out)
	}
	if !strings.Contains(out, "error-line") {
		t.Errorf("expected error line\n%s", out)
	}
}

func TestLogger_Sync_NoError(t *testing.T) {
	log, _ := newCapturedLogger(t, "info")
	if err := log.Sync(); err != nil {
		t.Errorf("Sync() returned error: %v", err)
	}
}

func TestLogger_EmptyKeySkipped(t *testing.T) {
	log, buf := newCapturedLogger(t, "info")
	log.Info("msg", ports.Field{Key: "", Value: "ignored"})
	if !strings.Contains(buf.String(), `"msg":"msg"`) {
		t.Errorf("expected the log line, got %q", buf.String())
	}
}

func TestNew_DefaultLevel(t *testing.T) {
	// Calling New with a bogus level should still produce a working
	// logger at info level (silently coerced, per levelToZap).
	l, err := New("not-a-level")
	if err != nil {
		t.Fatalf("New returned unexpected error: %v", err)
	}
	if l == nil {
		t.Fatal("New returned nil logger")
	}
	_ = l.Sync()
}
