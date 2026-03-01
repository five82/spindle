package main

import (
	"fmt"
	"strings"

	"github.com/spf13/cobra"

	"spindle/internal/api"
)

func newCacheRipCommand(ctx *commandContext) *cobra.Command {
	var device string

	cmd := &cobra.Command{
		Use:   "rip [device]",
		Short: "Rip a disc into the rip cache",
		Long: `Rip the current disc and populate the rip cache without moving on to encoding.
This command runs the identification and ripping stages, then exits after the
cache entry is written. If a cache entry already exists for the disc fingerprint,
it is overwritten.`,
		Args: cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			cfg, err := ctx.ensureConfig()
			if err != nil {
				return fmt.Errorf("load configuration: %w", err)
			}

			if len(args) > 0 {
				device = strings.TrimSpace(args[0])
			}
			if device == "" {
				device = strings.TrimSpace(cfg.MakeMKV.OpticalDrive)
			}
			if device == "" {
				return fmt.Errorf("no device specified and no optical_drive configured")
			}

			if _, err := ctx.dialClient(); err == nil {
				return fmt.Errorf("spindle daemon is running; stop it with: spindle stop")
			}

			logger, err := ctx.newCLILogger(cfg, "", false)
			if err != nil {
				return err
			}

			result, err := api.PopulateRipCache(cmd.Context(), api.PopulateRipCacheRequest{
				Config: cfg,
				Device: device,
				Logger: logger,
			})
			if err != nil {
				return err
			}
			fmt.Fprintf(cmd.OutOrStdout(), "✅ Rip cache populated: %s\n", result.CacheDir)
			return nil
		},
	}

	cmd.Flags().StringVarP(&device, "device", "d", "", "Optical device path (default: configured optical_drive)")

	return cmd
}
