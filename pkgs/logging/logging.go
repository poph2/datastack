// Package logging configures the structured slog logger shared by all services.
package logging

import (
	"fmt"
	"log/slog"
	"os"
	"strings"
)

// New returns a logger writing to stdout and makes it the slog default, so
// libraries that log through slog end up in the same stream.
// level is one of debug, info, warn, error; format is json or text.
func New(serviceName, level, format string) (*slog.Logger, error) {
	var lvl slog.Level
	if err := lvl.UnmarshalText([]byte(level)); err != nil {
		return nil, fmt.Errorf("parsing log level %q: %w", level, err)
	}

	opts := &slog.HandlerOptions{Level: lvl}

	var handler slog.Handler
	switch strings.ToLower(format) {
	case "json":
		handler = slog.NewJSONHandler(os.Stdout, opts)
	case "text":
		handler = slog.NewTextHandler(os.Stdout, opts)
	default:
		return nil, fmt.Errorf("unknown log format %q (want json or text)", format)
	}

	logger := slog.New(handler).With("service", serviceName)
	slog.SetDefault(logger)
	return logger, nil
}
