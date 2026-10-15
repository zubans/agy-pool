// Package logging настраивает журнал сервиса: log/slog в JSON на stdout.
package logging

import (
	"fmt"
	"io"
	"log/slog"
	"os"
	"strings"
)

// New создаёт JSON-логгер с уровнем level (debug, info, warn, error) и делает его логгером по умолчанию.
func New(level string) (*slog.Logger, error) {
	return NewWithWriter(os.Stdout, level)
}

// NewWithWriter — то же, что New, но с заданным местом вывода.
func NewWithWriter(w io.Writer, level string) (*slog.Logger, error) {
	lvl, err := ParseLevel(level)
	if err != nil {
		return nil, err
	}

	logger := slog.New(slog.NewJSONHandler(w, &slog.HandlerOptions{Level: lvl}))
	slog.SetDefault(logger)

	return logger, nil
}

// ParseLevel разбирает название уровня журнала.
func ParseLevel(level string) (slog.Level, error) {
	var lvl slog.Level
	if err := lvl.UnmarshalText([]byte(strings.TrimSpace(level))); err != nil {
		return 0, fmt.Errorf("unknown log level %q: %w", level, err)
	}

	return lvl, nil
}
