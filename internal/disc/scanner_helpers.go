package disc

import (
	"bufio"
	"errors"
	"os/exec"
	"regexp"
	"strings"
)

// ExtractDiscNameFromVolumeID cleans volume identifier to extract disc name.
func ExtractDiscNameFromVolumeID(volumeID string) string {
	if volumeID == "" {
		return ""
	}

	title := volumeID
	title = regexp.MustCompile(`^\d+_`).ReplaceAllString(title, "")
	title = regexp.MustCompile(`(?i)_S\d+_DISC_\d+$`).ReplaceAllString(title, "")
	title = regexp.MustCompile(`(?i)_TV$`).ReplaceAllString(title, "")
	title = strings.ReplaceAll(title, "_", " ")

	title = strings.TrimSpace(title)

	if title == "" || regexp.MustCompile(`^\d+$`).MatchString(title) {
		return ""
	}

	return title
}

// IsGenericLabel checks if a disc label is too generic for reliable identification.
func IsGenericLabel(label string) bool {
	if label == "" {
		return true
	}

	genericPatterns := []string{
		"LOGICAL_VOLUME_ID",
		"DVD_VIDEO",
		"BLURAY",
		"BD_ROM",
		"UNTITLED",
		"UNKNOWN DISC",
	}

	lowerLabel := strings.ToUpper(label)
	for _, pattern := range genericPatterns {
		if strings.Contains(lowerLabel, pattern) {
			return true
		}
	}

	if regexp.MustCompile(`^\d+$`).MatchString(label) {
		return true
	}

	if regexp.MustCompile(`^[A-Z0-9_]{1,3}$`).MatchString(label) {
		return true
	}

	return false
}

func extractMakemkvStderr(err error) []byte {
	type stderrProvider interface {
		Stderr() []byte
	}
	var provider stderrProvider
	if errors.As(err, &provider) {
		return provider.Stderr()
	}
	var exitErr *exec.ExitError
	if errors.As(err, &exitErr) {
		return exitErr.Stderr
	}
	return nil
}

func normalizeDeviceArg(device string) string {
	trimmed := strings.TrimSpace(device)
	if trimmed == "" {
		return "disc:0"
	}

	lower := strings.ToLower(trimmed)
	if strings.HasPrefix(lower, "disc:") || strings.HasPrefix(lower, "dev:") {
		return trimmed
	}

	if strings.HasPrefix(lower, "/dev/") {
		return "dev:" + trimmed
	}

	return trimmed
}

func extractDevicePath(device string) string {
	trimmed := strings.TrimSpace(device)
	if strings.HasPrefix(trimmed, "/dev/") {
		return trimmed
	}
	if strings.HasPrefix(trimmed, "dev:") {
		return strings.TrimPrefix(trimmed, "dev:")
	}
	if strings.HasPrefix(trimmed, "disc:") {
		return ""
	}
	return trimmed
}

func extractMakemkvErrorMessage(stdout, stderr []byte) string {
	joined := make([]string, 0, 2)
	if len(stderr) > 0 {
		joined = append(joined, string(stderr))
	}
	if len(stdout) > 0 {
		joined = append(joined, string(stdout))
	}
	if len(joined) == 0 {
		return ""
	}

	combined := strings.TrimSpace(strings.Join(joined, "\n"))
	if combined == "" {
		return ""
	}

	scanner := bufio.NewScanner(strings.NewReader(combined))
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if !strings.HasPrefix(line, "MSG:") {
			continue
		}

		parts := strings.SplitN(line, ",", 4)
		if len(parts) < 4 {
			continue
		}

		text := strings.Trim(parts[3], " \"")
		if text == "" {
			continue
		}

		lower := strings.ToLower(text)
		if strings.Contains(lower, "too old") ||
			strings.Contains(lower, "registration key") ||
			strings.Contains(lower, "failed") ||
			strings.Contains(lower, "error") ||
			strings.Contains(lower, "copy protection") ||
			strings.Contains(lower, "no disc") ||
			strings.Contains(lower, "not found") ||
			strings.Contains(lower, "read error") ||
			strings.Contains(lower, "i/o error") ||
			strings.Contains(lower, "timeout") {
			return text
		}
	}

	scanner = bufio.NewScanner(strings.NewReader(combined))
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line != "" {
			return line
		}
	}

	return combined
}
