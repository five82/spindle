package main

import (
	"github.com/spf13/cobra"

	"spindle/internal/daemonrun"
)

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
			cfg, err := ctx.ensureConfig()
			if err != nil {
				return err
			}
			return daemonrun.Run(cmd.Context(), cfg, daemonrun.Options{
				LogLevel:    ctx.resolvedLogLevel(cfg),
				Development: ctx.logDevelopment(cfg),
				Diagnostic:  ctx.diagnosticMode(),
			})
		},
	}
	cmd.Flags().BoolVar(&diagnostic, "diagnostic", false, "Enable diagnostic mode with separate DEBUG logs")
	return cmd
}
