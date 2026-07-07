package main

import (
	"bufio"
	"encoding/json"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"runtime/debug"
	"strconv"
	"strings"
	"time"

	"github.com/spf13/cobra"
	"golang.org/x/term"

	"github.com/five82/spindle/internal/config"
	"github.com/five82/spindle/internal/queueaccess"
)

// Global flags.
var (
	flagSocket   string
	flagConfig   string
	flagLogLevel string
	flagVerbose  bool
)

// Command group IDs for --help organization.
const (
	groupDaemon      = "daemon"
	groupQueue       = "queue"
	groupDisc        = "disc"
	groupMaintenance = "maintenance"
	groupDiagnostics = "diagnostics"
)

// cfg holds the loaded configuration (nil for commands that skip config loading).
var cfg *config.Config

func main() {
	root := &cobra.Command{
		Use:   "spindle",
		Short: "Optical disc to Jellyfin media library automation",
		Long: `Spindle automates optical disc to Jellyfin library processing:
disc detection, ripping, encoding, metadata, subtitles, and library refresh.

First run: 'spindle config init' to generate a config, then 'spindle start'.`,
		Example: `  spindle start              # launch the daemon
  spindle status             # daemon, dependency, and queue overview
  spindle logs -f --item 3   # follow logs for queue item 3
  spindle queue list         # list queue items`,
		Version: buildVersion(),
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

	// Command groups organize --help output.
	root.AddGroup(
		&cobra.Group{ID: groupDaemon, Title: "Daemon Commands:"},
		&cobra.Group{ID: groupQueue, Title: "Queue Commands:"},
		&cobra.Group{ID: groupDisc, Title: "Disc Commands:"},
		&cobra.Group{ID: groupMaintenance, Title: "Maintenance Commands:"},
		&cobra.Group{ID: groupDiagnostics, Title: "Diagnostic Commands:"},
	)

	// Register all command groups.
	root.AddCommand(
		newStartCmd(),
		newStopCmd(),
		newRestartCmd(),
		newStatusCmd(),
		newQueueCmd(),
		newLogsCmd(),
		newDiscCmd(),
		newCacheCmd(),
		newConfigCmd(),
		newStagingCmd(),
		newDiscIDCmd(),
		newDebugCmd(),
		newDaemonCmd(),
		newEncodeWorkerCmd(),
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

// openQueueAccess opens daemon HTTP queue access.
func openQueueAccess() (*queueaccess.HTTPAccess, error) {
	return queueaccess.OpenHTTP(socketPath(), cfg.API.Token)
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
		entry, err := cacheEntryByNumber(num)
		if err != nil {
			return "", err
		}
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
	runes := []rune(s)
	if len(runes) <= maxLen {
		return s
	}
	return string(runes[:maxLen-2]) + ".."
}

// buildVersion reports the module version plus VCS revision when available.
func buildVersion() string {
	info, ok := debug.ReadBuildInfo()
	if !ok {
		return "unknown"
	}
	version := info.Main.Version
	if version == "" {
		version = "unknown"
	}
	for _, s := range info.Settings {
		if s.Key == "vcs.revision" && len(s.Value) >= 8 {
			return version + " (" + s.Value[:8] + ")"
		}
	}
	return version
}

// confirm gates a destructive action. assumeYes (--yes) skips the prompt;
// a non-interactive stdin without --yes errors instead of hanging.
func confirm(action string, assumeYes bool) error {
	if assumeYes {
		return nil
	}
	if !stdinIsTTY() {
		return fmt.Errorf("confirmation required; re-run with --yes")
	}
	fmt.Printf("%s [y/N]: ", action)
	scanner := bufio.NewScanner(os.Stdin)
	if !scanner.Scan() {
		return fmt.Errorf("aborted")
	}
	switch strings.ToLower(strings.TrimSpace(scanner.Text())) {
	case "y", "yes":
		return nil
	default:
		return fmt.Errorf("aborted")
	}
}

// stdinIsTTY reports whether stdin is an interactive terminal; prompts are
// only allowed when it is.
func stdinIsTTY() bool {
	return term.IsTerminal(int(os.Stdin.Fd()))
}

// relativeAge renders an API timestamp string as a compact age ("2h30m ago").
// Display-only best effort: unparseable values are returned verbatim.
func relativeAge(ts string) string {
	s := strings.TrimSpace(ts)
	t, err := time.Parse(time.RFC3339Nano, s)
	if err != nil {
		t, err = time.ParseInLocation("2006-01-02 15:04:05", s, time.UTC)
	}
	if err != nil {
		return ts
	}
	age := time.Since(t).Truncate(time.Minute)
	if age < time.Minute {
		return "just now"
	}
	return age.String() + " ago"
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
