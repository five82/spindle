// Package logging provides file logging for reel CLI.
package logging

import (
	"fmt"
	"io"
	"log"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// DefaultLogDir returns the default log directory following XDG Base Directory Spec.
// Uses $XDG_STATE_HOME/reel/logs, defaulting to ~/.local/state/reel/logs.
func DefaultLogDir() string {
	if dir := os.Getenv("XDG_STATE_HOME"); dir != "" {
		return filepath.Join(dir, "reel", "logs")
	}
	home, err := os.UserHomeDir()
	if err != nil {
		// Fallback to current directory if home can't be determined
		return filepath.Join(".", "reel", "logs")
	}
	return filepath.Join(home, ".local", "state", "reel", "logs")
}

// level represents the logging level.
type level int

const (
	levelInfo level = iota
	levelDebug
)

// Logger wraps the standard logger with level filtering and file output.
type Logger struct {
	level    level
	logger   *log.Logger
	file     *os.File
	filePath string
}

// Setup creates a new logger that writes to a timestamped log file.
// Returns nil if logging is disabled (noLog=true).
// cmdArgs should be os.Args to log the command that was run.
func Setup(logDir string, verbose, noLog bool, cmdArgs []string) (*Logger, error) {
	if noLog {
		return nil, nil
	}

	// Create log directory
	if err := os.MkdirAll(logDir, 0755); err != nil {
		return nil, fmt.Errorf("failed to create log directory %s: %w", logDir, err)
	}

	// Generate timestamped filename
	timestamp := time.Now().Format("20060102_150405")
	filename := fmt.Sprintf("reel_encode_run_%s.log", timestamp)
	filePath := filepath.Join(logDir, filename)

	// Open log file
	file, err := os.OpenFile(filePath, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0644)
	if err != nil {
		return nil, fmt.Errorf("failed to create log file %s: %w", filePath, err)
	}

	level := levelInfo
	if verbose {
		level = levelDebug
	}

	logger := log.New(file, "", 0) // No flags - we add timestamps manually for consistent format

	l := &Logger{
		level:    level,
		logger:   logger,
		file:     file,
		filePath: filePath,
	}

	// Log startup
	l.Info("Command: %s", strings.Join(cmdArgs, " "))
	l.Info("Reel encoder starting")
	if verbose {
		l.Info("Debug level logging enabled")
	}
	l.Info("Log file: %s", filePath)

	return l, nil
}

// FilePath returns the path to the active log file.
func (l *Logger) FilePath() string {
	if l == nil {
		return ""
	}
	return l.filePath
}

// Close closes the log file.
func (l *Logger) Close() error {
	if l == nil || l.file == nil {
		return nil
	}
	return l.file.Close()
}

// Info logs an info-level message.
func (l *Logger) Info(format string, args ...any) {
	if l == nil {
		return
	}
	timestamp := time.Now().Format("2006-01-02 15:04:05")
	l.logger.Printf("%s [INFO] "+format, append([]any{timestamp}, args...)...)
}

// Debug logs a debug-level message (only if verbose mode is enabled).
func (l *Logger) Debug(format string, args ...any) {
	if l == nil || l.level < levelDebug {
		return
	}
	timestamp := time.Now().Format("2006-01-02 15:04:05")
	l.logger.Printf("%s [DEBUG] "+format, append([]any{timestamp}, args...)...)
}

// Writer returns an io.Writer that writes to the log file.
// Useful for redirecting other loggers or capturing output.
func (l *Logger) Writer() io.Writer {
	if l == nil || l.file == nil {
		return io.Discard
	}
	return l.file
}
