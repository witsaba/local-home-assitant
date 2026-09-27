package logger_test

import (
	"testing"

	"github.com/witsaba/local-home-assitant/services/workers/internal/infrastructure/logger"
	"go.uber.org/zap"
)

func TestNew_AllValidLevels(t *testing.T) {
	t.Parallel()
	for _, lvl := range []string{"debug", "info", "warn", "error", ""} {
		lvl := lvl
		t.Run("level="+lvl, func(t *testing.T) {
			t.Parallel()
			got, err := logger.New(lvl)
			if err != nil {
				t.Fatalf("New(%q) returned error: %v", lvl, err)
			}
			if got == nil {
				t.Fatal("New returned nil logger")
			}
			// Emit a probe record at every level that is enabled.
			got.Info("probe", zap.String("k", "v"))
		})
	}
}

func TestNew_RejectsUnknownLevel(t *testing.T) {
	t.Parallel()
	for _, lvl := range []string{"trace", "fatal", "verbose", "weird"} {
		lvl := lvl
		t.Run(lvl, func(t *testing.T) {
			t.Parallel()
			if _, err := logger.New(lvl); err == nil {
				t.Errorf("New(%q) should return an error", lvl)
			}
		})
	}
}

func TestNew_LevelIsCaseInsensitiveAndTrimmed(t *testing.T) {
	t.Parallel()
	for _, lvl := range []string{"DEBUG", "Info", "WARN", "Error", "  debug  "} {
		lvl := lvl
		t.Run(lvl, func(t *testing.T) {
			t.Parallel()
			got, err := logger.New(lvl)
			if err != nil {
				t.Errorf("New(%q) returned error: %v", lvl, err)
			}
			if got == nil {
				t.Error("got nil logger")
			}
		})
	}
}
