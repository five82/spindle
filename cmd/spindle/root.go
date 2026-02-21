package main

import (
	"github.com/spf13/cobra"
)

func newRootCommand() *cobra.Command {
	var socketFlag string
	var configFlag string
	var logLevelFlag string
	var verbose bool
	var jsonOutput bool

	ctx := newCommandContext(&socketFlag, &configFlag, &logLevelFlag, &verbose, nil, &jsonOutput)

	rootCmd := &cobra.Command{
		Use:           "spindle",
		Short:         "Spindle Go CLI",
		SilenceUsage:  true,
		SilenceErrors: true,
		PersistentPreRunE: func(cmd *cobra.Command, args []string) error {
			if shouldSkipConfig(cmd) {
				return nil
			}
			_, err := ctx.ensureConfig()
			return err
		},
		RunE: func(cmd *cobra.Command, args []string) error {
			return cmd.Help()
		},
	}

	rootCmd.PersistentFlags().StringVar(&socketFlag, "socket", "", "Path to the spindle daemon socket")
	rootCmd.PersistentFlags().StringVarP(&configFlag, "config", "c", "", "Configuration file path")
	rootCmd.PersistentFlags().StringVar(&logLevelFlag, "log-level", "", "Log level for CLI output (debug, info, warn, error)")
	rootCmd.PersistentFlags().BoolVarP(&verbose, "verbose", "v", false, "Shorthand for --log-level=debug")
	rootCmd.PersistentFlags().BoolVar(&jsonOutput, "json", false, "Output in JSON format")

	for _, cmd := range newDaemonCommands(ctx) {
		rootCmd.AddCommand(cmd)
	}

	rootCmd.AddCommand(newQueueCommand(ctx))
	rootCmd.AddCommand(newDaemonRunCommand(ctx))
	rootCmd.AddCommand(newQueueHealthCommand(ctx))
	rootCmd.AddCommand(newShowCommand(ctx))
	rootCmd.AddCommand(newIdentifyCommand(ctx))
	rootCmd.AddCommand(newGenerateSubtitleCommand(ctx))
	rootCmd.AddCommand(newTestNotifyCommand(ctx))
	rootCmd.AddCommand(newConfigCommand(ctx))
	rootCmd.AddCommand(newCacheCommand(ctx))
	rootCmd.AddCommand(newStagingCommand(ctx))
	rootCmd.AddCommand(newDiscCommand(ctx))
	rootCmd.AddCommand(newDiscIDCommand(ctx))

	return rootCmd
}
