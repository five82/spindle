package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os/signal"
	"strconv"
	"syscall"
	"time"

	"github.com/spf13/cobra"

	"github.com/five82/spindle/internal/daemonctl"
	"github.com/five82/spindle/internal/httpapi"
	"github.com/five82/spindle/internal/logs"
	"github.com/five82/spindle/internal/sockhttp"
)

// logFilter holds CLI filter flags for the logs command.
type logFilter struct {
	Component string
	Lane      string
	Request   string
	ItemID    int64
	Level     string
}

func (f logFilter) hasAny() bool {
	return f.Component != "" || f.Lane != "" || f.Request != "" || f.ItemID != 0 || f.Level != ""
}

func (f logFilter) setParams(params url.Values) {
	if f.Component != "" {
		params.Set("component", f.Component)
	}
	if f.Lane != "" {
		params.Set("lane", f.Lane)
	}
	if f.Request != "" {
		params.Set("request", f.Request)
	}
	if f.ItemID != 0 {
		params.Set("item", strconv.FormatInt(f.ItemID, 10))
	}
	if f.Level != "" {
		params.Set("level", f.Level)
	}
}

func newLogsCmd() *cobra.Command {
	var (
		follow bool
		lines  int
		filter logFilter
	)
	cmd := &cobra.Command{
		Use:   "logs",
		Short: "Display daemon logs",
		RunE: func(_ *cobra.Command, _ []string) error {
			// When filters are set or follow is requested, use the daemon API.
			if filter.hasAny() || follow {
				lp, sp := lockPath(), socketPath()
				if !daemonctl.IsRunning(lp, sp) {
					return fmt.Errorf("daemon is not running (filters require the daemon API)")
				}
				return logsFromAPI(sp, lines, follow, filter)
			}

			// No filters: tail the log file directly.
			logPath := cfg.DaemonLogPath()
			result, err := logs.Tail(context.Background(), logPath, logs.TailOptions{
				Limit: lines,
			})
			if err != nil {
				return fmt.Errorf("read logs: %w", err)
			}
			for _, line := range result.Lines {
				fmt.Println(line)
			}
			return nil
		},
	}
	cmd.Flags().BoolVarP(&follow, "follow", "f", false, "Follow log output")
	cmd.Flags().IntVarP(&lines, "lines", "n", 10, "Number of lines to show")
	cmd.Flags().StringVar(&filter.Component, "component", "", "Filter by component label")
	cmd.Flags().StringVar(&filter.Lane, "lane", "", "Filter by processing lane")
	cmd.Flags().StringVar(&filter.Request, "request", "", "Filter by request/correlation ID")
	cmd.Flags().Int64VarP(&filter.ItemID, "item", "i", 0, "Filter by queue item ID")
	cmd.Flags().StringVar(&filter.Level, "level", "", "Minimum log level (debug, info, warn, error)")
	return cmd
}

// logsFromAPI fetches logs from the daemon HTTP API.
func logsFromAPI(socketPath string, limit int, follow bool, filter logFilter) error {
	client := sockhttp.NewUnixClient(socketPath, 10*time.Second)

	// Build the static portion of the URL once; only "since" varies per poll.
	baseParams := url.Values{}
	filter.setParams(baseParams)
	if limit > 0 {
		baseParams.Set("limit", strconv.Itoa(limit))
	}
	baseParams.Set("tail", "1")
	baseQuery := baseParams.Encode()

	buildURL := func(since uint64) string {
		if since > 0 {
			return "http://localhost/api/logs?" + baseQuery + "&since=" + strconv.FormatUint(since, 10)
		}
		return "http://localhost/api/logs?" + baseQuery
	}

	fetchLogs := func(since uint64) ([]httpapi.LogEntry, uint64, error) {
		req, err := http.NewRequest(http.MethodGet, buildURL(since), nil)
		if err != nil {
			return nil, 0, err
		}
		if cfg != nil {
			sockhttp.SetAuth(req, cfg.API.Token)
		}
		resp, err := client.Do(req)
		if err != nil {
			return nil, 0, err
		}
		defer func() { _ = resp.Body.Close() }()

		body, err := io.ReadAll(resp.Body)
		if err != nil {
			return nil, 0, err
		}

		var result struct {
			Events []httpapi.LogEntry `json:"events"`
			Next   uint64             `json:"next"`
		}
		if err := json.Unmarshal(body, &result); err != nil {
			return nil, 0, err
		}
		return result.Events, result.Next, nil
	}

	events, next, err := fetchLogs(0)
	if err != nil {
		return fmt.Errorf("fetch logs: %w", err)
	}
	for _, e := range events {
		printLogEntry(e)
	}

	if !follow {
		return nil
	}

	ctx, cancel := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer cancel()

	for {
		select {
		case <-ctx.Done():
			return nil
		case <-time.After(1 * time.Second):
		}

		events, next, err = fetchLogs(next)
		if err != nil {
			continue
		}
		for _, e := range events {
			printLogEntry(e)
		}
	}
}

func printLogEntry(e httpapi.LogEntry) {
	fmt.Printf("%s %s %s", e.Time, e.Level, e.Msg)
	if e.Component != "" {
		fmt.Printf(" component=%s", e.Component)
	}
	if e.Lane != "" {
		fmt.Printf(" lane=%s", e.Lane)
	}
	if e.ItemID != 0 {
		fmt.Printf(" item_id=%d", e.ItemID)
	}
	for k, v := range e.Fields {
		fmt.Printf(" %s=%s", k, v)
	}
	fmt.Println()
}
