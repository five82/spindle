package identification

import (
	"fmt"
	"regexp"
	"strings"

	"spindle/internal/disc"
)

func isPlaceholderTitle(title, discLabel string) bool {
	t := strings.ToLower(strings.TrimSpace(title))
	if t == "" {
		return true
	}
	if t == "unknown disc" || strings.HasPrefix(t, "unknown disc") {
		return true
	}
	if strings.TrimSpace(discLabel) != "" && strings.EqualFold(strings.TrimSpace(title), strings.TrimSpace(discLabel)) {
		if disc.IsGenericLabel(title) || isTechnicalLabel(title) || looksLikeDiscLabel(title) {
			return true
		}
	}
	return false
}

func looksLikeDiscLabel(title string) bool {
	upper := strings.ToUpper(strings.TrimSpace(title))
	if upper == "" {
		return false
	}
	if strings.Contains(upper, "DISC") || strings.Contains(upper, "DISK") {
		return strings.Contains(upper, "_")
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

func truncateFingerprint(value string) string {
	v := strings.TrimSpace(value)
	if len(v) <= 12 {
		return v
	}
	return v[:12]
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
		if title != "" && !isTechnicalLabel(title) {
			return title
		}
	}

	return "Unknown Disc"
}

var (
	allDigitsPattern  = regexp.MustCompile(`^\d+$`)
	shortCodePattern  = regexp.MustCompile(`^[A-Z0-9_]{1,4}$`)
	technicalPatterns = []string{
		"LOGICAL_VOLUME_ID", "DVD_VIDEO", "BLURAY", "BD_ROM",
		"UNTITLED", "UNKNOWN DISC", "VOLUME_", "VOLUME ID", "DISK_", "TRACK_",
	}
)

func isTechnicalLabel(title string) bool {
	title = strings.TrimSpace(title)
	if title == "" {
		return true
	}

	upper := strings.ToUpper(title)
	for _, pattern := range technicalPatterns {
		if strings.Contains(upper, pattern) {
			return true
		}
	}

	// All uppercase with underscores (likely technical label)
	if strings.Contains(title, "_") && title == upper && len(title) > 8 {
		return true
	}

	// All numbers or very short uppercase codes
	return allDigitsPattern.MatchString(title) || shortCodePattern.MatchString(title)
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

	if title == "Unknown Disc" {
		return "Default"
	}

	return "Original"
}
