package main

import (
	"fmt"

	"github.com/spf13/cobra"
)

func newDiscCommand(ctx *commandContext) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "disc",
		Short: "Disc detection management",
		RunE: func(cmd *cobra.Command, args []string) error {
			return cmd.Help()
		},
	}

	pauseCmd := &cobra.Command{
		Use:   "pause",
		Short: "Pause detection of new disc insertions",
		RunE: func(cmd *cobra.Command, args []string) error {
			stdout := cmd.OutOrStdout()
			client, err := ctx.dialClient()
			if err != nil {
				return fmt.Errorf("daemon not running: %w", err)
			}
			defer client.Close()

			resp, err := client.DiscPause()
			if err != nil {
				return err
			}
			fmt.Fprintln(stdout, resp.Message)
			return nil
		},
	}

	resumeCmd := &cobra.Command{
		Use:   "resume",
		Short: "Resume detection of new disc insertions",
		RunE: func(cmd *cobra.Command, args []string) error {
			stdout := cmd.OutOrStdout()
			client, err := ctx.dialClient()
			if err != nil {
				return fmt.Errorf("daemon not running: %w", err)
			}
			defer client.Close()

			resp, err := client.DiscResume()
			if err != nil {
				return err
			}
			fmt.Fprintln(stdout, resp.Message)
			return nil
		},
	}

	cmd.AddCommand(pauseCmd, resumeCmd)
	return cmd
}
