package logging

import (
	"fmt"
	"io"
	"log/slog"
	"strings"
)

func New(levelName, format string, output io.Writer) (*slog.Logger, error) {
	var level slog.Level
	if err := level.UnmarshalText([]byte(strings.ToUpper(levelName))); err != nil {
		return nil, fmt.Errorf("invalid log level %q: %w", levelName, err)
	}

	options := &slog.HandlerOptions{Level: level}
	var handler slog.Handler
	switch strings.ToLower(format) {
	case "json":
		handler = slog.NewJSONHandler(output, options)
	case "text":
		handler = slog.NewTextHandler(output, options)
	default:
		return nil, fmt.Errorf("invalid log format %q: expected json or text", format)
	}
	return slog.New(handler), nil
}
