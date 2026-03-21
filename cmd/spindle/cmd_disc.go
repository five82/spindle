package main

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"time"

	"github.com/spf13/cobra"

	"github.com/five82/spindle/internal/daemonctl"
	"github.com/five82/spindle/internal/sockhttp"
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
			resp, err := daemonDiscPost("/api/disc/pause")
			if err != nil {
				return err
			}
			fmt.Println(resp)
			return nil
		},
	}
}

func newDiscResumeCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "resume",
		Short: "Resume disc detection",
		RunE: func(_ *cobra.Command, _ []string) error {
			resp, err := daemonDiscPost("/api/disc/resume")
			if err != nil {
				return err
			}
			fmt.Println(resp)
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
			resp, err := daemonDiscPost("/api/disc/detect")
			if err != nil {
				return err
			}
			fmt.Println(resp)
			return nil
		},
	}
}

// daemonDiscPost sends a POST to the daemon Unix socket and returns the response body.
func daemonDiscPost(path string) (string, error) {
	lp, sp := lockPath(), socketPath()
	if !daemonctl.IsRunning(lp, sp) {
		return "", fmt.Errorf("daemon is not running")
	}

	client := sockhttp.NewUnixClient(sp, 10*time.Second)

	req, err := http.NewRequest(http.MethodPost, "http://localhost"+path, nil)
	if err != nil {
		return "", fmt.Errorf("create request: %w", err)
	}
	if cfg != nil {
		sockhttp.SetAuth(req, cfg.API.Token)
	}

	resp, err := client.Do(req)
	if err != nil {
		return "", fmt.Errorf("request failed: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", fmt.Errorf("read response: %w", err)
	}

	if resp.StatusCode != http.StatusOK {
		var errResp struct {
			Error string `json:"error"`
		}
		if json.Unmarshal(body, &errResp) == nil && errResp.Error != "" {
			return "", fmt.Errorf("%s", errResp.Error)
		}
		return "", fmt.Errorf("request failed with status %d", resp.StatusCode)
	}

	return string(body), nil
}
