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
		return true
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
	// Priority 1: MakeMKV title (highest quality - reads actual disc metadata)
	if len(scanResult.Titles) > 0 {
		makemkvTitle := strings.TrimSpace(scanResult.Titles[0].Name)
		if makemkvTitle != "" && !isTechnicalLabel(makemkvTitle) {
			return makemkvTitle
		}
	}

	// Priority 2: BDInfo disc name (Blu-ray specific, good quality)
	if scanResult.BDInfo != nil {
		bdName := strings.TrimSpace(scanResult.BDInfo.DiscName)
		if bdName != "" && !isTechnicalLabel(bdName) {
			return bdName
		}
	}

	// Priority 3: Current title (usually raw disc label, lowest quality)
	if currentTitle != "" && !isTechnicalLabel(currentTitle) {
		return currentTitle
	}

	// Priority 4: Try to derive from source path (file-based identification)
	derived := strings.TrimSpace(deriveTitle(""))
	if derived != "" && !disc.IsGenericLabel(derived) {
		return derived
	}

	return "Unknown Disc"
}

func isTechnicalLabel(title string) bool {
	if strings.TrimSpace(title) == "" {
		return true
	}

	upper := strings.ToUpper(title)

	// Common technical/generic patterns
	technicalPatterns := []string{
		"LOGICAL_VOLUME_ID",
		"DVD_VIDEO",
		"BLURAY",
		"BD_ROM",
		"UNTITLED",
		"UNKNOWN DISC",
		"VOLUME_",
		"VOLUME ID",
		"DISK_",
		"TRACK_",
	}

	for _, pattern := range technicalPatterns {
		if strings.Contains(upper, pattern) {
			return true
		}
	}

	// All uppercase with underscores (likely technical label)
	if strings.Contains(title, "_") && title == strings.ToUpper(title) && len(title) > 8 {
		return true
	}

	// All numbers or very short uppercase codes
	if regexp.MustCompile(`^\d+$`).MatchString(title) || regexp.MustCompile(`^[A-Z0-9_]{1,4}$`).MatchString(title) {
		return true
	}

	return false
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
