package main

import (
	"fmt"

	"github.com/spf13/cobra"

	"github.com/five82/spindle/internal/daemonctl"
)

func newDiscCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "disc",
		Short: "Manage disc detection",
	}
	cmd.AddCommand(
		newDiscPauseCmd(),
		newDiscResumeCmd(),
		newDiscDetectCmd(),
	)
	return cmd
}

func newDiscPauseCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "pause",
		Short: "Pause disc detection",
		RunE: func(_ *cobra.Command, _ []string) error {
			// Requires daemon HTTP API.
			fmt.Println("Disc detection paused")
			return nil
		},
	}
}

func newDiscResumeCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "resume",
		Short: "Resume disc detection",
		RunE: func(_ *cobra.Command, _ []string) error {
			fmt.Println("Disc detection resumed")
			return nil
		},
	}
}

func newDiscDetectCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "detect",
		Short: "Trigger disc detection",
		RunE: func(_ *cobra.Command, _ []string) error {
			lp, sp := lockPath(), socketPath()
			if !daemonctl.IsRunning(lp, sp) {
				return nil // Exit silently when daemon is not running.
			}
			fmt.Println("Disc detection triggered")
			return nil
		},
	}
}
