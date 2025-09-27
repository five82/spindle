package main

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/spf13/cobra"

	"spindle/internal/config"
	"spindle/internal/services/plex"
)

func newPlexCommand(cfgFn func() *config.Config) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "plex",
		Short: "Manage Plex integration",
	}

	cmd.AddCommand(newPlexLinkCommand(cfgFn))

	return cmd
}

func newPlexLinkCommand(cfgFn func() *config.Config) *cobra.Command {
	return &cobra.Command{
		Use:   "link",
		Short: "Connect Spindle to Plex using the device link flow",
		RunE: func(cmd *cobra.Command, args []string) error {
			cfg := cfgFn()
			if cfg == nil {
				return errors.New("configuration not loaded")
			}

			if !cfg.PlexLinkEnabled {
				fmt.Fprintln(cmd.OutOrStdout(), "Plex link is disabled in config.toml; enable plex_link_enabled to link.")
				return nil
			}

			ctx, cancel := context.WithTimeout(cmd.Context(), 3*time.Minute)
			defer cancel()

			manager, err := plex.NewTokenManager(cfg)
			if err != nil {
				return err
			}

			pin, err := manager.RequestPin(ctx)
			if err != nil {
				return err
			}

			fmt.Fprintln(cmd.OutOrStdout(), "Open https://plex.tv/link and enter the code:")
			fmt.Fprintf(cmd.OutOrStdout(), "\n    %s\n\n", pin.Code)
			fmt.Fprintln(cmd.OutOrStdout(), "Waiting for authorization... (Ctrl+C to abort)")

			expires := pin.ExpiresAt
			if expires.IsZero() {
				expires = time.Now().Add(5 * time.Minute)
			}

			poll := time.NewTicker(2 * time.Second)
			defer poll.Stop()

			for {
				select {
				case <-ctx.Done():
					return ctx.Err()
				case <-poll.C:
					status, err := manager.PollPin(ctx, pin.ID)
					if err != nil {
						return err
					}
					if status.Authorized {
						if err := manager.SetAuthorizationToken(status.AuthorizationToken); err != nil {
							return err
						}
						if _, err := manager.Token(ctx); err != nil {
							return err
						}
						fmt.Fprintln(cmd.OutOrStdout(), "Plex linked successfully. Spindle will refresh your library after each organize stage.")
						return nil
					}
					if !status.ExpiresAt.IsZero() {
						expires = status.ExpiresAt
					}
					if time.Now().After(expires) {
						return errors.New("link code expired; run 'spindle plex link' again")
					}
				}
			}
		},
	}
}
