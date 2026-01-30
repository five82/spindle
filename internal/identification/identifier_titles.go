package identification

import (
	"fmt"
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
	return disc.IsUnusableLabel(title)
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
		if title != "" && !isTechnicalLabel(title) {
			return title
		}
	}

	return "Unknown Disc"
}

func isTechnicalLabel(title string) bool {
	return disc.IsUnusableLabel(title)
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
