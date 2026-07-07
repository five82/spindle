package main

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"time"

	"github.com/spf13/cobra"

	"github.com/five82/spindle/internal/daemonctl"
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
	)
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
