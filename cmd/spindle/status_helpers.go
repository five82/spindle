package main

import (
	"context"
	"fmt"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"spindle/internal/config"
	"spindle/internal/preflight"
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
	result := preflight.CheckJellyfin(context.Background(), cfg.Jellyfin.URL, cfg.Jellyfin.APIKey)
	if result.Passed {
		return renderStatusLine("Jellyfin", statusOK, result.Detail, colorize)
	}
	return renderStatusLine("Jellyfin", statusWarn, result.Detail, colorize)
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
	result := preflight.CheckDirectoryAccess(label, path)
	if result.Passed {
		return renderStatusLine(label, statusOK, result.Detail, colorize)
	}
	return renderStatusLine(label, statusError, result.Detail, colorize)
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

func discDetectionStatusLine(daemonRunning, netlinkActive bool, colorize bool) string {
	if netlinkActive {
		return renderStatusLine("Disc Detection", statusOK, "Netlink monitoring active", colorize)
	}
	if !daemonRunning {
		return renderStatusLine("Disc Detection", statusInfo, "Inactive (daemon not running)", colorize)
	}
	return renderStatusLine("Disc Detection", statusWarn, "Netlink unavailable (manual detection via 'spindle disc detected')", colorize)
}
