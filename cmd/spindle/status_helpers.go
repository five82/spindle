package main

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"golang.org/x/sys/unix"

	"spindle/internal/config"
	"spindle/internal/services/plex"
)

func plexStatusLine(cfg *config.Config) string {
	if cfg == nil {
		return "📚 Plex: Unknown"
	}
	if strings.TrimSpace(cfg.PlexURL) == "" {
		return "📚 Plex: Not configured or unreachable"
	}
	if !cfg.PlexLinkEnabled {
		return "📚 Plex: Link disabled"
	}
	manager, err := plex.NewTokenManager(cfg)
	if err != nil {
		return fmt.Sprintf("📚 Plex: Auth error (%v)", err)
	}
	if manager.HasAuthorization() {
		return "📚 Plex: Linked"
	}
	return "📚 Plex: Link required (run spindle plex link)"
}

func detectDiscLine(device string) string {
	device = strings.TrimSpace(device)
	if device == "" {
		device = "/dev/sr0"
	}
	if _, err := exec.LookPath("lsblk"); err != nil {
		return "📀 Disc: No disc detected"
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	cmd := exec.CommandContext(ctx, "lsblk", "-no", "LABEL,FSTYPE", device)
	output, err := cmd.Output()
	if err != nil {
		return "📀 Disc: No disc detected"
	}
	text := strings.TrimSpace(string(output))
	if text == "" {
		return "📀 Disc: No disc detected"
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
	discType := classifyDiscType(device, fstype)
	return fmt.Sprintf("📀 Disc: %s disc '%s' on %s", discType, label, device)
}

func classifyDiscType(device, fstype string) string {
	switch strings.ToLower(strings.TrimSpace(fstype)) {
	case "udf":
		return "Blu-ray"
	case "iso9660":
		return "DVD"
	default:
		_ = device
		return "Unknown"
	}
}

func executableAvailable(name string) bool {
	_, err := exec.LookPath(name)
	return err == nil
}

func directoryStatusLine(label, path string) string {
	if err := checkDirectoryAccess(path); err != nil {
		return fmt.Sprintf("📂 %s: %s (error: %v)", label, path, err)
	}
	return fmt.Sprintf("📂 %s: %s (read/write ok)", label, path)
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
