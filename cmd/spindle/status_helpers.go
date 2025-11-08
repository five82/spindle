package main

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"golang.org/x/sys/unix"

	"spindle/internal/config"
	"spindle/internal/services/plex"
)

func plexStatusLine(cfg *config.Config, colorize bool) string {
	if cfg == nil {
		return renderStatusLine("Plex", statusInfo, "Unknown", colorize)
	}
	if strings.TrimSpace(cfg.PlexURL) == "" {
		return renderStatusLine("Plex", statusWarn, "Not configured or unreachable", colorize)
	}
	if !cfg.PlexLinkEnabled {
		return renderStatusLine("Plex", statusWarn, "Link disabled", colorize)
	}
	manager, err := plex.NewTokenManager(cfg)
	if err != nil {
		return renderStatusLine("Plex", statusError, fmt.Sprintf("Auth error (%v)", err), colorize)
	}
	if manager.HasAuthorization() {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()

		client := &http.Client{Timeout: 5 * time.Second}
		if err := plex.CheckAuth(ctx, cfg, client, manager); err != nil {
			switch {
			case errors.Is(err, plex.ErrAuthorizationMissing):
				return renderStatusLine("Plex", statusWarn, "Link required (run spindle plex link)", colorize)
			case errors.Is(err, context.DeadlineExceeded), errors.Is(err, context.Canceled):
				return renderStatusLine("Plex", statusWarn, "Auth check timed out", colorize)
			default:
				return renderStatusLine("Plex", statusWarn, fmt.Sprintf("Auth check failed (%v)", err), colorize)
			}
		}
		return renderStatusLine("Plex", statusOK, "Linked", colorize)
	}
	return renderStatusLine("Plex", statusWarn, "Link required (run spindle plex link)", colorize)
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

func executableAvailable(name string) bool {
	_, err := exec.LookPath(name)
	return err == nil
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
