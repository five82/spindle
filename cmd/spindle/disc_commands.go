package main

import (
	"fmt"

	"github.com/spf13/cobra"

	"spindle/internal/disc"
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
		newDiscStatusCommand(ctx),
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
				if ctx.JSONMode() {
					return writeJSON(cmd, map[string]any{
						"handled": false,
						"message": "spindle daemon not running; disc detection skipped",
					})
				}
				fmt.Fprintln(cmd.ErrOrStderr(), "spindle daemon not running; disc detection skipped")
				return nil
			}
			defer client.Close()

			resp, err := client.DiscDetect()
			if err != nil {
				if ctx.JSONMode() {
					return writeJSON(cmd, map[string]any{
						"handled": false,
						"message": fmt.Sprintf("disc detection failed: %v", err),
					})
				}
				fmt.Fprintf(cmd.ErrOrStderr(), "disc detection failed: %v\n", err)
				return nil
			}

			if ctx.JSONMode() {
				return writeJSON(cmd, resp)
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

func newDiscStatusCommand(ctx *commandContext) *cobra.Command {
	return &cobra.Command{
		Use:   "status",
		Short: "Check optical drive readiness",
		Long: `Query the optical drive hardware status using ioctl.

Reports whether the drive has a disc loaded, tray is open, etc.
Runs directly against the hardware (no daemon required).

Requires a raw device path (/dev/srN) in makemkv.optical_drive config.
The disc:N format does not support hardware status checks.`,
		RunE: func(cmd *cobra.Command, args []string) error {
			cfg, err := ctx.ensureConfig()
			if err != nil {
				return fmt.Errorf("load config: %w", err)
			}

			devicePath := disc.ExtractDevicePath(cfg.MakeMKV.OpticalDrive)
			if devicePath == "" {
				if ctx.JSONMode() {
					return writeJSON(cmd, map[string]any{
						"error":  "drive check requires a /dev/srN device path, not disc:N format",
						"device": cfg.MakeMKV.OpticalDrive,
					})
				}
				return fmt.Errorf("drive check requires a /dev/srN device path; configured: %s", cfg.MakeMKV.OpticalDrive)
			}

			status, err := disc.CheckDriveStatus(devicePath)
			if err != nil {
				if ctx.JSONMode() {
					return writeJSON(cmd, map[string]any{
						"device": devicePath,
						"error":  err.Error(),
					})
				}
				return fmt.Errorf("check drive status: %w", err)
			}

			ready := status == disc.DriveStatusDiscOK
			if ctx.JSONMode() {
				return writeJSON(cmd, map[string]any{
					"device": devicePath,
					"status": status.String(),
					"ready":  ready,
				})
			}

			fmt.Fprintf(cmd.OutOrStdout(), "%s: %s\n", devicePath, status.String())
			if !ready {
				// Use SilenceUsage to avoid printing usage on non-zero exit
				cmd.SilenceUsage = true
				return fmt.Errorf("drive not ready")
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
	if ctx.JSONMode() {
		return writeJSON(cmd, map[string]any{"message": message})
	}
	fmt.Fprintln(cmd.OutOrStdout(), message)
	return nil
}
