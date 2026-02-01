package main

import (
	"fmt"
	"os"
	"os/exec"
	"strings"

	"github.com/spf13/cobra"

	"spindle/internal/ipc"
)

const udevRulePath = "/etc/udev/rules.d/99-spindle.rules"

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
		newDiscDetectedCommand(ctx),
		newDiscSetupCommand(ctx),
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

func newDiscDetectedCommand(ctx *commandContext) *cobra.Command {
	var device string

	cmd := &cobra.Command{
		Use:   "detected",
		Short: "Notify daemon that a disc was detected (called by udev)",
		Long: `Notify the spindle daemon that a disc was detected on a device.
This command is intended to be called by udev rules when a disc is inserted.
It sends an RPC to the daemon to process the disc.

If the daemon is not running, this command exits silently (exit code 0)
to avoid breaking udev.`,
		RunE: func(cmd *cobra.Command, args []string) error {
			if strings.TrimSpace(device) == "" {
				return fmt.Errorf("--device is required")
			}

			client, err := ctx.dialClient()
			if err != nil {
				// Daemon not running - exit silently to avoid breaking udev
				fmt.Fprintln(cmd.ErrOrStderr(), "spindle daemon not running; disc detection skipped")
				return nil
			}
			defer client.Close()

			resp, err := client.DiscDetected(device)
			if err != nil {
				fmt.Fprintf(cmd.ErrOrStderr(), "disc detection failed: %v\n", err)
				return nil // Don't return error to avoid breaking udev
			}

			if resp.Handled {
				fmt.Fprintf(cmd.OutOrStdout(), "%s (item %d)\n", resp.Message, resp.ItemID)
			} else {
				fmt.Fprintln(cmd.OutOrStdout(), resp.Message)
			}
			return nil
		},
	}

	cmd.Flags().StringVar(&device, "device", "", "Device path (e.g., /dev/sr0)")
	_ = cmd.MarkFlagRequired("device")

	return cmd
}

func newDiscSetupCommand(_ *commandContext) *cobra.Command {
	return &cobra.Command{
		Use:   "setup",
		Short: "Install udev rule for automatic disc detection",
		Long: `Install a udev rule that automatically notifies spindle when a disc is inserted.
This command requires sudo privileges to write to /etc/udev/rules.d/.

The rule triggers 'spindle disc detected' when a disc is inserted into the
configured optical drive.`,
		RunE: func(cmd *cobra.Command, args []string) error {
			stdout := cmd.OutOrStdout()
			stderr := cmd.ErrOrStderr()

			// Get the path to the spindle binary
			exePath, err := os.Executable()
			if err != nil {
				return fmt.Errorf("failed to determine spindle path: %w", err)
			}

			// Generate udev rule content
			rule := generateUdevRule(exePath)

			fmt.Fprintln(stdout, "Installing udev rule for disc detection...")
			fmt.Fprintf(stdout, "Rule path: %s\n", udevRulePath)
			fmt.Fprintln(stdout)
			fmt.Fprintln(stdout, "Rule content:")
			fmt.Fprintln(stdout, rule)
			fmt.Fprintln(stdout)

			// Write the udev rule using sudo tee
			writeCmd := exec.Command("sudo", "tee", udevRulePath) //nolint:gosec
			writeCmd.Stdin = strings.NewReader(rule)
			writeCmd.Stderr = stderr
			if err := writeCmd.Run(); err != nil {
				return fmt.Errorf("failed to write udev rule (sudo tee): %w", err)
			}

			fmt.Fprintln(stdout, "Reloading udev rules...")

			// Reload udev rules
			reloadCmd := exec.Command("sudo", "udevadm", "control", "--reload-rules")
			reloadCmd.Stderr = stderr
			if err := reloadCmd.Run(); err != nil {
				return fmt.Errorf("failed to reload udev rules: %w", err)
			}

			fmt.Fprintln(stdout)
			fmt.Fprintln(stdout, "udev rule installed successfully.")
			fmt.Fprintln(stdout, "Disc detection is now automatic when the spindle daemon is running.")
			return nil
		},
	}
}

func generateUdevRule(spindlePath string) string {
	// The udev rule triggers on disc media change events for sr* devices
	// ENV{ID_CDROM_MEDIA}=="1" ensures the disc has media loaded
	return fmt.Sprintf(`# Spindle disc detection rule
# Installed by: spindle disc setup
ACTION=="change", SUBSYSTEM=="block", KERNEL=="sr[0-9]*", ENV{ID_CDROM_MEDIA}=="1", RUN+="%s disc detected --device /dev/%%k"
`, spindlePath)
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

// CheckUdevRuleInstalled checks if the spindle udev rule is installed and valid.
// Returns true if installed, and the current spindle path if it needs updating.
func CheckUdevRuleInstalled() (installed bool, needsUpdate bool, currentPath string) {
	data, err := os.ReadFile(udevRulePath)
	if err != nil {
		return false, false, ""
	}

	content := string(data)
	if !strings.Contains(content, "spindle disc detected") {
		return false, false, ""
	}

	// Check if the path in the rule matches current executable
	exePath, err := os.Executable()
	if err != nil {
		return true, false, ""
	}

	if !strings.Contains(content, exePath) {
		return true, true, exePath
	}

	return true, false, ""
}
