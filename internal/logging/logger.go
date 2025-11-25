package logging

import (
	"fmt"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"strings"

	"spindle/internal/config"
)

// Options describes logger construction parameters.
type Options struct {
	Level            string
	Format           string
	OutputPaths      []string
	ErrorOutputPaths []string
	Development      bool
	Stream           *StreamHub
}

// New constructs a slog logger using the provided options.
func New(opts Options) (*slog.Logger, error) {
	level := parseLevel(opts.Level)
	levelVar := new(slog.LevelVar)
	levelVar.Set(level)

	outputWriter, err := openWriters(
		defaultSlice(opts.OutputPaths, []string{"stdout"}),
		defaultSlice(opts.ErrorOutputPaths, []string{"stderr"}),
	)
	if err != nil {
		return nil, err
	}

	addSource := opts.Development || level <= slog.LevelDebug

	format := strings.ToLower(strings.TrimSpace(opts.Format))
	if format == "" {
		format = "console"
	}

	var handler slog.Handler
	switch format {
	case "json":
		handler, err = newJSONHandler(outputWriter, levelVar, addSource)
		if err != nil {
			return nil, err
		}
	case "console":
		handler = newPrettyHandler(outputWriter, levelVar, addSource)
	default:
		return nil, fmt.Errorf("log format: unsupported value %q", opts.Format)
	}

	if opts.Stream != nil {
		handler = newStreamHandler(handler, opts.Stream)
	}

	return slog.New(handler), nil
}

// NewFromConfig creates a logger using application config defaults.
func NewFromConfig(cfg *config.Config) (*slog.Logger, error) {
	if cfg == nil {
		return New(Options{Level: "info", Format: "console", OutputPaths: []string{"stdout"}, ErrorOutputPaths: []string{"stderr"}})
	}

	outputPaths := []string{"stdout"}
	errorOutputs := []string{"stderr"}
	if cfg.LogDir != "" {
		if err := os.MkdirAll(cfg.LogDir, 0o755); err != nil {
			return nil, fmt.Errorf("ensure log directory: %w", err)
		}
		logPath := filepath.Join(cfg.LogDir, "spindle.log")
		outputPaths = append(outputPaths, logPath)
		errorOutputs = append(errorOutputs, logPath)
	}

	opts := Options{
		Level:            cfg.LogLevel,
		Format:           cfg.LogFormat,
		OutputPaths:      outputPaths,
		ErrorOutputPaths: errorOutputs,
		Development:      false,
	}
	return New(opts)
}

func parseLevel(level string) slog.Level {
	switch strings.ToLower(strings.TrimSpace(level)) {
	case "debug":
		return slog.LevelDebug
	case "warn":
		return slog.LevelWarn
	case "error":
		return slog.LevelError
	case "dpanic", "panic", "fatal": // map to error semantics
		return slog.LevelError
	case "info", "":
		return slog.LevelInfo
	default:
		return slog.LevelInfo
	}
}

func defaultSlice(value []string, fallback []string) []string {
	if len(value) == 0 {
		cp := make([]string, len(fallback))
		copy(cp, fallback)
		return cp
	}
	cp := make([]string, len(value))
	copy(cp, value)
	return cp
}

func openWriters(outputPaths []string, errorPaths []string) (io.Writer, error) {
	type writerEntry struct {
		key    string
		writer io.Writer
	}

	seen := map[string]struct{}{}
	var entries []writerEntry
	var hasStdout bool
	var hasStderr bool

	combined := append([]string{}, outputPaths...)
	combined = append(combined, errorPaths...)

	for _, path := range combined {
		trimmed := strings.TrimSpace(path)
		if trimmed == "" {
			continue
		}
		if _, ok := seen[trimmed]; ok {
			continue
		}
		seen[trimmed] = struct{}{}

		switch trimmed {
		case "stdout":
			hasStdout = true
			entries = append(entries, writerEntry{key: "stdout", writer: os.Stdout})
		case "stderr":
			hasStderr = true
			entries = append(entries, writerEntry{key: "stderr", writer: os.Stderr})
		default:
			if err := ensureLogDir(trimmed); err != nil {
				return nil, err
			}
			file, err := os.OpenFile(trimmed, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o664)
			if err != nil {
				return nil, fmt.Errorf("open log file %s: %w", trimmed, err)
			}
			entries = append(entries, writerEntry{key: trimmed, writer: file})
		}
	}

	if hasStdout && hasStderr {
		filtered := entries[:0]
		for _, entry := range entries {
			if entry.key == "stderr" {
				continue
			}
			filtered = append(filtered, entry)
		}
		entries = filtered
	}

	if len(entries) == 0 {
		return os.Stdout, nil
	}
	if len(entries) == 1 {
		return entries[0].writer, nil
	}

	writers := make([]io.Writer, 0, len(entries))
	for _, entry := range entries {
		writers = append(writers, entry.writer)
	}
	return io.MultiWriter(writers...), nil
}

func ensureLogDir(path string) error {
	dir := filepath.Dir(path)
	if dir == "." || dir == "" {
		return nil
	}
	return os.MkdirAll(dir, 0o755)
}
