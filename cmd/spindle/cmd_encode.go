package main

import (
	"context"
	"fmt"
	"os"

	"github.com/spf13/cobra"

	"github.com/five82/spindle/internal/daemonctl"
	"github.com/five82/spindle/internal/encoder"
)

// newEncodeCmd encodes a single file with Reel target-quality mode, outside
// the daemon workflow. It exists for agent/operator orchestration of edge
// cases (extras, alternate cuts, joined multi-disc features) that the
// automated pipeline does not handle.
func newEncodeCmd() *cobra.Command {
	var outputDir string
	cmd := &cobra.Command{
		Use:   "encode <input>",
		Short: "Encode one video file with Reel (AV1 target-quality)",
		Long: `Encode one video file with Reel's AV1 target-quality mode, the same
encode path the daemon uses. Intended for manual orchestration of content the
automated workflow does not cover (disc extras, alternate cuts, joined
multi-disc features).

The daemon must be stopped: manual orchestration and the daemon must not
compete for encoding resources.`,
		Example: `  spindle encode ripped/title_t02.mkv
  spindle encode short.mkv -o encoded/`,
		GroupID: groupDisc,
		Args:    cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if daemonctl.IsRunning(lockPath(), socketPath()) {
				return fmt.Errorf("daemon is running; stop it first with 'spindle stop'")
			}
			input := args[0]
			if _, err := os.Stat(input); err != nil {
				return fmt.Errorf("input file: %w", err)
			}
			if err := os.MkdirAll(outputDir, 0o755); err != nil {
				return fmt.Errorf("create output dir: %w", err)
			}

			result, err := encoder.RunConsole(context.Background(), input, outputDir, os.Stdout)
			if err != nil {
				return fmt.Errorf("encode failed: %w", err)
			}

			fmt.Printf("\n%s\n", successStyle("Encode complete"))
			fmt.Printf("%s %s\n", labelStyle("Output:    "), result.OutputFile)
			fmt.Printf("%s %s -> %s (%.1f%% smaller)\n", labelStyle("Size:      "),
				formatBytes(int64(result.OriginalSize)), formatBytes(int64(result.EncodedSize)),
				result.SizeReductionPercent)
			validation := successStyle("passed")
			if !result.ValidationPassed {
				validation = failStyle("FAILED")
			}
			fmt.Printf("%s %s\n", labelStyle("Validation:"), validation)
			if !result.ValidationPassed {
				return fmt.Errorf("encode validation failed for %s", result.OutputFile)
			}
			return nil
		},
	}
	cmd.Flags().StringVarP(&outputDir, "output-dir", "o", ".", "Directory for the encoded output")
	return cmd
}
