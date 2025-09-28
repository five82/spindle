package main

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/spf13/cobra"

	"spindle/internal/config"
	"spindle/internal/ipc"
	"spindle/internal/queue"
	"spindle/internal/services/plex"
)

var manualFileExtensions = map[string]struct{}{
	".mkv": {},
	".mp4": {},
	".avi": {},
}

func main() {
	cmd := newRootCommand()
	if err := cmd.Execute(); err != nil {
		if !errors.Is(err, context.Canceled) {
			fmt.Fprintln(os.Stderr, err)
		}
		os.Exit(1)
	}
}

func newRootCommand() *cobra.Command {
	var socketFlag string
	var configFlag string
	var loadedConfig *config.Config

	rootCmd := &cobra.Command{
		Use:           "spindle",
		Short:         "Spindle Go CLI",
		SilenceUsage:  true,
		SilenceErrors: true,
		PersistentPreRunE: func(cmd *cobra.Command, args []string) error {
			if shouldSkipConfig(cmd) {
				return nil
			}

			if socketFlag == "" {
				socketFlag = defaultSocketPath()
			}
			if loadedConfig == nil {
				cfg, _, _, err := config.Load(configFlag)
				if err != nil {
					return err
				}
				if err := cfg.EnsureDirectories(); err != nil {
					return err
				}
				loadedConfig = cfg
			}
			return nil
		},
		RunE: func(cmd *cobra.Command, args []string) error {
			return cmd.Help()
		},
	}

	rootCmd.PersistentFlags().StringVar(&socketFlag, "socket", "", "Path to the spindle daemon socket")
	rootCmd.PersistentFlags().StringVarP(&configFlag, "config", "c", "", "Configuration file path")

	rootCmd.AddCommand(
		&cobra.Command{
			Use:   "start",
			Short: "Start the spindle daemon",
			RunE: func(cmd *cobra.Command, args []string) error {
				return withClient(socketFlag, func(client *ipc.Client) error {
					resp, err := client.Start()
					if err != nil {
						return err
					}
					if resp.Started {
						fmt.Fprintln(cmd.OutOrStdout(), "Daemon started")
					} else if resp.Message != "" {
						fmt.Fprintln(cmd.OutOrStdout(), resp.Message)
					} else {
						fmt.Fprintln(cmd.OutOrStdout(), "Daemon already running")
					}
					return nil
				})
			},
		},
		&cobra.Command{
			Use:   "stop",
			Short: "Stop the spindle daemon",
			RunE: func(cmd *cobra.Command, args []string) error {
				return withClient(socketFlag, func(client *ipc.Client) error {
					resp, err := client.Stop()
					if err != nil {
						return err
					}
					if resp.Stopped {
						fmt.Fprintln(cmd.OutOrStdout(), "Daemon stopped")
					}
					return nil
				})
			},
		},
		&cobra.Command{
			Use:   "status",
			Short: "Show system and queue status",
			RunE: func(cmd *cobra.Command, args []string) error {
				if loadedConfig == nil {
					return errors.New("configuration not available")
				}

				stdout := cmd.OutOrStdout()
				var statusResp *ipc.StatusResponse
				statusResp = &ipc.StatusResponse{}

				client, err := ipc.Dial(socketFlag)
				if err == nil {
					defer client.Close()
					if resp, statusErr := client.Status(); statusErr == nil {
						statusResp = resp
					}
				} else {
					client = nil
				}

				queueStats := make(map[string]int)
				for k, v := range statusResp.QueueStats {
					queueStats[k] = v
				}

				if client == nil {
					ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
					defer cancel()
					store, openErr := queue.Open(loadedConfig)
					if openErr == nil {
						stats, statsErr := store.Stats(ctx)
						_ = store.Close()
						if statsErr == nil {
							queueStats = make(map[string]int, len(stats))
							for status, count := range stats {
								queueStats[string(status)] = count
							}
						}
					}
				}

				running := statusResp.Running

				fmt.Fprintln(stdout, "System Status")
				if running {
					fmt.Fprintln(stdout, "🟢 Spindle: Running")
				} else {
					fmt.Fprintln(stdout, "🔴 Spindle: Not running")
				}
				fmt.Fprintln(stdout, detectDiscLine(loadedConfig.OpticalDrive))
				if executableAvailable("drapto") {
					fmt.Fprintln(stdout, "⚙️ Drapto: Available")
				} else {
					fmt.Fprintln(stdout, "⚙️ Drapto: Not available")
				}
				fmt.Fprintln(stdout, plexStatusLine(loadedConfig))
				if strings.TrimSpace(loadedConfig.NtfyTopic) != "" {
					fmt.Fprintln(stdout, "📱 Notifications: Configured")
				} else {
					fmt.Fprintln(stdout, "📱 Notifications: Not configured")
				}

				fmt.Fprintln(stdout)
				fmt.Fprintln(stdout, "Queue Status")

				rows := buildQueueStatusRows(queueStats)
				if len(rows) == 0 {
					fmt.Fprintln(stdout, "Queue is empty")
					return nil
				}
				table := renderTable([]string{"Status", "Count"}, rows, []columnAlignment{alignLeft, alignRight})
				fmt.Fprint(stdout, table)
				return nil
			},
		},
	)

	rootCmd.AddCommand(newPlexCommand(func() *config.Config {
		return loadedConfig
	}))

	queueCmd := &cobra.Command{
		Use:   "queue",
		Short: "Inspect and manage the work queue",
	}

	queueStatusCmd := &cobra.Command{
		Use:   "status",
		Short: "Show queue status summary",
		RunE: func(cmd *cobra.Command, args []string) error {
			return withClient(socketFlag, func(client *ipc.Client) error {
				status, err := client.Status()
				if err != nil {
					return err
				}
				rows := buildQueueStatusRows(status.QueueStats)
				if len(rows) == 0 {
					fmt.Fprintln(cmd.OutOrStdout(), "Queue is empty")
					return nil
				}
				table := renderTable([]string{"Status", "Count"}, rows, []columnAlignment{alignLeft, alignRight})
				fmt.Fprint(cmd.OutOrStdout(), table)
				return nil
			})
		},
	}
	queueCmd.AddCommand(queueStatusCmd)

	var listStatuses []string
	listCmd := &cobra.Command{
		Use:   "list",
		Short: "List queue items",
		RunE: func(cmd *cobra.Command, args []string) error {
			return withClient(socketFlag, func(client *ipc.Client) error {
				resp, err := client.QueueList(listStatuses)
				if err != nil {
					return err
				}
				if len(resp.Items) == 0 {
					fmt.Fprintln(cmd.OutOrStdout(), "Queue is empty")
					return nil
				}
				table := renderTable(
					[]string{"ID", "Title", "Status", "Created", "Fingerprint"},
					buildQueueListRows(resp.Items),
					[]columnAlignment{alignRight, alignLeft, alignLeft, alignLeft, alignLeft},
				)
				fmt.Fprint(cmd.OutOrStdout(), table)
				return nil
			})
		},
	}
	listCmd.Flags().StringSliceVarP(&listStatuses, "status", "s", nil, "Filter by queue status (repeatable)")
	queueCmd.AddCommand(listCmd)

	var clearCompleted bool
	var clearFailed bool
	var clearForce bool
	clearCmd := &cobra.Command{
		Use:   "clear",
		Short: "Remove queue items",
		RunE: func(cmd *cobra.Command, args []string) error {
			if clearCompleted && clearFailed {
				return errors.New("specify only one of --completed or --failed")
			}
			return withClient(socketFlag, func(client *ipc.Client) error {
				if clearForce {
					fmt.Fprintln(cmd.OutOrStdout(), "Clearing queue without confirmation (--force)")
				}
				if clearCompleted {
					resp, err := client.QueueClearCompleted()
					if err != nil {
						return err
					}
					fmt.Fprintf(cmd.OutOrStdout(), "Cleared %d completed items\n", resp.Removed)
					return nil
				}
				if clearFailed {
					resp, err := client.QueueClearFailed()
					if err != nil {
						return err
					}
					fmt.Fprintf(cmd.OutOrStdout(), "Cleared %d failed items\n", resp.Removed)
					return nil
				}
				resp, err := client.QueueClear()
				if err != nil {
					return err
				}
				fmt.Fprintf(cmd.OutOrStdout(), "Cleared %d queue items\n", resp.Removed)
				return nil
			})
		},
	}
	clearCmd.Flags().BoolVar(&clearCompleted, "completed", false, "Remove only completed items")
	clearCmd.Flags().BoolVar(&clearFailed, "failed", false, "Remove only failed items")
	clearCmd.Flags().BoolVar(&clearForce, "force", false, "No-op flag for compatibility; removal always proceeds")
	queueCmd.AddCommand(clearCmd)

	queueCmd.AddCommand(&cobra.Command{
		Use:   "clear-failed",
		Short: "Remove failed queue items",
		RunE: func(cmd *cobra.Command, args []string) error {
			return withClient(socketFlag, func(client *ipc.Client) error {
				resp, err := client.QueueClearFailed()
				if err != nil {
					return err
				}
				fmt.Fprintf(cmd.OutOrStdout(), "Cleared %d failed items\n", resp.Removed)
				return nil
			})
		},
	})

	queueCmd.AddCommand(&cobra.Command{
		Use:   "reset-stuck",
		Short: "Return in-flight items to pending",
		RunE: func(cmd *cobra.Command, args []string) error {
			return withClient(socketFlag, func(client *ipc.Client) error {
				resp, err := client.QueueReset()
				if err != nil {
					return err
				}
				fmt.Fprintf(cmd.OutOrStdout(), "Reset %d items\n", resp.Updated)
				return nil
			})
		},
	})

	queueCmd.AddCommand(&cobra.Command{
		Use:   "retry [itemID...]",
		Short: "Retry failed queue items",
		Args:  cobra.ArbitraryArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			ids := make([]int64, 0, len(args))
			for _, arg := range args {
				id, err := strconv.ParseInt(arg, 10, 64)
				if err != nil {
					return fmt.Errorf("invalid item id %q", arg)
				}
				ids = append(ids, id)
			}
			return withClient(socketFlag, func(client *ipc.Client) error {
				out := cmd.OutOrStdout()
				if len(ids) == 0 {
					resp, err := client.QueueRetry(nil)
					if err != nil {
						return err
					}
					fmt.Fprintf(out, "Retried %d failed items\n", resp.Updated)
					return nil
				}

				resp, err := client.QueueList(nil)
				if err != nil {
					return err
				}
				itemsByID := make(map[int64]ipc.QueueItem, len(resp.Items))
				for _, item := range resp.Items {
					itemsByID[item.ID] = item
				}

				for _, id := range ids {
					item, ok := itemsByID[id]
					if !ok {
						fmt.Fprintf(out, "Item %d not found\n", id)
						continue
					}
					if strings.ToLower(strings.TrimSpace(item.Status)) != "failed" {
						fmt.Fprintf(out, "Item %d is not in failed state\n", id)
						continue
					}
					retryResp, retryErr := client.QueueRetry([]int64{id})
					if retryErr != nil {
						return retryErr
					}
					if retryResp.Updated > 0 {
						fmt.Fprintf(out, "Item %d reset for retry\n", id)
					} else {
						fmt.Fprintf(out, "Item %d is not in failed state\n", id)
					}
				}
				return nil
			})
		},
	})

	queueCmd.AddCommand(&cobra.Command{
		Use:   "health",
		Short: "Show queue health summary",
		RunE: func(cmd *cobra.Command, args []string) error {
			return withClient(socketFlag, func(client *ipc.Client) error {
				health, err := client.QueueHealth()
				if err != nil {
					return err
				}
				fmt.Fprintf(cmd.OutOrStdout(), "Total: %d\nPending: %d\nProcessing: %d\nFailed: %d\nReview: %d\nCompleted: %d\n",
					health.Total,
					health.Pending,
					health.Processing,
					health.Failed,
					health.Review,
					health.Completed,
				)
				return nil
			})
		},
	})

	addFileCmd := &cobra.Command{
		Use:   "add-file <path>",
		Short: "Add a video file to the processing queue",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			absPath, err := filepath.Abs(args[0])
			if err != nil {
				return fmt.Errorf("resolve path: %w", err)
			}
			info, err := os.Stat(absPath)
			if err != nil {
				if errors.Is(err, os.ErrNotExist) {
					return fmt.Errorf("file does not exist: %s", absPath)
				}
				return fmt.Errorf("inspect file: %w", err)
			}
			if info.IsDir() {
				return fmt.Errorf("%s is a directory", absPath)
			}
			ext := strings.ToLower(filepath.Ext(info.Name()))
			if _, ok := manualFileExtensions[ext]; !ok {
				return fmt.Errorf("unsupported file extension %q", ext)
			}
			return withClient(socketFlag, func(client *ipc.Client) error {
				resp, err := client.AddFile(absPath)
				if err != nil {
					return err
				}
				if resp == nil {
					return errors.New("empty response from daemon")
				}
				fmt.Fprintf(cmd.OutOrStdout(), "Queued manual file as item #%d (%s)\n", resp.Item.ID, filepath.Base(absPath))
				return nil
			})
		},
	}
	rootCmd.AddCommand(addFileCmd)

	var showFollow bool
	var showLines int
	showCmd := &cobra.Command{
		Use:   "show",
		Short: "Display daemon logs",
		RunE: func(cmd *cobra.Command, args []string) error {
			initialLimit := showLines
			if initialLimit < 0 {
				initialLimit = 0
			}
			initialOffset := int64(-1)
			if initialLimit == 0 {
				initialOffset = 0
			}
			return withClient(socketFlag, func(client *ipc.Client) error {
				ctx := cmd.Context()
				offset := initialOffset
				limit := initialLimit
				follow := showFollow
				waitMillis := 1000
				printed := false
				for {
					req := ipc.LogTailRequest{
						Offset:     offset,
						Limit:      limit,
						Follow:     follow,
						WaitMillis: waitMillis,
					}
					resp, err := client.LogTail(req)
					if err != nil {
						return fmt.Errorf("tail logs: %w", err)
					}
					if resp == nil {
						return errors.New("log tail response missing")
					}
					for _, line := range resp.Lines {
						fmt.Fprintln(cmd.OutOrStdout(), line)
						printed = true
					}
					offset = resp.Offset
					limit = 0
					if !follow {
						if !printed {
							fmt.Fprintln(cmd.OutOrStdout(), "No log entries available")
						}
						return nil
					}
					select {
					case <-ctx.Done():
						return nil
					default:
					}
				}
			})
		},
	}
	queueHealthCmd := &cobra.Command{
		Use:   "queue-health",
		Short: "Check queue database health",
		RunE: func(cmd *cobra.Command, args []string) error {
			return withClient(socketFlag, func(client *ipc.Client) error {
				resp, err := client.DatabaseHealth()
				if err != nil {
					return err
				}
				out := cmd.OutOrStdout()
				fmt.Fprintf(out, "Database path: %s\n", resp.DBPath)
				fmt.Fprintf(out, "Database exists: %s\n", yesNo(resp.DatabaseExists))
				fmt.Fprintf(out, "Readable: %s\n", yesNo(resp.DatabaseReadable))
				fmt.Fprintf(out, "Schema version: %s\n", resp.SchemaVersion)
				fmt.Fprintf(out, "queue_items table present: %s\n", yesNo(resp.TableExists))
				if len(resp.ColumnsPresent) > 0 {
					cols := append([]string(nil), resp.ColumnsPresent...)
					sort.Strings(cols)
					fmt.Fprintf(out, "Columns: %s\n", strings.Join(cols, ", "))
				}
				if len(resp.MissingColumns) > 0 {
					missing := append([]string(nil), resp.MissingColumns...)
					sort.Strings(missing)
					fmt.Fprintf(out, "Missing columns: %s\n", strings.Join(missing, ", "))
				} else {
					fmt.Fprintln(out, "Missing columns: none")
				}
				fmt.Fprintf(out, "Integrity check: %s\n", yesNo(resp.IntegrityCheck))
				fmt.Fprintf(out, "Total items: %d\n", resp.TotalItems)
				if resp.Error != "" {
					fmt.Fprintf(out, "Error: %s\n", resp.Error)
				}
				return nil
			})
		},
	}

	testNotifyCmd := &cobra.Command{
		Use:   "test-notify",
		Short: "Send a test notification",
		RunE: func(cmd *cobra.Command, args []string) error {
			return withClient(socketFlag, func(client *ipc.Client) error {
				resp, err := client.TestNotification()
				if err != nil {
					if resp != nil && resp.Message != "" {
						fmt.Fprintln(cmd.OutOrStdout(), resp.Message)
					}
					return err
				}
				if resp == nil {
					return errors.New("missing notification response")
				}
				if resp.Message != "" {
					fmt.Fprintln(cmd.OutOrStdout(), resp.Message)
				} else if resp.Sent {
					fmt.Fprintln(cmd.OutOrStdout(), "Test notification sent")
				} else {
					fmt.Fprintln(cmd.OutOrStdout(), "Notification not sent")
				}
				return nil
			})
		},
	}

	showCmd.Flags().BoolVarP(&showFollow, "follow", "f", false, "Follow log output")
	showCmd.Flags().IntVarP(&showLines, "lines", "n", 10, "Number of lines to show (0 for all)")
	configCmd := &cobra.Command{
		Use:   "config",
		Short: "Configuration utilities",
	}

	var configInitPath string
	var configInitOverwrite bool

	configInitCmd := &cobra.Command{
		Use:         "init",
		Short:       "Create a sample configuration file",
		Annotations: map[string]string{"skipConfigLoad": "true"},
		RunE: func(cmd *cobra.Command, args []string) error {
			target := configInitPath
			if strings.TrimSpace(target) == "" {
				defaultPath, err := config.DefaultConfigPath()
				if err != nil {
					return fmt.Errorf("determine default config path: %w", err)
				}
				target = defaultPath
			} else {
				expanded, err := config.ExpandPath(target)
				if err != nil {
					return fmt.Errorf("resolve config path: %w", err)
				}
				target = expanded
			}

			dir := filepath.Dir(target)
			if err := os.MkdirAll(dir, 0o755); err != nil {
				return fmt.Errorf("create config directory %q: %w", dir, err)
			}

			if !configInitOverwrite {
				if _, err := os.Stat(target); err == nil {
					return fmt.Errorf("config file already exists at %s (use --overwrite to replace it)", target)
				} else if err != nil && !os.IsNotExist(err) {
					return fmt.Errorf("check config path: %w", err)
				}
			}

			if err := config.CreateSample(target); err != nil {
				return fmt.Errorf("create sample config: %w", err)
			}

			out := cmd.OutOrStdout()
			fmt.Fprintf(out, "Wrote sample configuration to %s\n", target)
			fmt.Fprintln(out, "Edit the file to set tmdb_api_key (or export TMDB_API_KEY) before running Spindle.")
			return nil
		},
	}

	configInitCmd.Flags().StringVarP(&configInitPath, "path", "p", "", "Destination for the configuration file")
	configInitCmd.Flags().BoolVar(&configInitOverwrite, "overwrite", false, "Overwrite existing configuration if present")

	configValidateCmd := &cobra.Command{
		Use:   "validate",
		Short: "Validate configuration file",
		RunE: func(cmd *cobra.Command, args []string) error {
			cfg, path, exists, err := config.Load("")
			if err != nil {
				return fmt.Errorf("load config: %w", err)
			}
			if err := cfg.EnsureDirectories(); err != nil {
				return fmt.Errorf("ensure directories: %w", err)
			}
			out := cmd.OutOrStdout()
			fmt.Fprintf(out, "Config path: %s\n", path)
			if !exists {
				fmt.Fprintln(out, "Config file did not exist; defaults were used")
			}
			fmt.Fprintln(out, "Configuration valid")
			return nil
		},
	}
	configCmd.AddCommand(configValidateCmd)
	configCmd.AddCommand(configInitCmd)

	rootCmd.AddCommand(showCmd)
	rootCmd.AddCommand(queueCmd)
	rootCmd.AddCommand(queueHealthCmd)
	rootCmd.AddCommand(testNotifyCmd)
	rootCmd.AddCommand(configCmd)

	return rootCmd
}

