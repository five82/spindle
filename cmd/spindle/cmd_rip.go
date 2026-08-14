package main

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/five82/spindle/internal/daemonctl"
	"github.com/five82/spindle/internal/makemkv"
)

// newRipCmd rips selected titles from a disc to a plain directory, outside
// the daemon workflow and the rip cache. It exists for agent/operator
// orchestration of edge cases (extras, shorts, alternate cuts) where the
// automated single-feature selection does not apply.
func newRipCmd() *cobra.Command {
	var (
		titleIDs  []int
		allTitles bool
		outputDir string
		minLength int
	)
	cmd := &cobra.Command{
		Use:   "rip [device]",
		Short: "Rip selected disc titles to a directory",
		Long: `Rip specific titles (or every title) from a disc into a directory,
bypassing the queue and the rip cache. Intended for manual orchestration of
content the automated workflow does not cover (disc extras, theatrical
shorts, alternate cuts).

Title IDs depend on --min-length: MakeMKV numbers only the titles it reports.
Use the same --min-length for 'spindle disc scan' and 'spindle rip' so the
IDs line up (both default to 0, which reports everything).

The daemon must be stopped: manual orchestration and the daemon must not
compete for the optical drive.`,
		Example: `  spindle rip --title 2                # one title to the current directory
  spindle rip --title 2,5,7 -o ripped/ # several titles
  spindle rip --all -o ripped/         # every title on the disc`,
		GroupID: groupDisc,
		Args:    cobra.MaximumNArgs(1),
		RunE: func(_ *cobra.Command, args []string) error {
			if daemonctl.IsRunning(lockPath(), socketPath()) {
				return fmt.Errorf("daemon is running; stop it first with 'spindle stop'")
			}
			if len(titleIDs) == 0 && !allTitles {
				return fmt.Errorf("select titles with --title <id,...> or --all")
			}
			if len(titleIDs) > 0 && allTitles {
				return fmt.Errorf("cannot combine --title and --all")
			}
			var device string
			if len(args) > 0 {
				device = args[0]
			}
			if device == "" && cfg != nil {
				device = cfg.MakeMKV.OpticalDrive
			}
			if device == "" {
				return fmt.Errorf("no device specified and no optical drive configured")
			}
			if err := os.MkdirAll(outputDir, 0o755); err != nil {
				return fmt.Errorf("create output dir: %w", err)
			}

			ctx := context.Background()
			logger := buildLogger()

			fmt.Printf("Scanning disc on %s...\n", device)
			info, err := makemkv.Scan(ctx, device,
				time.Duration(cfg.MakeMKV.InfoTimeout)*time.Second, minLength, logger)
			if err != nil {
				return fmt.Errorf("makemkv scan: %w", err)
			}

			byID := make(map[int]makemkv.TitleInfo, len(info.Titles))
			for _, t := range info.Titles {
				byID[t.ID] = t
			}

			var targets []makemkv.TitleInfo
			if allTitles {
				targets = info.Titles
			} else {
				for _, id := range titleIDs {
					t, ok := byID[id]
					if !ok {
						return fmt.Errorf("title %d not found on disc (available: %s)", id, availableTitleIDs(info.Titles))
					}
					targets = append(targets, t)
				}
			}
			if len(targets) == 0 {
				return fmt.Errorf("no titles to rip (disc reported %d titles at min-length %ds)", len(info.Titles), minLength)
			}

			fmt.Printf("\nRipping %d title(s) from %q to %s:\n", len(targets), info.Name, outputDir)
			for _, t := range targets {
				fmt.Printf("  Title %d: %s (%s, %d chapters, %s)\n",
					t.ID, t.Name, formatTitleDuration(t.Duration), t.Chapters, formatBytes(t.SizeBytes))
			}

			var ripped []string
			for i, t := range targets {
				fmt.Printf("\nPhase %d/%d - Ripping title %d\n", i+1, len(targets), t.ID)
				before := snapshotMKVNames(outputDir)
				lastPercent := -10.0
				err := makemkv.Rip(ctx, device, t.ID, outputDir,
					time.Duration(cfg.MakeMKV.RipTimeout)*time.Second, minLength,
					func(p makemkv.RipProgress) {
						if p.Percent-lastPercent < 10 {
							return
						}
						lastPercent = p.Percent
						fmt.Printf("  %5.1f%%\n", p.Percent)
					}, logger)
				if err != nil {
					return fmt.Errorf("rip title %d: %w", t.ID, err)
				}
				newFile := newMKVName(outputDir, before)
				if newFile == "" {
					return fmt.Errorf("rip title %d: no new .mkv file appeared in %s", t.ID, outputDir)
				}
				path := filepath.Join(outputDir, newFile)
				ripped = append(ripped, path)
				fmt.Printf("  %s %s\n", successStyle("Ripped:"), path)
			}

			fmt.Printf("\n%s\n", successStyle(fmt.Sprintf("Ripped %d title(s)", len(ripped))))
			for _, p := range ripped {
				fmt.Printf("  %s\n", p)
			}
			return nil
		},
	}
	cmd.Flags().IntSliceVar(&titleIDs, "title", nil, "Title IDs to rip (comma-separated)")
	cmd.Flags().BoolVar(&allTitles, "all", false, "Rip every title on the disc")
	cmd.Flags().StringVarP(&outputDir, "output-dir", "o", ".", "Directory for ripped files")
	cmd.Flags().IntVar(&minLength, "min-length", 0, "Minimum title length in seconds (must match the scan that produced the title IDs)")
	return cmd
}

func availableTitleIDs(titles []makemkv.TitleInfo) string {
	ids := make([]string, 0, len(titles))
	for _, t := range titles {
		ids = append(ids, fmt.Sprintf("%d", t.ID))
	}
	return strings.Join(ids, ", ")
}

func formatTitleDuration(seconds int) string {
	return fmt.Sprintf("%d:%02d:%02d", seconds/3600, (seconds%3600)/60, seconds%60)
}

// snapshotMKVNames records the .mkv filenames currently in dir so a rip's
// output can be identified by diff (MakeMKV picks its own filenames).
func snapshotMKVNames(dir string) map[string]bool {
	names := make(map[string]bool)
	entries, err := os.ReadDir(dir)
	if err != nil {
		return names
	}
	for _, e := range entries {
		if !e.IsDir() && strings.EqualFold(filepath.Ext(e.Name()), ".mkv") {
			names[e.Name()] = true
		}
	}
	return names
}

func newMKVName(dir string, before map[string]bool) string {
	var fresh []string
	for name := range snapshotMKVNames(dir) {
		if !before[name] {
			fresh = append(fresh, name)
		}
	}
	if len(fresh) == 0 {
		return ""
	}
	sort.Strings(fresh)
	return fresh[0]
}
