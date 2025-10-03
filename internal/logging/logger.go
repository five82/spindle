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
	level := normalizeLevel(opts.Level)

	if strings.ToLower(strings.TrimSpace(opts.Format)) == "json" {
		cfg := zap.Config{
			Level:             zap.NewAtomicLevel(),
			Development:       opts.Development,
			DisableCaller:     level != "debug",
			DisableStacktrace: false,
			Sampling:          nil,
			Encoding:          "json",
			OutputPaths:       defaultSlice(opts.OutputPaths, []string{"stdout"}),
			ErrorOutputPaths:  defaultSlice(opts.ErrorOutputPaths, []string{"stderr"}),
			EncoderConfig:     encoderConfig("json"),
		}

		if err := cfg.Level.UnmarshalText([]byte(level)); err != nil {
			return nil, fmt.Errorf("parse log level: %w", err)
		}

		logger, err := cfg.Build()
		if err != nil {
			return nil, fmt.Errorf("build logger: %w", err)
		}
		return logger, nil
	}

	// For console format, use a simplified approach for better readability
	cfg := zap.Config{
		Level:             zap.NewAtomicLevel(),
		Development:       opts.Development,
		DisableCaller:     level != "debug", // Only show caller for debug level
		DisableStacktrace: level != "debug", // Only show stacktrace for debug level
		Sampling:          nil,
		Encoding:          "console",
		OutputPaths:       defaultSlice(opts.OutputPaths, []string{"stdout"}),
		ErrorOutputPaths:  defaultSlice(opts.ErrorOutputPaths, []string{"stderr"}),
		EncoderConfig:     simpleConsoleConfig(),
	}

	if err := cfg.Level.UnmarshalText([]byte(level)); err != nil {
		return nil, fmt.Errorf("parse log level: %w", err)
	}

	logger, err := cfg.Build()
	if err != nil {
		return nil, fmt.Errorf("build logger: %w", err)
	}

	return logger, nil
}

// simpleConsoleConfig returns a clean console encoder configuration
func simpleConsoleConfig() zapcore.EncoderConfig {
	cfg := zap.NewDevelopmentEncoderConfig()
	cfg.EncodeTime = zapcore.ISO8601TimeEncoder
	cfg.EncodeLevel = zapcore.CapitalColorLevelEncoder
	cfg.EncodeDuration = zapcore.StringDurationEncoder
	cfg.EncodeCaller = zapcore.ShortCallerEncoder

	// Remove some noise for cleaner output
	cfg.TimeKey = "time"
	cfg.LevelKey = "level"
	cfg.MessageKey = "msg"
	cfg.CallerKey = "caller"

	return cfg
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

	// Console encoder configuration for human-readable output
	return zapcore.EncoderConfig{
		TimeKey:        "",
		LevelKey:       "",
		NameKey:        "component",
		CallerKey:      "",
		FunctionKey:    zapcore.OmitKey,
		MessageKey:     "msg",
		StacktraceKey:  "stacktrace",
		LineEnding:     zapcore.DefaultLineEnding,
		EncodeLevel:    customLevelEncoder,
		EncodeTime:     zapcore.ISO8601TimeEncoder,
		EncodeDuration: zapcore.StringDurationEncoder,
		EncodeCaller:   zapcore.ShortCallerEncoder,
	}
}

// customLevelEncoder provides cleaner level output for console
func customLevelEncoder(level zapcore.Level, enc zapcore.PrimitiveArrayEncoder) {
	switch level {
	case zapcore.DebugLevel:
		enc.AppendString("[DEBUG] ")
	case zapcore.InfoLevel:
		// No prefix for info level to reduce noise
	case zapcore.WarnLevel:
		enc.AppendString("[WARN] ")
	case zapcore.ErrorLevel:
		enc.AppendString("[ERROR] ")
	case zapcore.FatalLevel:
		enc.AppendString("[FATAL] ")
	case zapcore.PanicLevel:
		enc.AppendString("[PANIC] ")
	default:
		enc.AppendString("[" + level.CapitalString() + "] ")
	}
}
