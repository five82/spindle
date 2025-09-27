package logging

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"

	"spindle/internal/config"
)

// Options describes logger construction parameters.
type Options struct {
	Level            string
	Format           string
	OutputPaths      []string
	ErrorOutputPaths []string
	Development      bool
}

// New constructs a zap logger using the provided options.
func New(opts Options) (*zap.Logger, error) {
	cfg := zap.Config{
		Level:             zap.NewAtomicLevel(),
		Development:       opts.Development,
		DisableCaller:     false,
		DisableStacktrace: false,
		Sampling:          nil,
		Encoding:          sanitizeEncoding(opts.Format),
		OutputPaths:       defaultSlice(opts.OutputPaths, []string{"stdout"}),
		ErrorOutputPaths:  defaultSlice(opts.ErrorOutputPaths, []string{"stderr"}),
		EncoderConfig:     encoderConfig(opts.Format),
	}

	if err := cfg.Level.UnmarshalText([]byte(normalizeLevel(opts.Level))); err != nil {
		return nil, fmt.Errorf("parse log level: %w", err)
	}

	logger, err := cfg.Build()
	if err != nil {
		return nil, fmt.Errorf("build logger: %w", err)
	}

	return logger, nil
}

// NewFromConfig creates a logger using application config defaults.
func NewFromConfig(cfg *config.Config) (*zap.Logger, error) {
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

func sanitizeEncoding(format string) string {
	switch strings.ToLower(strings.TrimSpace(format)) {
	case "json":
		return "json"
	default:
		return "console"
	}
}

func normalizeLevel(level string) string {
	switch strings.ToLower(strings.TrimSpace(level)) {
	case "debug":
		return "debug"
	case "warn":
		return "warn"
	case "error":
		return "error"
	case "dpanic", "panic", "fatal":
		return level
	default:
		return "info"
	}
}

func defaultSlice(value []string, fallback []string) []string {
	if len(value) == 0 {
		// Copy fallback to avoid sharing underlying slice
		cp := make([]string, len(fallback))
		copy(cp, fallback)
		return cp
	}
	cp := make([]string, len(value))
	copy(cp, value)
	return cp
}

func encoderConfig(format string) zapcore.EncoderConfig {
	if strings.ToLower(strings.TrimSpace(format)) == "json" {
		cfg := zap.NewProductionEncoderConfig()
		cfg.EncodeTime = zapcore.RFC3339TimeEncoder
		cfg.TimeKey = "ts"
		cfg.MessageKey = "msg"
		cfg.LevelKey = "level"
		cfg.EncodeLevel = zapcore.LowercaseLevelEncoder
		return cfg
	}

	cfg := zap.NewDevelopmentEncoderConfig()
	cfg.EncodeTime = zapcore.RFC3339TimeEncoder
	cfg.EncodeLevel = zapcore.CapitalColorLevelEncoder
	cfg.TimeKey = "ts"
	cfg.MessageKey = "msg"
	cfg.LevelKey = "level"
	return cfg
}
