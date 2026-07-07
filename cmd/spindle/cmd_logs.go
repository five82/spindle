package main

import (
	"context"
	"fmt"
	"os/signal"
	"syscall"
	"time"

	"github.com/spf13/cobra"

	"github.com/five82/spindle/internal/logs"
	"github.com/five82/spindle/internal/queueaccess"
)

func newLogsCmd() *cobra.Command {
	var (
		follow bool
		lines  int
		query  queueaccess.LogsQuery
	)
	cmd := &cobra.Command{
		Use:   "logs",
		Short: "Display daemon logs",
		RunE: func(_ *cobra.Command, _ []string) error {
			hasFilter := query.Component != "" || query.Lane != "" || query.Request != "" ||
				query.ItemID != 0 || query.Level != ""

			// When filters are set or follow is requested, use the daemon API.
			if hasFilter || follow {
				acc, err := openQueueAccess()
				if err != nil {
					return fmt.Errorf("daemon is not running (filters require the daemon API): %w", err)
				}
				query.Limit = lines
				return logsFromAPI(acc, query, follow)
			}

			// No filters: tail the log file directly.
			logPath := cfg.DaemonLogPath()
			logLines, err := logs.Tail(logPath, lines)
			if err != nil {
				return fmt.Errorf("read logs: %w", err)
			}
			for _, line := range logLines {
				fmt.Println(line)
			}
			return nil
		},
	}
	cmd.Flags().BoolVarP(&follow, "follow", "f", false, "Follow log output")
	cmd.Flags().IntVarP(&lines, "lines", "n", 10, "Number of lines to show")
	cmd.Flags().StringVar(&query.Component, "component", "", "Filter by component label")
	cmd.Flags().StringVar(&query.Lane, "lane", "", "Filter by processing lane")
	cmd.Flags().StringVar(&query.Request, "request", "", "Filter by request/correlation ID")
	cmd.Flags().Int64VarP(&query.ItemID, "item", "i", 0, "Filter by queue item ID")
	cmd.Flags().StringVar(&query.Level, "level", "", "Minimum log level (debug, info, warn, error)")
	return cmd
}

// logsFromAPI fetches logs from the daemon HTTP API.
func logsFromAPI(acc *queueaccess.HTTPAccess, query queueaccess.LogsQuery, follow bool) error {
	query.Tail = true // seed the initial window from the tail
	events, next, err := acc.Logs(query)
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

		query.Since = next
		events, cursor, err := acc.Logs(query)
		if err != nil {
			continue // keep the cursor; retry the same window next tick
		}
		next = cursor
		for _, e := range events {
			printLogEntry(e)
		}
	}
}

func printLogEntry(e queueaccess.LogEntry) {
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