func plexStatusLine(cfg *config.Config) string {
	if cfg == nil {
		return "📚 Plex: Unknown"
	}
	if strings.TrimSpace(cfg.PlexURL) == "" {
		return "📚 Plex: Not configured or unreachable"
	}
	if !cfg.PlexLinkEnabled {
		return "📚 Plex: Link disabled"
	}
	manager, err := plex.NewTokenManager(cfg)
	if err != nil {
		return fmt.Sprintf("📚 Plex: Auth error (%v)", err)
	}
	if manager.HasAuthorization() {
		return "📚 Plex: Linked"
	}
	return "📚 Plex: Link required (run spindle plex link)"
}

func shouldSkipConfig(cmd *cobra.Command) bool {
	for c := cmd; c != nil; c = c.Parent() {
		if c.Annotations != nil && c.Annotations["skipConfigLoad"] == "true" {
			return true
		}
	}
	return false
}

func defaultSocketPath() string {
	cfg, _, _, err := config.Load("")
	if err == nil {
		return filepath.Join(cfg.LogDir, "spindle.sock")
	}

	logDir, err2 := config.ExpandPath("~/.local/share/spindle/logs")
	if err2 != nil {
		return filepath.Join(os.TempDir(), "spindle.sock")
	}
	return filepath.Join(logDir, "spindle.sock")
}

