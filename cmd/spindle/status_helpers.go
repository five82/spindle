package main

import (
	"context"
	"fmt"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"golang.org/x/sys/unix"

	"spindle/internal/config"
)

func jellyfinStatusLine(cfg *config.Config, colorize bool) string {
	if cfg == nil {
		return renderStatusLine("Jellyfin", statusInfo, "Unknown", colorize)
	}
	if !cfg.Jellyfin.Enabled {
		return renderStatusLine("Jellyfin", statusWarn, "Disabled", colorize)
	}
	if strings.TrimSpace(cfg.Jellyfin.URL) == "" {
		return renderStatusLine("Jellyfin", statusWarn, "Missing URL", colorize)
	}
	if strings.TrimSpace(cfg.Jellyfin.APIKey) == "" {
		return renderStatusLine("Jellyfin", statusWarn, "Missing API key", colorize)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	client := &http.Client{Timeout: 5 * time.Second}
	if err := checkJellyfinAuth(ctx, client, cfg.Jellyfin.URL, cfg.Jellyfin.APIKey); err != nil {
		return renderStatusLine("Jellyfin", statusWarn, err.Error(), colorize)
	}
	return renderStatusLine("Jellyfin", statusOK, "Reachable", colorize)
}

func checkJellyfinAuth(ctx context.Context, client *http.Client, baseURL, apiKey string) error {
	base := strings.TrimRight(strings.TrimSpace(baseURL), "/")
	if base == "" {
		return fmt.Errorf("missing url")
	}
	if strings.TrimSpace(apiKey) == "" {
		return fmt.Errorf("missing api key")
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, base+"/Users", nil)
	if err != nil {
		return fmt.Errorf("auth check failed (%v)", err)
	}
	req.Header.Set("X-Emby-Token", strings.TrimSpace(apiKey))

	resp, err := client.Do(req)
	if err != nil {
		return fmt.Errorf("auth check failed (%v)", err)
	}
	defer resp.Body.Close()

	switch resp.StatusCode {
	case http.StatusOK:
		return nil
	case http.StatusUnauthorized, http.StatusForbidden:
		return fmt.Errorf("auth failed (invalid api key)")
	default:
		return fmt.Errorf("auth check failed (%d)", resp.StatusCode)
	}
}

func detectDiscLine(device string, colorize bool) string {
	device = strings.TrimSpace(device)
	if device == "" {
		device = "/dev/sr0"
	}
	if _, err := exec.LookPath("lsblk"); err != nil {
		return renderStatusLine("Disc", statusInfo, "No disc detected", colorize)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	cmd := exec.CommandContext(ctx, "lsblk", "-no", "LABEL,FSTYPE", device)
	output, err := cmd.Output()
	if err != nil {
		return renderStatusLine("Disc", statusInfo, "No disc detected", colorize)
	}
	text := strings.TrimSpace(string(output))
	if text == "" {
		return renderStatusLine("Disc", statusInfo, "No disc detected", colorize)
	}
	fields := strings.Fields(text)
	label := "Unknown"
	if len(fields) > 0 && fields[0] != "" {
		label = fields[0]
	}
	fstype := ""
	if len(fields) > 1 {
		fstype = strings.ToLower(fields[1])
	}
	discType := classifyDiscType(fstype)
	return renderStatusLine("Disc", statusOK, fmt.Sprintf("%s disc '%s' on %s", discType, label, device), colorize)
}

func classifyDiscType(fstype string) string {
	switch strings.ToLower(strings.TrimSpace(fstype)) {
	case "udf":
		return "Blu-ray"
	case "iso9660":
		return "DVD"
	default:
		return "Unknown"
	}
}

func directoryStatusLine(label, path string, colorize bool) string {
	if err := checkDirectoryAccess(path); err != nil {
		return renderStatusLine(label, statusError, fmt.Sprintf("%s (error: %v)", path, err), colorize)
	}
	return renderStatusLine(label, statusOK, fmt.Sprintf("%s (read/write ok)", path), colorize)
}

func checkDirectoryAccess(path string) error {
	info, err := os.Stat(path)
	if err != nil {
		if os.IsNotExist(err) {
			return fmt.Errorf("does not exist")
		}
		return fmt.Errorf("stat: %w", err)
	}
	if !info.IsDir() {
		return fmt.Errorf("is not a directory")
	}
	if err := unix.Access(path, unix.R_OK|unix.W_OK|unix.X_OK); err != nil {
		return fmt.Errorf("insufficient permissions: %w", err)
	}
	return nil
}

func librarySubdirPath(root, child string) string {
	child = strings.TrimSpace(child)
	if child == "" {
		return root
	}
	if filepath.IsAbs(child) {
		return child
	}
	return filepath.Join(root, child)
}

func udevRuleStatusLine(colorize bool) string {
	installed, needsUpdate, _ := CheckUdevRuleInstalled()
	if !installed {
		return renderStatusLine("Disc Detection", statusWarn, "udev rule not installed (run: spindle disc setup)", colorize)
	}
	if needsUpdate {
		return renderStatusLine("Disc Detection", statusWarn, "udev rule path outdated (run: spindle disc setup)", colorize)
	}
	return renderStatusLine("Disc Detection", statusOK, "udev rule installed", colorize)
}
