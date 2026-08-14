package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"time"

	"github.com/spf13/cobra"

	"github.com/five82/spindle/internal/daemonctl"
	"github.com/five82/spindle/internal/makemkv"
	"github.com/five82/spindle/internal/sockhttp"
)

func newDiscCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:     "disc",
		Short:   "Disc detection and identification",
		GroupID: groupDisc,
	}
	cmd.AddCommand(
		newDiscPauseCmd(),
		newDiscResumeCmd(),
		newDiscDetectCmd(),
		newIdentifyCmd(),
		newDiscScanCmd(),
	)
	return cmd
}

// newDiscScanCmd lists every title on a disc from a raw MakeMKV scan, with
// no identification or TMDB involvement. Unlike 'disc identify' it defaults
// to --min-length 0 so short extras and trailers are visible; the reported
// title IDs are what 'spindle rip' consumes.
func newDiscScanCmd() *cobra.Command {
	var (
		asJSON    bool
		minLength int
	)
	cmd := &cobra.Command{
		Use:   "scan [device]",
		Short: "List all disc titles from a raw MakeMKV scan",
		Example: `  spindle disc scan                # all titles, configured drive
  spindle disc scan --json         # machine-readable title inventory
  spindle disc scan /dev/sr1 --min-length 60`,
		Args: cobra.MaximumNArgs(1),
		RunE: func(_ *cobra.Command, args []string) error {
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

			if !asJSON {
				fmt.Printf("Scanning disc on %s...\n", device)
			}
			info, err := makemkv.Scan(context.Background(), device,
				time.Duration(cfg.MakeMKV.InfoTimeout)*time.Second, minLength, buildLogger())
			if err != nil {
				return fmt.Errorf("makemkv scan: %w", err)
			}

			if asJSON {
				type scanTitle struct {
					ID              int    `json:"id"`
					Name            string `json:"name,omitempty"`
					Playlist        string `json:"playlist,omitempty"`
					DurationSeconds int    `json:"duration_seconds"`
					Chapters        int    `json:"chapters"`
					SizeBytes       int64  `json:"size_bytes"`
					Segments        int    `json:"segments,omitempty"`
				}
				out := struct {
					Device           string      `json:"device"`
					DiscName         string      `json:"disc_name,omitempty"`
					MinLengthSeconds int         `json:"min_length_seconds"`
					Titles           []scanTitle `json:"titles"`
				}{Device: device, DiscName: info.Name, MinLengthSeconds: minLength, Titles: []scanTitle{}}
				for _, t := range info.Titles {
					out.Titles = append(out.Titles, scanTitle{
						ID:              t.ID,
						Name:            t.Name,
						Playlist:        t.Playlist,
						DurationSeconds: t.Duration,
						Chapters:        t.Chapters,
						SizeBytes:       t.SizeBytes,
						Segments:        t.SegmentCount,
					})
				}
				return printJSON(out)
			}

			fmt.Printf("\n%s %s\n", labelStyle("Disc:  "), info.Name)
			fmt.Printf("%s %d (min length %ds)\n", labelStyle("Titles:"), len(info.Titles), minLength)
			for _, t := range info.Titles {
				line := fmt.Sprintf("  Title %d: %s (%d chapters, %s)",
					t.ID, formatTitleDuration(t.Duration), t.Chapters, formatBytes(t.SizeBytes))
				if t.Playlist != "" {
					line += fmt.Sprintf(" [%s]", t.Playlist)
				}
				if t.Name != "" {
					line += " " + t.Name
				}
				fmt.Println(line)
			}
			return nil
		},
	}
	cmd.Flags().BoolVar(&asJSON, "json", false, "Output the title inventory as JSON")
	cmd.Flags().IntVar(&minLength, "min-length", 0, "Minimum title length in seconds (0 reports everything)")
	return cmd
}

func newDiscPauseCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "pause",
		Short: "Pause disc detection",
		RunE: func(_ *cobra.Command, _ []string) error {
			var resp struct {
				Changed bool `json:"changed"`
			}
			if err := daemonDiscPost("/api/disc/pause", &resp); err != nil {
				return err
			}
			if resp.Changed {
				fmt.Println("Disc detection paused")
			} else {
				fmt.Println("Disc detection already paused")
			}
			return nil
		},
	}
}

func newDiscResumeCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "resume",
		Short: "Resume disc detection",
		RunE: func(_ *cobra.Command, _ []string) error {
			var resp struct {
				Changed bool `json:"changed"`
			}
			if err := daemonDiscPost("/api/disc/resume", &resp); err != nil {
				return err
			}
			if resp.Changed {
				fmt.Println("Disc detection resumed")
			} else {
				fmt.Println("Disc detection already active")
			}
			return nil
		},
	}
}

func newDiscDetectCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "detect",
		Short: "Trigger disc detection",
		Long: `Trigger disc detection on the daemon.

Exits successfully with a notice when the daemon is not running, so it is
safe to call from udev hooks on disc insertion.`,
		RunE: func(_ *cobra.Command, _ []string) error {
			lp, sp := lockPath(), socketPath()
			if !daemonctl.IsRunning(lp, sp) {
				fmt.Fprintln(os.Stderr, "daemon not running; nothing to do")
				return nil
			}
			var resp struct {
				Handled bool   `json:"handled"`
				Message string `json:"message"`
			}
			if err := daemonDiscPost("/api/disc/detect", &resp); err != nil {
				return err
			}
			switch {
			case resp.Message != "":
				fmt.Println(resp.Message)
			case resp.Handled:
				fmt.Println("Disc detection started")
			default:
				fmt.Println("Disc detection skipped")
			}
			return nil
		},
	}
}

// daemonDiscPost sends a POST to the daemon Unix socket and decodes the JSON
// response into out (which may be nil to discard the body).
func daemonDiscPost(path string, out any) error {
	lp, sp := lockPath(), socketPath()
	if !daemonctl.IsRunning(lp, sp) {
		return fmt.Errorf("daemon is not running")
	}

	client := sockhttp.NewUnixClient(sp, 10*time.Second)

	req, err := http.NewRequest(http.MethodPost, "http://localhost"+path, nil)
	if err != nil {
		return fmt.Errorf("create request: %w", err)
	}
	if cfg != nil {
		sockhttp.SetAuth(req, cfg.API.Token)
	}

	resp, err := client.Do(req)
	if err != nil {
		return fmt.Errorf("request failed: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return fmt.Errorf("read response: %w", err)
	}

	if resp.StatusCode != http.StatusOK {
		var errResp struct {
			Error string `json:"error"`
		}
		if json.Unmarshal(body, &errResp) == nil && errResp.Error != "" {
			return fmt.Errorf("%s", errResp.Error)
		}
		return fmt.Errorf("request failed with status %d", resp.StatusCode)
	}

	if out != nil {
		if err := json.Unmarshal(body, out); err != nil {
			return fmt.Errorf("decode response: %w", err)
		}
	}
	return nil
}
