package identification

import (
	"fmt"
	"strings"

	"spindle/internal/disc"
)

// Sentinel defaults for unidentified content.
const (
	DefaultDiscTitle = "Unknown Disc"
	DefaultShowLabel = "Manual Import"
)

func isPlaceholderTitle(title, discLabel string) bool {
	t := strings.ToLower(strings.TrimSpace(title))
	if t == "" {
		return true
	}
	if strings.HasPrefix(t, "unknown disc") {
		return true
	}
	if strings.TrimSpace(discLabel) != "" && strings.EqualFold(strings.TrimSpace(title), strings.TrimSpace(discLabel)) {
		if disc.IsGenericLabel(title) || disc.IsUnusableLabel(title) {
			return true
		}
	}
	return false
}

func unknownContentKey(fingerprint string) string {
	fp := strings.TrimSpace(fingerprint)
	if fp == "" {
		return "unknown:pending"
	}
	if len(fp) > 16 {
		fp = fp[:16]
	}
	return fmt.Sprintf("unknown:%s", strings.ToLower(fp))
}

func determineBestTitle(currentTitle string, scanResult *disc.ScanResult) string {
	// Priority order: MakeMKV title > BDInfo name > current title
	candidates := []string{}

	if len(scanResult.Titles) > 0 {
		candidates = append(candidates, strings.TrimSpace(scanResult.Titles[0].Name))
	}
	if scanResult.BDInfo != nil {
		candidates = append(candidates, strings.TrimSpace(scanResult.BDInfo.DiscName))
	}
	candidates = append(candidates, currentTitle)

	for _, title := range candidates {
		if title != "" && !disc.IsUnusableLabel(title) {
			return title
		}
	}

	return DefaultDiscTitle
}

// collectDiscSources builds a candidate string list for disc number extraction
// and show hint derivation. Seeds are added first, then BDInfo fields in a
// consistent order (VolumeIdentifier, DiscName). Empty strings are omitted.
func collectDiscSources(scanResult *disc.ScanResult, seeds ...string) []string {
	out := make([]string, 0, len(seeds)+2)
	for _, s := range seeds {
		if strings.TrimSpace(s) != "" {
			out = append(out, s)
		}
	}
	if scanResult != nil && scanResult.BDInfo != nil {
		if v := scanResult.BDInfo.VolumeIdentifier; v != "" {
			out = append(out, v)
		}
		if v := scanResult.BDInfo.DiscName; v != "" {
			out = append(out, v)
		}
	}
	return out
}

func detectTitleSource(title string, scanResult *disc.ScanResult) string {
	if len(scanResult.Titles) > 0 {
		makemkvTitle := strings.TrimSpace(scanResult.Titles[0].Name)
		if makemkvTitle == title {
			return "MakeMKV"
		}
	}

	if scanResult.BDInfo != nil {
		bdName := strings.TrimSpace(scanResult.BDInfo.DiscName)
		if bdName == title {
			return "BDInfo"
		}
	}

	if title == DefaultDiscTitle {
		return "Default"
	}

	return "Original"
}
