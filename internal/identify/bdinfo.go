package identify

import (
	"context"
	"fmt"
	"log/slog"
	"os/exec"
	"regexp"
	"strings"

	"github.com/five82/spindle/internal/logs"
)

// BDInfoResult holds parsed output from the bd_info command.
type BDInfoResult struct {
	DiscID           string
	VolumeIdentifier string
	DiscName         string
	IsBluRay         bool
	HasAACS          bool
	Provider         string
	Studio           string
	Year             string
}

// yearPattern extracts a 4-digit year from text.
// Uses (?:\b|_) to also match years preceded by underscores (common in disc labels).
var yearPattern = regexp.MustCompile(`(?:\b|_)((19|20)\d{2})(?:\b|_|$)`)

// studioPrefixes maps lowercase prefixes to full studio names.
var studioPrefixes = map[string]string{
	"sony":       "Sony Pictures",
	"warner":     "Warner Bros.",
	"universal":  "Universal Pictures",
	"disney":     "Walt Disney Studios",
	"paramount":  "Paramount Pictures",
	"mgm":        "Metro-Goldwyn-Mayer",
	"fox":        "20th Century Studios",
	"lionsgate":  "Lionsgate",
}

// RunBDInfo executes bd_info on the given device and returns parsed results.
// Returns nil (not an error) if bd_info is not installed.
func RunBDInfo(ctx context.Context, device string, logger *slog.Logger) (*BDInfoResult, error) {
	logger = logs.Default(logger)

	path, err := exec.LookPath("bd_info")
	if err != nil {
		logger.Info("bd_info not available",
			"decision_type", logs.DecisionBDInfoAvailability,
			"decision_result", "unavailable",
			"decision_reason", "not found in PATH",
		)
		return nil, nil // graceful degradation
	}

	//nolint:gosec // device path is validated by caller
	cmd := exec.CommandContext(ctx, path, device)
	out, err := cmd.Output()
	if err != nil {
		return nil, fmt.Errorf("bd_info %s: %w", device, err)
	}

	result := parseBDInfoOutput(string(out))
	logger.Debug("bd_info parsed result",
		"disc_id", result.DiscID,
		"disc_name", result.DiscName,
		"volume_id", result.VolumeIdentifier,
		"year", result.Year,
		"studio", result.Studio,
		"is_bluray", result.IsBluRay,
	)
	return result, nil
}

// parseBDInfoOutput parses key-value output from bd_info.
func parseBDInfoOutput(output string) *BDInfoResult {
	result := &BDInfoResult{}

	for _, line := range strings.Split(output, "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}

		key, value, found := strings.Cut(line, ":")
		if !found {
			continue
		}
		key = strings.TrimSpace(key)
		value = strings.TrimSpace(value)

		switch strings.ToLower(key) {
		case "disc id":
			result.DiscID = strings.ToUpper(value)
		case "volume identifier":
			result.VolumeIdentifier = value
		case "disc title", "disc name":
			result.DiscName = value
		case "bluray detected":
			result.IsBluRay = strings.EqualFold(value, "yes")
		case "aacs detected":
			result.HasAACS = strings.EqualFold(value, "yes")
		case "provider data":
			result.Provider = value
			result.Studio = mapStudio(value)
		}
	}

	// Extract year from disc name first, then volume identifier.
	result.Year = extractYear(result.DiscName, result.VolumeIdentifier)

	return result
}

// mapStudio maps provider data to a known studio name via case-insensitive
// prefix matching. Falls back to the provider string if > 3 chars.
func mapStudio(provider string) string {
	lower := strings.ToLower(provider)
	for prefix, studio := range studioPrefixes {
		if strings.HasPrefix(lower, prefix) {
			return studio
		}
	}
	if len(provider) > 3 {
		return provider
	}
	return ""
}

// extractYear returns the first 4-digit year found in any of the sources.
func extractYear(sources ...string) string {
	for _, s := range sources {
		if m := yearPattern.FindStringSubmatch(s); m != nil {
			return m[1]
		}
	}
	return ""
}
