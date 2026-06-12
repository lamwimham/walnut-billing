package logger

import (
	"log/slog"
	"os"
)

// Init initializes the structured logger.
// In production, use JSON format. In development, use text format.
func Init(env string) *slog.Logger {
	var handler slog.Handler
	opts := &slog.HandlerOptions{
		Level: slog.LevelInfo,
	}

	if env == "dev" || env == "development" {
		opts.Level = slog.LevelDebug
		handler = slog.NewTextHandler(os.Stdout, opts)
	} else {
		handler = slog.NewJSONHandler(os.Stdout, opts)
	}

	logger := slog.New(handler)
	slog.SetDefault(logger)
	return logger
}

// L returns the default logger.
func L() *slog.Logger {
	return slog.Default()
}
