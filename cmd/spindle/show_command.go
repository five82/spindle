package main

import (
	"errors"
	"fmt"

	"github.com/spf13/cobra"

	"spindle/internal/ipc"
)

func newShowCommand(ctx *commandContext) *cobra.Command {
	var follow bool
	var lines int

	cmd := &cobra.Command{
		Use:   "show",
		Short: "Display daemon logs",
		RunE: func(cmd *cobra.Command, args []string) error {
			initialLimit := lines
			if initialLimit < 0 {
				initialLimit = 0
			}
			initialOffset := int64(-1)
			if initialLimit == 0 {
				initialOffset = 0
			}

			return ctx.withClient(func(client *ipc.Client) error {
				ctx := cmd.Context()
				offset := initialOffset
				limit := initialLimit
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

	cmd.Flags().BoolVarP(&follow, "follow", "f", false, "Follow log output")
	cmd.Flags().IntVarP(&lines, "lines", "n", 10, "Number of lines to show (0 for all)")
	return cmd
}
