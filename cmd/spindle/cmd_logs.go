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

func newLogsCmd() *cobra.Command {
	var (
		follow    bool
		lines     int
		component string
		lane      string
		request   string
		itemID    int64
		level     string
	)
	cmd := &cobra.Command{
		Use:   "logs",
		Short: "Display daemon logs",
		RunE: func(_ *cobra.Command, _ []string) error {
			hasFilters := component != "" || lane != "" || request != "" || itemID != 0 || level != ""

			// When filters are set or follow is requested, use the daemon API.
			if hasFilters || follow {
				lp, sp := lockPath(), socketPath()
				if !daemonctl.IsRunning(lp, sp) {
					return fmt.Errorf("daemon is not running (filters require the daemon API)")
				}
				return logsFromAPI(sp, lines, follow, component, lane, request, itemID, level)
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
	cmd.Flags().StringVar(&component, "component", "", "Filter by component label")
	cmd.Flags().StringVar(&lane, "lane", "", "Filter by processing lane")
	cmd.Flags().StringVar(&request, "request", "", "Filter by request/correlation ID")
	cmd.Flags().Int64VarP(&itemID, "item", "i", 0, "Filter by queue item ID")
	cmd.Flags().StringVar(&level, "level", "", "Minimum log level (debug, info, warn, error)")
	return cmd
}

// logsFromAPI fetches logs from the daemon HTTP API.
func logsFromAPI(socketPath string, limit int, follow bool, component, lane, request string, itemID int64, level string) error {
	client := sockhttp.NewUnixClient(socketPath, 10*time.Second)

	buildURL := func(since uint64) string {
		params := url.Values{}
		if component != "" {
			params.Set("component", component)
		}
		if lane != "" {
			params.Set("lane", lane)
		}
		if request != "" {
			params.Set("request", request)
		}
		if itemID != 0 {
			params.Set("item", strconv.FormatInt(itemID, 10))
		}
		if level != "" {
			params.Set("level", level)
		}
		if limit > 0 {
			params.Set("limit", strconv.Itoa(limit))
		}
		params.Set("tail", "1")
		if since > 0 {
			params.Set("since", strconv.FormatUint(since, 10))
		}
		return "http://localhost/api/logs?" + params.Encode()
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
