package disc

import (
	"regexp"
	"strconv"
	"strings"
)

type bdInfoParser struct{}

func (bdInfoParser) Parse(output []byte) *BDInfoResult {
	if len(output) == 0 {
		return nil
	}

	result := &BDInfoResult{}
	lines := strings.Split(string(output), "\n")

	for _, line := range lines {
		trimmed := strings.TrimSpace(line)
		if trimmed == "" {
			continue
		}

		switch {
		case strings.Contains(trimmed, "Volume Identifier") && strings.Contains(trimmed, ":"):
			parts := strings.SplitN(trimmed, ":", 2)
			if len(parts) == 2 {
				result.VolumeIdentifier = strings.TrimSpace(parts[1])
			}
		case strings.Contains(trimmed, "Disc Title") && strings.Contains(trimmed, ":"):
			parts := strings.SplitN(trimmed, ":", 2)
			if len(parts) == 2 {
				result.DiscName = strings.TrimSpace(parts[1])
			}
		case strings.Contains(trimmed, "BluRay detected") && strings.Contains(trimmed, ":"):
			parts := strings.SplitN(trimmed, ":", 2)
			if len(parts) == 2 {
				result.IsBluRay = strings.TrimSpace(strings.ToLower(parts[1])) == "yes"
			}
		case strings.Contains(trimmed, "AACS detected") && strings.Contains(trimmed, ":"):
			parts := strings.SplitN(trimmed, ":", 2)
			if len(parts) == 2 {
				result.HasAACS = strings.TrimSpace(strings.ToLower(parts[1])) == "yes"
			}
		case strings.Contains(trimmed, "provider data") && strings.Contains(trimmed, ":"):
			parts := strings.SplitN(trimmed, ":", 2)
			if len(parts) == 2 {
				provider := strings.TrimSpace(strings.Trim(parts[1], " \"'"))
				if provider != "" {
					result.Provider = provider
					result.Studio = extractStudioFromProvider(provider)
				}
			}
		}
	}

	if result.DiscName == "" && result.VolumeIdentifier != "" {
		result.DiscName = ExtractDiscNameFromVolumeID(result.VolumeIdentifier)
		result.Year = extractYearFromIdentifier(result.DiscName, result.VolumeIdentifier)
	}

	if result.DiscName != "" && result.Year == 0 {
		result.Year = extractYearFromIdentifier(result.DiscName, result.VolumeIdentifier)
	}

	if result.Provider != "" && result.Studio == "" {
		result.Studio = extractStudioFromProvider(result.Provider)
	}

	if result.VolumeIdentifier == "" && result.DiscName == "" && result.Provider == "" {
		return nil
	}

	return result
}

func extractYearFromIdentifier(discName, volumeID string) int {
	yearPattern := regexp.MustCompile(`\b(19|20)\d{2}\b`)

	if discName != "" {
		if match := yearPattern.FindString(discName); match != "" {
			if year, err := strconv.Atoi(match); err == nil {
				return year
			}
		}
	}

	if volumeID != "" {
		if match := yearPattern.FindString(volumeID); match != "" {
			if year, err := strconv.Atoi(match); err == nil {
				return year
			}
		}
	}

	return 0
}

func extractStudioFromProvider(provider string) string {
	if provider == "" {
		return ""
	}

	studioMappings := map[string]string{
		"sony":          "Sony Pictures",
		"sony pictures": "Sony Pictures",
		"warner":        "Warner Bros",
		"warner bros":   "Warner Bros",
		"universal":     "Universal Pictures",
		"disney":        "Walt Disney Pictures",
		"paramount":     "Paramount Pictures",
		"mgm":           "Metro-Goldwyn-Mayer",
		"fox":           "20th Century Fox",
		"lionsgate":     "Lionsgate",
	}

	lowerProvider := strings.ToLower(provider)
	for studio, fullName := range studioMappings {
		if strings.Contains(lowerProvider, studio) {
			return fullName
		}
	}

	cleaned := strings.TrimSpace(provider)
	if len(cleaned) <= 3 {
		return ""
	}
	return cleaned
}
