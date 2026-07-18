package main

import (
	"fmt"
	"os"

	"github.com/spf13/cobra"

	"github.com/five82/spindle/internal/encoder"
)

// newEncodeWorkerCmd runs one Reel encode in an isolated child process and
// streams reporter events to the daemon as JSON lines. It is hidden because
// stdout is a machine protocol, not an operator interface.
func newEncodeWorkerCmd() *cobra.Command {
	var input string
	var outputDir string
	cmd := &cobra.Command{
		Use:    "encode-worker",
		Short:  "Internal: encode one file and stream reporter events (used by the daemon)",
		Hidden: true,
		Args:   cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			if input == "" || outputDir == "" {
				return fmt.Errorf("encode-worker requires --input and --output-dir")
			}
			// Errors are already reported on the stdout wire as a failure
			// event; the non-zero exit is the daemon's secondary signal.
			if err := encoder.RunWorker(cmd.Context(), input, outputDir, os.Stdout); err != nil {
				return fmt.Errorf("encode failed: %w", err)
			}
			return nil
		},
	}
	cmd.Flags().StringVar(&input, "input", "", "Input video file")
	cmd.Flags().StringVar(&outputDir, "output-dir", "", "Directory for the encoded output")
	return cmd
}
