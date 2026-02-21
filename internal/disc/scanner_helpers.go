package disc

import (
	"bufio"
	"errors"
	"os/exec"
	"regexp"
	"strconv"
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
	return IsUnusableLabel(label)
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

// ExtractDevicePath returns the raw /dev path from a device string.
// For "dev:/dev/sr0" returns "/dev/sr0", for "/dev/sr0" returns "/dev/sr0",
// for "disc:N" returns "" (no raw device path available).
func ExtractDevicePath(device string) string {
	return extractDevicePath(device)
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

// extractWarnings extracts hardware and disc error lines from MakeMKV output.
// MakeMKV emits bare error lines (not MSG-prefixed) for SCSI errors, read
// failures, and other hardware issues. These are critical diagnostic signals
// that indicate disc damage or drive problems.
func extractWarnings(data []byte) []string {
	if len(data) == 0 {
		return nil
	}
	var warnings []string
	scanner := bufio.NewScanner(strings.NewReader(string(data)))
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" {
			continue
		}
		// Bare error lines: Error 'Scsi error - MEDIUM ERROR:L-EC UNCORRECTABLE ERROR' ...
		if strings.HasPrefix(line, "Error ") {
			warnings = append(warnings, line)
			continue
		}
		// MSG:2003 = read errors (e.g. L-EC uncorrectable, hardware errors)
		if strings.HasPrefix(line, "MSG:") {
			code := parseMSGCode(line)
			if code == 2003 || code == 5003 || code == 5010 {
				text := parseMSGText(line)
				if text != "" {
					warnings = append(warnings, text)
				}
			}
		}
	}
	return warnings
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

// parseMSGCode extracts the numeric code from a MakeMKV MSG line.
func parseMSGCode(line string) int {
	if !strings.HasPrefix(line, "MSG:") {
		return -1
	}
	payload := strings.TrimPrefix(line, "MSG:")
	comma := strings.IndexByte(payload, ',')
	if comma < 0 {
		return -1
	}
	code, err := strconv.Atoi(strings.TrimSpace(payload[:comma]))
	if err != nil {
		return -1
	}
	return code
}

// parseMSGText extracts the human-readable message from a MakeMKV MSG line.
func parseMSGText(line string) string {
	if !strings.HasPrefix(line, "MSG:") {
		return ""
	}
	payload := strings.TrimPrefix(line, "MSG:")
	fieldIdx := 0
	inQuote := false
	start := 0
	for i := 0; i < len(payload); i++ {
		switch payload[i] {
		case '"':
			inQuote = !inQuote
		case ',':
			if !inQuote {
				fieldIdx++
				if fieldIdx == 3 {
					start = i + 1
				}
				if fieldIdx == 4 {
					return trimMSGField(payload[start:i])
				}
			}
		}
	}
	if fieldIdx >= 3 {
		return trimMSGField(payload[start:])
	}
	return ""
}

func trimMSGField(s string) string {
	s = strings.TrimSpace(s)
	if len(s) >= 2 && s[0] == '"' && s[len(s)-1] == '"' {
		s = s[1 : len(s)-1]
	}
	return s
}
