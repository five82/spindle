package main

import (
	"github.com/spf13/cobra"
)

func newRootCommand() *cobra.Command {
	var socketFlag string
	var configFlag string

	ctx := newCommandContext(&socketFlag, &configFlag)

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

	for _, cmd := range newDaemonCommands(ctx) {
		rootCmd.AddCommand(cmd)
	}

	rootCmd.AddCommand(newQueueCommand(ctx))
	rootCmd.AddCommand(newDaemonRunCommand(ctx))
	rootCmd.AddCommand(newQueueHealthCommand(ctx))
	rootCmd.AddCommand(newShowCommand(ctx))
	rootCmd.AddCommand(newAddFileCommand(ctx))
	rootCmd.AddCommand(newIdentifyCommand(ctx))
	rootCmd.AddCommand(newTestNotifyCommand(ctx))
	rootCmd.AddCommand(newConfigCommand(ctx))
	rootCmd.AddCommand(newPlexCommand(ctx.configValue))

	return rootCmd
}
