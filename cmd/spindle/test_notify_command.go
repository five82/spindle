package main

import (
	"errors"
	"fmt"

	"github.com/spf13/cobra"

	"spindle/internal/ipc"
)

func newTestNotifyCommand(ctx *commandContext) *cobra.Command {
	return &cobra.Command{
		Use:   "test-notify",
		Short: "Send a test notification",
		RunE: func(cmd *cobra.Command, args []string) error {
			return ctx.withClient(func(client *ipc.Client) error {
				resp, err := client.TestNotification()
				if err != nil {
					if resp != nil && resp.Message != "" {
						fmt.Fprintln(cmd.OutOrStdout(), resp.Message)
					}
					return err
				}
				if resp == nil {
					return errors.New("missing notification response")
				}
				switch {
				case resp.Message != "":
					fmt.Fprintln(cmd.OutOrStdout(), resp.Message)
				case resp.Sent:
					fmt.Fprintln(cmd.OutOrStdout(), "Test notification sent")
				default:
					fmt.Fprintln(cmd.OutOrStdout(), "Notification not sent")
				}
				return nil
			})
		},
	}
}
