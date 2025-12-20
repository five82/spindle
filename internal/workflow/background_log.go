package workflow

import (
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"time"
	"unicode"

	"spindle/internal/config"
	"spindle/internal/logging"
	"spindle/internal/queue"
)

// BackgroundLogger manages dedicated log files for background processing lanes.
type BackgroundLogger struct {
	baseDir string
	hub     *logging.StreamHub
	cfg     *config.Config
}

// NewBackgroundLogger creates a new background logger.
func NewBackgroundLogger(cfg *config.Config, hub *logging.StreamHub) *BackgroundLogger {
	dir := ""
	if cfg != nil && cfg.Paths.LogDir != "" {
		dir = filepath.Join(cfg.Paths.LogDir, "background")
	}
	return &BackgroundLogger{
		baseDir: dir,
		hub:     hub,
		cfg:     cfg,
	}
}

// Ensure prepares the log directory and file path for an item.
func (b *BackgroundLogger) Ensure(item *queue.Item) (string, bool, error) {
	if item == nil {
		return "", false, fmt.Errorf("queue item is nil")
	}
	if strings.TrimSpace(b.baseDir) == "" {
		return "", false, fmt.Errorf("background log directory not configured")
	}
	created := false
	if strings.TrimSpace(item.BackgroundLogPath) == "" {
		filename := b.filename(item)
		if filename == "" {
			filename = fmt.Sprintf("item-%d.log", item.ID)
		}
		item.BackgroundLogPath = filepath.Join(b.baseDir, filename)
		created = true
	}
	if err := os.MkdirAll(filepath.Dir(item.BackgroundLogPath), 0o755); err != nil {
		return "", false, fmt.Errorf("ensure background log directory: %w", err)
	}
	return item.BackgroundLogPath, created, nil
}

// CreateHandler builds a slog.Handler writing to the specified path.
func (b *BackgroundLogger) CreateHandler(path string) (slog.Handler, error) {
	level := "info"
	format := "json"
	if b.cfg != nil {
		if strings.TrimSpace(b.cfg.Logging.Level) != "" {
			level = b.cfg.Logging.Level
		}
		if strings.TrimSpace(b.cfg.Logging.Format) != "" {
			format = b.cfg.Logging.Format
		}
	}
	logger, err := logging.New(logging.Options{
		Level:            level,
		Format:           format,
		OutputPaths:      []string{path},
		ErrorOutputPaths: []string{path},
		Development:      false,
		// Background logs write to item files, but still publish to the daemon stream so
		// users can observe per-item/episode progress via the log API and `spindle show --lane background --item <id>`.
		Stream: b.hub,
	})
	if err != nil {
		return nil, err
	}
	return logger.Handler(), nil
}

func (b *BackgroundLogger) filename(item *queue.Item) string {
	timestamp := time.Now().UTC().Format("20060102T150405")
	fingerprint := strings.TrimSpace(item.DiscFingerprint)
	if fingerprint == "" {
		fingerprint = fmt.Sprintf("item-%d", item.ID)
	}
	title := sanitizeSlug(item.DiscTitle)
	if title == "" {
		title = "untitled"
	}
	return fmt.Sprintf("%s-%s-%s.log", timestamp, fingerprint, title)
}

func sanitizeSlug(value string) string {
	value = strings.TrimSpace(value)
	if value == "" {
		return ""
	}
	var builder strings.Builder
	builder.Grow(len(value))
	lastDash := false
	for _, r := range value {
		switch {
		case r >= 'a' && r <= 'z', r >= '0' && r <= '9':
			builder.WriteRune(r)
			lastDash = false
		case r >= 'A' && r <= 'Z':
			builder.WriteRune(unicode.ToLower(r))
			lastDash = false
		case unicode.IsDigit(r):
			builder.WriteRune(r)
			lastDash = false
		default:
			if !lastDash {
				builder.WriteByte('-')
				lastDash = true
			}
		}
	}
	slug := strings.Trim(builder.String(), "-")
	if slug == "" {
		return ""
	}
	return slug
}
