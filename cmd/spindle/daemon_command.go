package main

import "github.com/spf13/cobra"

func newDaemonRunCommand(ctx *commandContext) *cobra.Command {
	var diagnostic bool
	cmd := &cobra.Command{
		Use:          "daemon",
		Short:        "Run the spindle daemon (internal)",
		Hidden:       true,
		Annotations:  map[string]string{"skipConfigLoad": "true"},
		SilenceUsage: true,
		RunE: func(cmd *cobra.Command, _ []string) error {
			ctx.diagnostic = &diagnostic
			// ensure config directories exist before we enter daemon loop
			if _, err := ctx.ensureConfig(); err != nil {
				return err
			}
			return runDaemonProcess(cmd.Context(), ctx)
		},
	}
	cmd.Flags().BoolVar(&diagnostic, "diagnostic", false, "Enable diagnostic mode with separate DEBUG logs")
	return cmd
}
