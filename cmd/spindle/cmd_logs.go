package main

import (
	"context"
	"fmt"
	"os/signal"
	"syscall"
	"time"

	"github.com/spf13/cobra"

	"github.com/five82/spindle/internal/logs"
)

func newLogsCmd() *cobra.Command {
	var (
		follow bool
		lines  int
	)
	cmd := &cobra.Command{
		Use:   "logs",
		Short: "Display daemon logs",
		RunE: func(_ *cobra.Command, _ []string) error {
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

			if follow {
				ctx, cancel := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
				defer cancel()

				offset := result.Offset
				for {
					select {
					case <-ctx.Done():
						return nil
					case <-time.After(1 * time.Second):
					}

					result, err = logs.Tail(ctx, logPath, logs.TailOptions{
						Offset: offset,
						Limit:  100,
					})
					if err != nil {
						continue
					}
					for _, line := range result.Lines {
						fmt.Println(line)
					}
					offset = result.Offset
				}
			}

			return nil
		},
	}
	cmd.Flags().BoolVarP(&follow, "follow", "f", false, "Follow log output")
	cmd.Flags().IntVarP(&lines, "lines", "n", 10, "Number of lines to show")
	return cmd
}
