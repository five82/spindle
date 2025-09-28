package main

import "github.com/spf13/cobra"

func newDaemonRunCommand(ctx *commandContext) *cobra.Command {
	cmd := &cobra.Command{
		Use:          "daemon",
		Short:        "Run the spindle daemon (internal)",
		Hidden:       true,
		Annotations:  map[string]string{"skipConfigLoad": "true"},
		SilenceUsage: true,
		RunE: func(cmd *cobra.Command, _ []string) error {
			// ensure config directories exist before we enter daemon loop
			if _, err := ctx.ensureConfig(); err != nil {
				return err
			}
			return runDaemonProcess(cmd.Context(), ctx)
		},
	}
	return cmd
}