func withClient(socket string, fn func(*ipc.Client) error) error {
	client, err := ipc.Dial(socket)
	if err != nil {
		return fmt.Errorf("connect to daemon: %w", err)
	}
	defer client.Close()
	return fn(client)
}

func yesNo(value bool) string {
	if value {
		return "yes"
	}
	return "no"
}

func formatStatusLabel(status string) string {
	status = strings.TrimSpace(status)
	if status == "" {
		return ""
	}
	parts := strings.Split(status, "_")
	for i, part := range parts {
		lower := strings.ToLower(part)
		if lower == "" {
			continue
		}
		parts[i] = strings.ToUpper(lower[:1]) + lower[1:]
	}
	return strings.Join(parts, " ")
}

type columnAlignment int

const (
	alignLeft columnAlignment = iota
	alignRight
)

func buildQueueStatusRows(stats map[string]int) [][]string {
	if len(stats) == 0 {
		return nil
	}
	keys := make([]string, 0, len(stats))
	for key := range stats {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	rows := make([][]string, 0, len(keys))
	for _, key := range keys {
		rows = append(rows, []string{formatStatusLabel(key), fmt.Sprintf("%d", stats[key])})
	}
	return rows
}

func buildQueueListRows(items []ipc.QueueItem) [][]string {
	if len(items) == 0 {
		return nil
	}
	sorted := make([]ipc.QueueItem, len(items))
	copy(sorted, items)
	sort.Slice(sorted, func(i, j int) bool {
		ti := parseQueueTime(sorted[i].CreatedAt)
		tj := parseQueueTime(sorted[j].CreatedAt)
		if ti.Equal(tj) {
			return sorted[i].ID > sorted[j].ID
		}
		return ti.After(tj)
	})

	rows := make([][]string, 0, len(sorted))
	for _, item := range sorted {
		title := strings.TrimSpace(item.DiscTitle)
		if title == "" {
			source := strings.TrimSpace(item.SourcePath)
			if source != "" {
				title = filepath.Base(source)
			} else {
				title = "Unknown"
			}
		}
		status := formatStatusLabel(item.Status)
		created := formatDisplayTime(item.CreatedAt)
		fingerprint := formatFingerprint(item.DiscFingerprint)
		rows = append(rows, []string{
			fmt.Sprintf("%d", item.ID),
			title,
			status,
			created,
			fingerprint,
		})
	}
	return rows
}

func formatDisplayTime(value string) string {
	value = strings.TrimSpace(value)
	if value == "" {
		return ""
	}
	if t, err := time.Parse(time.RFC3339, value); err == nil {
		return t.UTC().Format("2006-01-02 15:04")
	}
	if t, err := time.Parse(time.RFC3339Nano, value); err == nil {
		return t.UTC().Format("2006-01-02 15:04")
	}
	return value
}

func parseQueueTime(value string) time.Time {
	if value == "" {
		return time.Time{}
	}
	if t, err := time.Parse(time.RFC3339, value); err == nil {
		return t
	}
	if t, err := time.Parse(time.RFC3339Nano, value); err == nil {
		return t
	}
	return time.Time{}
}

func formatFingerprint(value string) string {
	value = strings.TrimSpace(value)
	if value == "" {
		return "-"
	}
	if len(value) > 12 {
		return value[:12]
	}
	return value
}

func renderTable(headers []string, rows [][]string, aligns []columnAlignment) string {
	columns := len(headers)
	if columns == 0 {
		return ""
	}
	colWidths := make([]int, columns)
	for i := 0; i < columns; i++ {
		maxLen := utf8.RuneCountInString(headers[i])
		for _, row := range rows {
			if i >= len(row) {
				continue
			}
			length := utf8.RuneCountInString(row[i])
			if length > maxLen {
				maxLen = length
			}
		}
		colWidths[i] = maxLen + 2
	}

	var b strings.Builder
	writeBorder(&b, '┏', '┳', '┓', '━', colWidths)
	writeRow(&b, '┃', headers, colWidths, aligns, true)
	writeBorder(&b, '┡', '╇', '┩', '━', colWidths)
	for _, row := range rows {
		writeRow(&b, '│', row, colWidths, aligns, false)
	}
	writeBorder(&b, '└', '┴', '┘', '─', colWidths)
	return b.String()
}

func writeBorder(b *strings.Builder, left, mid, right, fill rune, widths []int) {
	b.WriteRune(left)
	for i, width := range widths {
		if width < 1 {
			width = 1
		}
		b.WriteString(strings.Repeat(string(fill), width))
		if i == len(widths)-1 {
			b.WriteRune(right)
			b.WriteRune('\n')
		} else {
			b.WriteRune(mid)
		}
	}
}

func writeRow(b *strings.Builder, edge rune, cells []string, widths []int, aligns []columnAlignment, header bool) {
	b.WriteRune(edge)
	for i, width := range widths {
		content := ""
		if i < len(cells) {
			content = cells[i]
		}
		textWidth := utf8.RuneCountInString(content)
		padWidth := width - 2
		if padWidth < 0 {
			padWidth = 0
		}
		if textWidth > padWidth {
			textWidth = padWidth
		}

		leftPad := 1
		rightPad := padWidth - textWidth + 1
		alignment := alignLeft
		if i < len(aligns) {
			alignment = aligns[i]
		}
		if !header && alignment == alignRight {
			leftPad = padWidth - textWidth + 1
			rightPad = 1
		}
		if leftPad < 1 {
			leftPad = 1
		}
		if rightPad < 1 {
			rightPad = 1
		}

		b.WriteString(strings.Repeat(" ", leftPad))
		if textWidth < utf8.RuneCountInString(content) {
			runes := []rune(content)
			b.WriteString(string(runes[:padWidth]))
		} else {
			b.WriteString(content)
		}
		b.WriteString(strings.Repeat(" ", rightPad))
		if i == len(widths)-1 {
			b.WriteRune(edge)
			b.WriteRune('\n')
		} else {
			b.WriteRune(edge)
		}
	}
}

func detectDiscLine(device string) string {
	device = strings.TrimSpace(device)
	if device == "" {
		device = "/dev/sr0"
	}
	if _, err := exec.LookPath("lsblk"); err != nil {
		return "📀 Disc: No disc detected"
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, "lsblk", "-no", "LABEL,FSTYPE", device)
	output, err := cmd.Output()
	if err != nil {
		return "📀 Disc: No disc detected"
	}
	text := strings.TrimSpace(string(output))
	if text == "" {
		return "📀 Disc: No disc detected"
	}
	fields := strings.Fields(text)
	label := "Unknown"
	if len(fields) > 0 && fields[0] != "" {
		label = fields[0]
	}
	fstype := ""
	if len(fields) > 1 {
		fstype = strings.ToLower(fields[1])
	}
	discType := classifyDiscType(device, fstype)
	return fmt.Sprintf("📀 Disc: %s disc '%s' on %s", discType, label, device)
}

func classifyDiscType(device, fstype string) string {
	switch strings.ToLower(strings.TrimSpace(fstype)) {
	case "udf":
		return "Blu-ray"
	case "iso9660":
		return "DVD"
	default:
		_ = device
		return "Unknown"
	}
}

func executableAvailable(name string) bool {
	_, err := exec.LookPath(name)
	return err == nil
}
