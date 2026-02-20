package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/spf13/cobra"

	draptolib "github.com/five82/drapto"

	"spindle/internal/config"
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
			target, label, err := resolveCropTarget(ctx, args[0], cmd.OutOrStdout())
			if err != nil {
				return err
			}

			out := cmd.OutOrStdout()
			start := time.Now()
			fmt.Fprintf(out, "Crop Detection Start: %s\n", start.Format("Jan 2 2006 15:04:05 MST"))
			fmt.Fprintf(out, "Target: %s\n\n", label)

			result, err := runCropDetection(cmd.Context(), target)

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

func resolveCropTarget(ctx *commandContext, arg string, out io.Writer) (string, string, error) {
	arg = strings.TrimSpace(arg)
	if arg == "" {
		return "", "", errors.New("cache entry or path is required")
	}

	if entryNum, err := strconv.Atoi(arg); err == nil {
		if entryNum < 1 {
			return "", "", fmt.Errorf("invalid cache entry number: %d", entryNum)
		}
		manager, warn, err := cacheManager(ctx)
		if warn != "" {
			fmt.Fprintln(out, warn)
		}
		if err != nil || manager == nil {
			if err != nil {
				return "", "", err
			}
			return "", "", errors.New("rip cache is unavailable")
		}
		stats, err := manager.Stats(context.Background())
		if err != nil {
			return "", "", err
		}
		if entryNum > len(stats.EntrySummaries) {
			return "", "", fmt.Errorf("cache entry %d out of range (only %d entries exist)", entryNum, len(stats.EntrySummaries))
		}
		entry := stats.EntrySummaries[entryNum-1]
		if entry.PrimaryFile == "" {
			return "", "", fmt.Errorf("cache entry %d has no detectable video files", entryNum)
		}
		target := filepath.Join(entry.Directory, entry.PrimaryFile)
		label := strings.TrimSpace(entry.PrimaryFile)
		if label == "" {
			label = filepath.Base(entry.Directory)
		}
		return target, label, nil
	}

	path, err := config.ExpandPath(arg)
	if err != nil {
		return "", "", err
	}
	info, err := os.Stat(path)
	if err != nil {
		return "", "", fmt.Errorf("inspect path %q: %w", path, err)
	}
	if info.IsDir() {
		target, count, err := selectPrimaryVideo(path)
		if err != nil {
			return "", "", err
		}
		label := filepath.Base(path)
		if label == "" {
			label = path
		}
		if count > 1 {
			label = fmt.Sprintf("%s (+%d more)", label, count-1)
		}
		return target, label, nil
	}
	return path, filepath.Base(path), nil
}

func runCropDetection(ctx context.Context, target string) (*draptolib.CropDetectionResult, error) {
	return draptolib.DetectCrop(ctx, target)
}

func printCropResults(out io.Writer, result *draptolib.CropDetectionResult) {
	fmt.Fprintln(out, "=== Video Properties ===")
	fmt.Fprintf(out, "Resolution: %dx%d\n", result.VideoWidth, result.VideoHeight)
	if result.IsHDR {
		fmt.Fprintf(out, "Dynamic Range: HDR (threshold=100)\n")
	} else {
		fmt.Fprintf(out, "Dynamic Range: SDR (threshold=16)\n")
	}

	fmt.Fprintln(out, "\n=== Crop Detection Result ===")
	fmt.Fprintf(out, "Status: %s\n", result.Message)

	if result.Required {
		fmt.Fprintf(out, "Crop Filter: %s\n", result.CropFilter)
		// Calculate cropped dimensions
		if parts := strings.Split(strings.TrimPrefix(result.CropFilter, "crop="), ":"); len(parts) >= 2 {
			if w, err := strconv.ParseUint(parts[0], 10, 32); err == nil {
				if h, err := strconv.ParseUint(parts[1], 10, 32); err == nil {
					removedHeight := uint64(result.VideoHeight) - h
					aspectRatio := float64(w) / float64(h)
					fmt.Fprintf(out, "Output Dimensions: %dx%d (removing %d pixels)\n", w, h, removedHeight)
					fmt.Fprintf(out, "Aspect Ratio: %.3f:1\n", aspectRatio)
				}
			}
		}
	} else if result.MultipleRatios {
		fmt.Fprintln(out, "Result: No crop applied (multiple aspect ratios detected)")
		fmt.Fprintln(out, "Note: No single crop value was found in >80% of samples")
	} else {
		fmt.Fprintln(out, "Result: No crop needed")
	}

	fmt.Fprintf(out, "\n=== Sample Analysis ===\n")
	fmt.Fprintf(out, "Total Samples: %d\n", result.TotalSamples)
	fmt.Fprintf(out, "Unique Crop Values: %d\n", len(result.Candidates))

	if len(result.Candidates) > 0 {
		fmt.Fprintln(out, "\nCrop Candidates (by frequency):")
		for i, c := range result.Candidates {
			// Parse crop dimensions for display
			parts := strings.Split(c.Crop, ":")
			dimStr := ""
			if len(parts) >= 2 {
				if w, err := strconv.ParseUint(parts[0], 10, 32); err == nil {
					if h, err := strconv.ParseUint(parts[1], 10, 32); err == nil {
						aspectRatio := float64(w) / float64(h)
						dimStr = fmt.Sprintf(" -> %dx%d (%.3f:1)", w, h, aspectRatio)
					}
				}
			}

			marker := "  "
			if i == 0 && result.Required {
				marker = "* "
			}
			fmt.Fprintf(out, "%s%2d. crop=%s  count=%3d (%5.1f%%)%s\n",
				marker, i+1, c.Crop, c.Count, c.Percent, dimStr)
		}

		// If multiple ratios, show what threshold would have been needed
		if result.MultipleRatios && len(result.Candidates) > 0 {
			topPercent := result.Candidates[0].Percent
			fmt.Fprintf(out, "\nDiagnosis: Top candidate has %.1f%% of samples (need >80%% for auto-crop)\n", topPercent)
			if topPercent >= 70 {
				fmt.Fprintln(out, "Suggestion: This is borderline - the film may have minor aspect ratio variations")
			} else if topPercent >= 50 {
				fmt.Fprintln(out, "Suggestion: Significant aspect ratio variation detected - may be intentional (e.g., IMAX sequences)")
			} else {
				fmt.Fprintln(out, "Suggestion: High variation in detected crops - check if HDR threshold is appropriate")
			}
		}
	}
}
