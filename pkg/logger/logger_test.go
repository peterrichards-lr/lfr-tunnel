package logger

import (
	"log/slog"
	"os"
	"testing"
)

func TestInitLogger(t *testing.T) {
	Init("debug", true)
	slog.Info("Test debug mode")

	Init("info", false)
	slog.Info("Test info mode")

	// Verify no panics and works fine
	// Revert to normal
	Init("info", false)
}

func TestLoggerOutput(t *testing.T) {
	tmpFile, _ := os.CreateTemp("", "logger-test-*.log")
	defer func() { _ = os.Remove(tmpFile.Name()) }()

	// Set as default to test nothing panics
	logger := slog.New(slog.NewJSONHandler(tmpFile, nil))
	slog.SetDefault(logger)

	Init("debug", true)
	slog.Debug("test")
}
