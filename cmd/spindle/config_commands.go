package main

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/spf13/cobra"

	"spindle/internal/config"
)

func newConfigCommand(ctx *commandContext) *cobra.Command {
	configCmd := &cobra.Command{
		Use:   "config",
		Short: "Configuration utilities",
	}

	configCmd.AddCommand(newConfigValidateCommand())
	configCmd.AddCommand(newConfigInitCommand())

	return configCmd
}

func newConfigInitCommand() *cobra.Command {
	var targetPath string
	var overwrite bool

	cmd := &cobra.Command{
		Use:         "init",
		Short:       "Create a sample configuration file",
		Annotations: map[string]string{"skipConfigLoad": "true"},
		RunE: func(cmd *cobra.Command, args []string) error {
			target := strings.TrimSpace(targetPath)
			if target == "" {
				defaultPath, err := config.DefaultConfigPath()
				if err != nil {
					return fmt.Errorf("determine default config path: %w", err)
				}
				target = defaultPath
			} else {
				expanded, err := config.ExpandPath(target)
				if err != nil {
					return fmt.Errorf("resolve config path: %w", err)
				}
				target = expanded
			}

			dir := filepath.Dir(target)
			if err := os.MkdirAll(dir, 0o755); err != nil {
				return fmt.Errorf("create config directory %q: %w", dir, err)
			}

			if !overwrite {
				if _, err := os.Stat(target); err == nil {
					return fmt.Errorf("config file already exists at %s (use --overwrite to replace it)", target)
				} else if err != nil && !os.IsNotExist(err) {
					return fmt.Errorf("check config path: %w", err)
				}
			}

			if err := config.CreateSample(target); err != nil {
				return fmt.Errorf("create sample config: %w", err)
			}

			out := cmd.OutOrStdout()
			fmt.Fprintf(out, "Wrote sample configuration to %s\n", target)
			fmt.Fprintln(out, "Edit the file to set tmdb_api_key (or export TMDB_API_KEY) before running Spindle.")
			return nil
		},
	}

	cmd.Flags().StringVarP(&targetPath, "path", "p", "", "Destination for the configuration file")
	cmd.Flags().BoolVar(&overwrite, "overwrite", false, "Overwrite existing configuration if present")
	return cmd
}

func newConfigValidateCommand() *cobra.Command {
	return &cobra.Command{
		Use:   "validate",
		Short: "Validate configuration file",
		RunE: func(cmd *cobra.Command, args []string) error {
			cfg, path, exists, err := config.Load("")
			if err != nil {
				return fmt.Errorf("load config: %w", err)
			}
			if err := cfg.EnsureDirectories(); err != nil {
				return fmt.Errorf("ensure directories: %w", err)
			}
			out := cmd.OutOrStdout()
			fmt.Fprintf(out, "Config path: %s\n", path)
			if !exists {
				fmt.Fprintln(out, "Config file did not exist; defaults were used")
			}
			fmt.Fprintln(out, "Configuration valid")
			return nil
		},
	}
}
