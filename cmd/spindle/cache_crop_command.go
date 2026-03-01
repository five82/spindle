package main

import (
	"fmt"
	"io"
	"time"

	"github.com/spf13/cobra"

	draptolib "github.com/five82/drapto"

	"spindle/internal/api"
)

func newCacheCropCommand(ctx *commandContext) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "crop <entry|path>",
		Short: "Run crop detection on a cached rip file",
		Long: `Run crop detection on a cached rip file.

Provide either a cache entry number (from 'spindle cache stats') or a direct path
to a video file. This command is useful for troubleshooting crop detection issues,
particularly when "Multiple aspect ratios detected" prevents automatic cropping.

The command will:
1. Probe the video for resolution and HDR status
2. Sample 141 points from 15-85% of the video
3. Display the crop filter if detected, or show the candidate distribution
4. Help diagnose why a particular crop was or wasn't applied

Example:
  spindle cache crop 1              # Run on cache entry #1
  spindle cache crop /path/to/file.mkv`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			target, label, err := resolveCacheTarget(cmd, ctx, args[0], cmd.OutOrStdout())
			if err != nil {
				return err
			}

			out := cmd.OutOrStdout()
			start := time.Now()
			fmt.Fprintf(out, "Crop Detection Start: %s\n", start.Format("Jan 2 2006 15:04:05 MST"))
			fmt.Fprintf(out, "Target: %s\n\n", label)

			result, err := api.RunCropDiagnostic(cmd.Context(), api.RunCropDiagnosticRequest{Target: target})

			end := time.Now()
			fmt.Fprintf(out, "\nCrop Detection End: %s\n", end.Format("Jan 2 2006 15:04:05 MST"))
			fmt.Fprintf(out, "Crop Detection Duration: %s\n", end.Sub(start).Round(time.Millisecond))

			if err != nil {
				return err
			}

			printCropResults(out, result)
			return nil
		},
	}
	return cmd
}

func printCropResults(out io.Writer, result *draptolib.CropDetectionResult) {
	insight := api.BuildCropDetectionInsight(result)

	fmt.Fprintln(out, "=== Video Properties ===")
	fmt.Fprintf(out, "Resolution: %dx%d\n", result.VideoWidth, result.VideoHeight)
	fmt.Fprintf(out, "Dynamic Range: %s (threshold=%d)\n", insight.DynamicRange, insight.Threshold)

	fmt.Fprintln(out, "\n=== Crop Detection Result ===")
	fmt.Fprintf(out, "Status: %s\n", result.Message)

	if result.Required {
		fmt.Fprintf(out, "Crop Filter: %s\n", result.CropFilter)
		if insight.OutputDimensions != "" {
			fmt.Fprintf(out, "Output Dimensions: %s\n", insight.OutputDimensions)
		}
		if insight.OutputAspectRatio != "" {
			fmt.Fprintf(out, "Aspect Ratio: %s\n", insight.OutputAspectRatio)
		}
	} else if result.MultipleRatios {
		fmt.Fprintln(out, "Result: No crop applied (multiple aspect ratios detected)")
		fmt.Fprintf(out, "Note: No single crop value was found in >%.0f%% of samples\n", insight.MultipleRatioThreshold)
	} else {
		fmt.Fprintln(out, "Result: No crop needed")
	}

	fmt.Fprintf(out, "\n=== Sample Analysis ===\n")
	fmt.Fprintf(out, "Total Samples: %d\n", result.TotalSamples)
	fmt.Fprintf(out, "Unique Crop Values: %d\n", len(result.Candidates))

	if len(result.Candidates) > 0 {
		fmt.Fprintln(out, "\nCrop Candidates (by frequency):")
		for i, c := range insight.Candidates {
			marker := "  "
			if c.IsPreferred {
				marker = "* "
			}
			fmt.Fprintf(out, "%s%2d. crop=%s  count=%3d (%5.1f%%)%s\n",
				marker, i+1, c.Crop, c.Count, c.Percent, c.Dimensions)
		}

		if result.MultipleRatios && len(insight.Candidates) > 0 {
			fmt.Fprintf(out, "\nDiagnosis: Top candidate has %.1f%% of samples (need >%.0f%% for auto-crop)\n",
				insight.TopCandidatePercent, insight.MultipleRatioThreshold)
			if insight.VariationSuggestion != "" {
				fmt.Fprintf(out, "Suggestion: %s\n", insight.VariationSuggestion)
			}
		}
	}
}
