package main

import (
	"fmt"

	"github.com/spf13/cobra"

	"spindle/internal/ipc"
)

func newDiscCommand(ctx *commandContext) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "disc",
		Short: "Disc detection management",
		RunE: func(cmd *cobra.Command, args []string) error {
			return cmd.Help()
		},
	}

	cmd.AddCommand(
		newDiscPauseCommand(ctx),
		newDiscResumeCommand(ctx),
		newDiscDetectCommand(ctx),
	)
	return cmd
}

func newDiscPauseCommand(ctx *commandContext) *cobra.Command {
	return &cobra.Command{
		Use:   "pause",
		Short: "Pause detection of new disc insertions",
		RunE: func(cmd *cobra.Command, args []string) error {
			return discRPC(ctx, cmd, func(client *ipc.Client) (string, error) {
				resp, err := client.DiscPause()
				if err != nil {
					return "", err
				}
				return resp.Message, nil
			})
		},
	}
}

func newDiscResumeCommand(ctx *commandContext) *cobra.Command {
	return &cobra.Command{
		Use:   "resume",
		Short: "Resume detection of new disc insertions",
		RunE: func(cmd *cobra.Command, args []string) error {
			return discRPC(ctx, cmd, func(client *ipc.Client) (string, error) {
				resp, err := client.DiscResume()
				if err != nil {
					return "", err
				}
				return resp.Message, nil
			})
		},
	}
}

func newDiscDetectCommand(ctx *commandContext) *cobra.Command {
	return &cobra.Command{
		Use:   "detect",
		Short: "Trigger disc detection",
		Long: `Trigger disc detection using the configured optical drive.

This command is useful for testing or when automatic netlink detection
is unavailable. The daemon normally detects discs automatically via
netlink monitoring, so this command is typically only needed for
troubleshooting or manual testing.

If the daemon is not running, this command exits silently.`,
		RunE: func(cmd *cobra.Command, args []string) error {
			client, err := ctx.dialClient()
			if err != nil {
				fmt.Fprintln(cmd.ErrOrStderr(), "spindle daemon not running; disc detection skipped")
				return nil
			}
			defer client.Close()

			resp, err := client.DiscDetect()
			if err != nil {
				fmt.Fprintf(cmd.ErrOrStderr(), "disc detection failed: %v\n", err)
				return nil
			}

			if resp.Handled {
				fmt.Fprintf(cmd.OutOrStdout(), "%s (item %d)\n", resp.Message, resp.ItemID)
			} else {
				fmt.Fprintln(cmd.OutOrStdout(), resp.Message)
			}
			return nil
		},
	}
}

func discRPC(ctx *commandContext, cmd *cobra.Command, fn func(*ipc.Client) (string, error)) error {
	client, err := ctx.dialClient()
	if err != nil {
		return fmt.Errorf("daemon not running: %w", err)
	}
	defer client.Close()

	message, err := fn(client)
	if err != nil {
		return err
	}
	fmt.Fprintln(cmd.OutOrStdout(), message)
	return nil
}
