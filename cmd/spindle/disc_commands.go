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

	cmd.AddCommand(newDiscPauseCommand(ctx), newDiscResumeCommand(ctx))
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
