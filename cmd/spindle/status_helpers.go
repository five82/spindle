package main

import (
	"path/filepath"
	"strings"

	"spindle/internal/config"
	"spindle/internal/preflight"
)

func jellyfinStatusLine(cfg *config.Config, colorize bool) string {
	result := preflight.CheckJellyfinFromConfig(cfg)
	if result.Passed {
		return renderStatusLine("Jellyfin", statusOK, result.Detail, colorize)
	}
	if strings.EqualFold(strings.TrimSpace(result.Detail), "Unknown") {
		return renderStatusLine("Jellyfin", statusInfo, result.Detail, colorize)
	}
	return renderStatusLine("Jellyfin", statusWarn, result.Detail, colorize)
}

func openSubtitlesStatusLine(cfg *config.Config, colorize bool) string {
	result := preflight.CheckOpenSubtitlesFromConfig(cfg)
	if result.Passed {
		if strings.EqualFold(strings.TrimSpace(result.Detail), "Disabled") {
			return renderStatusLine("OpenSubtitles", statusInfo, result.Detail, colorize)
		}
		return renderStatusLine("OpenSubtitles", statusOK, result.Detail, colorize)
	}
	if strings.EqualFold(strings.TrimSpace(result.Detail), "Unknown") {
		return renderStatusLine("OpenSubtitles", statusInfo, result.Detail, colorize)
	}
	return renderStatusLine("OpenSubtitles", statusWarn, result.Detail, colorize)
}

func detectDiscLine(device string, colorize bool) string {
	probe := preflight.ProbeDisc(device)
	if !probe.Detected {
		return renderStatusLine("Disc", statusInfo, "No disc detected", colorize)
	}
	return renderStatusLine("Disc", statusOK, probe.DiscDetail(), colorize)
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
