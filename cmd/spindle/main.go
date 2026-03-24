package main

import (
	"encoding/json"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strconv"

	"github.com/spf13/cobra"

	"github.com/five82/spindle/internal/config"
	"github.com/five82/spindle/internal/queueaccess"
	"github.com/five82/spindle/internal/ripcache"
)

// Global flags.
var (
	flagSocket   string
	flagConfig   string
	flagLogLevel string
	flagVerbose  bool
	flagJSON     bool
)

// cfg holds the loaded configuration (nil for commands that skip config loading).
var cfg *config.Config

func main() {
	root := &cobra.Command{
		Use:   "spindle",
		Short: "Optical disc to Jellyfin media library automation",
		PersistentPreRunE: func(cmd *cobra.Command, _ []string) error {
			if flagVerbose {
				flagLogLevel = "debug"
			}
			// Commands annotated with skipConfigLoad don't need config.
			if cmd.Annotations["skipConfigLoad"] == "true" {
				return nil
			}
			var err error
			cfg, err = config.Load(flagConfig, buildLogger())
			if err != nil {
				return fmt.Errorf("load config: %w", err)
			}
			return nil
		},
		SilenceUsage:  true,
		SilenceErrors: true,
	}

	// Global flags.
	pf := root.PersistentFlags()
	pf.StringVar(&flagSocket, "socket", "", "Path to the daemon Unix socket")
	pf.StringVarP(&flagConfig, "config", "c", "", "Configuration file path")
	pf.StringVar(&flagLogLevel, "log-level", "info", "Log level: debug, info, warn, error")
	pf.BoolVarP(&flagVerbose, "verbose", "v", false, "Shorthand for --log-level=debug")
	pf.BoolVar(&flagJSON, "json", false, "Output in JSON format")

	// Register all command groups.
	root.AddCommand(
		newStartCmd(),
		newStopCmd(),
		newRestartCmd(),
		newStatusCmd(),
		newQueueCmd(),
		newLogsCmd(),
		newIdentifyCmd(),
		newGensubtitleCmd(),
		newTestNotifyCmd(),
		newDiscCmd(),
		newCacheCmd(),
		newConfigCmd(),
		newStagingCmd(),
		newDiscIDCmd(),
		newDebugCmd(),
		newAuditGatherCmd(),
		newDaemonCmd(),
	)

	if err := root.Execute(); err != nil {
		fmt.Fprintf(os.Stderr, "%s %v\n", failStyle("Error:"), err)
		os.Exit(1)
	}
}

// socketPath returns the effective socket path.
func socketPath() string {
	if flagSocket != "" {
		return flagSocket
	}
	if cfg != nil {
		return cfg.SocketPath()
	}
	return ""
}

// lockPath returns the effective lock path.
func lockPath() string {
	if cfg != nil {
		return cfg.LockPath()
	}
	return ""
}

// openQueueAccess opens queue access with HTTP fallback to direct DB.
func openQueueAccess() (queueaccess.Access, error) {
	return queueaccess.OpenWithFallback(socketPath(), cfg.API.Token, cfg.QueueDBPath())
}

// buildLogger creates a structured logger from the global log level flag.
func buildLogger() *slog.Logger {
	level := slog.LevelInfo
	switch flagLogLevel {
	case "debug":
		level = slog.LevelDebug
	case "warn":
		level = slog.LevelWarn
	case "error":
		level = slog.LevelError
	}
	return slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: level}))
}

// resolveTarget resolves a cache entry number or direct file path to a file path.
// If target is a number, looks up the Nth entry in the rip cache and returns the
// first non-metadata file in that cache entry directory.
func resolveTarget(target string) (string, error) {
	if num, err := strconv.Atoi(target); err == nil && num >= 1 {
		rcStore := ripcache.New(cfg.RipCacheDir(), cfg.RipCache.MaxGiB)
		entries, listErr := rcStore.List()
		if listErr != nil {
			return "", listErr
		}
		if num > len(entries) {
			return "", fmt.Errorf("entry %d not found (have %d entries)", num, len(entries))
		}
		entry := entries[num-1]
		entryDir := filepath.Join(cfg.RipCacheDir(), entry.Fingerprint)
		dirEntries, err := os.ReadDir(entryDir)
		if err != nil {
			return "", fmt.Errorf("read cache entry: %w", err)
		}
		for _, de := range dirEntries {
			if !de.IsDir() && de.Name() != "metadata.json" {
				return filepath.Join(entryDir, de.Name()), nil
			}
		}
		return "", fmt.Errorf("no video files in cache entry %d", num)
	}
	if _, err := os.Stat(target); err != nil {
		return "", fmt.Errorf("file not found: %s", target)
	}
	return target, nil
}

// prettyJSON re-indents a JSON string for display. Returns the original string on error.
func prettyJSON(s string) string {
	var v any
	if err := json.Unmarshal([]byte(s), &v); err != nil {
		return s
	}
	data, err := json.MarshalIndent(v, "             ", "  ")
	if err != nil {
		return s
	}
	return string(data)
}

func truncate(s string, maxLen int) string {
	if len(s) <= maxLen {
		return s
	}
	return s[:maxLen-2] + ".."
}

func formatBytes(b int64) string {
	const (
		gib = 1024 * 1024 * 1024
		mib = 1024 * 1024
	)
	switch {
	case b >= gib:
		return fmt.Sprintf("%.1f GiB", float64(b)/float64(gib))
	case b >= mib:
		return fmt.Sprintf("%.1f MiB", float64(b)/float64(mib))
	default:
		return fmt.Sprintf("%d B", b)
	}
}
