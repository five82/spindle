package logging

import (
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// RetentionTarget specifies a directory and filename pattern to prune.
type RetentionTarget struct {
	Dir     string
	Pattern string
	Exclude []string
}

// CleanupOldLogs removes files matching the provided targets that are older
// than retentionDays. A retentionDays value of 0 disables pruning.
func CleanupOldLogs(logger *slog.Logger, retentionDays int, targets ...RetentionTarget) {
	if retentionDays <= 0 {
		return
	}
	cutoff := time.Now().AddDate(0, 0, -retentionDays)

	exclusions := make(map[string]struct{})
	for _, target := range targets {
		for _, path := range target.Exclude {
			if trimmed := strings.TrimSpace(path); trimmed != "" {
				if abs, err := filepath.Abs(trimmed); err == nil {
					exclusions[abs] = struct{}{}
				}
			}
		}
	}

	for _, target := range targets {
		dir := strings.TrimSpace(target.Dir)
		if dir == "" {
			continue
		}
		entries, err := os.ReadDir(dir)
		if err != nil {
			continue
		}
		for _, entry := range entries {
			if entry.IsDir() {
				continue
			}
			name := entry.Name()
			if pat := strings.TrimSpace(target.Pattern); pat != "" {
				matched, err := filepath.Match(pat, name)
				if err != nil || !matched {
					continue
				}
			}
			fullPath := filepath.Join(dir, name)
			absPath, err := filepath.Abs(fullPath)
			if err == nil {
				fullPath = absPath
			}
			if _, skip := exclusions[fullPath]; skip {
				continue
			}
			info, err := entry.Info()
			if err != nil {
				continue
			}
			if !info.ModTime().Before(cutoff) {
				continue
			}
			if err := os.Remove(fullPath); err != nil {
				WarnWithContext(logger, "log retention remove failed; file remains", "log_retention_failed",
					String("path", fullPath),
					Error(err),
					String(FieldErrorHint, "check file permissions and log_dir ownership"),
					String(FieldImpact, "old log file remains on disk"),
				)
				continue
			}
			if logger != nil {
				logger.Info("log pruned",
					String("path", fullPath),
					String(FieldEventType, "log_pruned"),
				)
			}
		}
	}
}
