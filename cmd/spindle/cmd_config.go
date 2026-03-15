package main

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/spf13/cobra"

	"github.com/five82/spindle/internal/config"
)

func newConfigCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "config",
		Short: "Manage configuration",
	}
	cmd.AddCommand(newConfigInitCmd(), newConfigValidateCmd())
	return cmd
}

func newConfigInitCmd() *cobra.Command {
	var (
		path      string
		overwrite bool
	)
	cmd := &cobra.Command{
		Use:   "init",
		Short: "Create a sample configuration file",
		Annotations: map[string]string{
			"skipConfigLoad": "true",
		},
		RunE: func(_ *cobra.Command, _ []string) error {
			if path == "" {
				// Default config path.
				configHome := os.Getenv("XDG_CONFIG_HOME")
				if configHome == "" {
					home, _ := os.UserHomeDir()
					configHome = home + "/.config"
				}
				path = configHome + "/spindle/config.toml"
			}

			if !overwrite {
				if _, err := os.Stat(path); err == nil {
					return fmt.Errorf("config file already exists: %s (use --overwrite)", path)
				}
			}

			dir := filepath.Dir(path)
			if err := os.MkdirAll(dir, 0o755); err != nil {
				return fmt.Errorf("create config dir: %w", err)
			}

			if err := os.WriteFile(path, []byte(config.SampleConfig()), 0o644); err != nil {
				return err
			}
			fmt.Printf("Config written to %s\n", path)
			return nil
		},
	}
	cmd.Flags().StringVarP(&path, "path", "p", "", "Destination file path")
	cmd.Flags().BoolVar(&overwrite, "overwrite", false, "Overwrite existing file")
	return cmd
}

func newConfigValidateCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "validate",
		Short: "Validate configuration file",
		RunE: func(_ *cobra.Command, _ []string) error {
			if err := cfg.Validate(); err != nil {
				fmt.Printf("%s\n%v\n", failStyle("Config: INVALID"), err)
				return err
			}
			if err := cfg.EnsureDirectories(); err != nil {
				return fmt.Errorf("ensure directories: %w", err)
			}
			fmt.Println(successStyle("Config: valid"))
			return nil
		},
	}
}
