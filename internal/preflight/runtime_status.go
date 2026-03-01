package preflight

import (
	"context"
	"fmt"
	"os/exec"
	"strings"
	"time"

	"spindle/internal/config"
)

// CheckJellyfinFromConfig evaluates Jellyfin status from config and connectivity.
func CheckJellyfinFromConfig(cfg *config.Config) Result {
	const name = "Jellyfin"

	if cfg == nil {
		return Result{Name: name, Detail: "Unknown"}
	}
	if !cfg.Jellyfin.Enabled {
		return Result{Name: name, Detail: "Disabled"}
	}
	if strings.TrimSpace(cfg.Jellyfin.URL) == "" {
		return Result{Name: name, Detail: "Missing URL"}
	}
	if strings.TrimSpace(cfg.Jellyfin.APIKey) == "" {
		return Result{Name: name, Detail: "Missing API key"}
	}
	check := CheckJellyfin(context.Background(), cfg.Jellyfin.URL, cfg.Jellyfin.APIKey)
	if check.Passed {
		return Result{Name: name, Passed: true, Detail: check.Detail}
	}
	return Result{Name: name, Detail: check.Detail}
}

// CheckOpenSubtitlesFromConfig evaluates OpenSubtitles status from config and connectivity.
func CheckOpenSubtitlesFromConfig(cfg *config.Config) Result {
	const name = "OpenSubtitles"

	if cfg == nil {
		return Result{Name: name, Detail: "Unknown"}
	}
	if !cfg.Subtitles.OpenSubtitlesEnabled {
		return Result{Name: name, Passed: true, Detail: "Disabled"}
	}
	if strings.TrimSpace(cfg.Subtitles.OpenSubtitlesAPIKey) == "" {
		return Result{Name: name, Detail: "Missing API key"}
	}
	check := CheckOpenSubtitles(
		context.Background(),
		"",
		cfg.Subtitles.OpenSubtitlesAPIKey,
		cfg.Subtitles.OpenSubtitlesUserAgent,
	)
	if check.Passed {
		return Result{Name: name, Passed: true, Detail: check.Detail}
	}
	return Result{Name: name, Detail: check.Detail}
}

// DiscProbe reports the current optical-disc detection snapshot.
type DiscProbe struct {
	Detected bool
	Device   string
	Label    string
	Type     string
}

// ProbeDisc attempts to detect and classify the currently loaded disc via lsblk.
func ProbeDisc(device string) DiscProbe {
	device = strings.TrimSpace(device)
	if device == "" {
		device = "/dev/sr0"
	}
	if _, err := exec.LookPath("lsblk"); err != nil {
		return DiscProbe{Device: device}
	}

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	cmd := exec.CommandContext(ctx, "lsblk", "-no", "LABEL,FSTYPE", device)
	output, err := cmd.Output()
	if err != nil {
		return DiscProbe{Device: device}
	}
	text := strings.TrimSpace(string(output))
	if text == "" {
		return DiscProbe{Device: device}
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
	return DiscProbe{
		Detected: true,
		Device:   device,
		Label:    label,
		Type:     classifyDiscType(fstype),
	}
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

// DiscDetail renders a display-friendly summary for status UIs.
func (p DiscProbe) DiscDetail() string {
	if !p.Detected {
		return "No disc detected"
	}
	return fmt.Sprintf("%s disc '%s' on %s", p.Type, p.Label, p.Device)
}
