package disc

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"os/exec"
	"regexp"
	"sort"
	"strconv"
	"strings"
)

// Title represents a MakeMKV title entry.
type Title struct {
	ID       int    `json:"id"`
	Name     string `json:"name"`
	Duration int    `json:"duration"`
}

// BDInfoResult captures bd_info command output for enhanced disc identification.
type BDInfoResult struct {
	VolumeIdentifier string `json:"volume_identifier"`
	DiscName         string `json:"disc_name"`
	Provider         string `json:"provider"`
	IsBluRay         bool   `json:"is_blu_ray"`
	HasAACS          bool   `json:"has_aacs"`
	Year             int    `json:"year,omitempty"`
	Studio           string `json:"studio,omitempty"`
}

// ScanResult captures MakeMKV scan output used for identification.
type ScanResult struct {
	Fingerprint string        `json:"fingerprint"`
	Titles      []Title       `json:"titles"`
	BDInfo      *BDInfoResult `json:"bd_info,omitempty"`
	RawOutput   string
}

// Executor abstracts command execution for the scanner.
type Executor interface {
	Run(ctx context.Context, binary string, args []string) ([]byte, error)
}

// commandExecutor executes commands using os/exec.
type commandExecutor struct{}

func (commandExecutor) Run(ctx context.Context, binary string, args []string) ([]byte, error) {
	cmd := exec.CommandContext(ctx, binary, args...) //nolint:gosec
	return cmd.Output()
}

// Scanner wraps MakeMKV info commands to gather disc metadata.
type Scanner struct {
	binary string
	exec   Executor
}

// NewScanner constructs a Scanner for the provided MakeMKV binary.
func NewScanner(binary string) *Scanner {
	return &Scanner{
		binary: strings.TrimSpace(binary),
		exec:   commandExecutor{},
	}
}

// NewScannerWithExecutor allows injecting a custom executor for testing.
func NewScannerWithExecutor(binary string, exec Executor) *Scanner {
	if exec == nil {
		exec = commandExecutor{}
	}
	return &Scanner{binary: strings.TrimSpace(binary), exec: exec}
}

// Scan executes MakeMKV to gather disc details, with bd_info fallback for title identification.
func (s *Scanner) Scan(ctx context.Context, device string) (*ScanResult, error) {
	if s.binary == "" {
		return nil, errors.New("makemkv binary not configured")
	}
	target := normalizeDeviceArg(device)

	args := []string{"-r", "--cache=1", "info", target, "--robot"}
	output, err := s.exec.Run(ctx, s.binary, args)
	if err != nil {
		type exitCoder interface{ ExitCode() int }
		var exitErr exitCoder
		if errors.As(err, &exitErr) {
			stderr := extractMakemkvStderr(err)
			clean := extractMakemkvErrorMessage(output, stderr)
			if clean != "" {
				return nil, fmt.Errorf("makemkv info failed (exit status %d): %s: %w", exitErr.ExitCode(), clean, err)
			}
			return nil, fmt.Errorf("makemkv info failed (exit status %d): %w", exitErr.ExitCode(), err)
		}
		return nil, fmt.Errorf("makemkv info failed: %w", err)
	}

	result, err := parseScanOutput(output)
	if err != nil {
		return nil, err
	}
	result.RawOutput = string(output)

	// If no titles found or the main title is generic/empty, try bd_info for better identification
	if len(result.Titles) == 0 || (len(result.Titles) > 0 && IsGenericLabel(result.Titles[0].Name)) {
		bdInfo := s.scanBDInfo(ctx, device)
		if bdInfo != nil {
			result.BDInfo = bdInfo
			// Update the first title with bd_info disc name if available
			if len(result.Titles) > 0 && bdInfo.DiscName != "" {
				result.Titles[0].Name = bdInfo.DiscName
			}
		}
	}

	return result, nil
}

// scanBDInfo runs bd_info command for enhanced disc identification.
func (s *Scanner) scanBDInfo(ctx context.Context, device string) *BDInfoResult {
	// Use raw device path if available, otherwise use the MakeMKV target format
	bdInfoDevice := extractDevicePath(device)
	if bdInfoDevice == "" {
		bdInfoDevice = normalizeDeviceArg(device)
		bdInfoDevice = strings.TrimPrefix(bdInfoDevice, "dev:")
	}

	output, err := s.exec.Run(ctx, "bd_info", []string{bdInfoDevice})
	if err != nil {
		// bd_info not available or failed - this is not a fatal error
		return nil
	}

	return parseBDInfoOutput(output)
}

// extractDevicePath extracts the raw device path from various device formats.
func extractDevicePath(device string) string {
	trimmed := strings.TrimSpace(device)
	if strings.HasPrefix(trimmed, "/dev/") {
		return trimmed
	}
	if strings.HasPrefix(trimmed, "dev:") {
		return strings.TrimPrefix(trimmed, "dev:")
	}
	if strings.HasPrefix(trimmed, "disc:") {
		// For disc: format, we can't easily extract the raw device
		// This would require additional system-specific logic
		return ""
	}
	return trimmed
}

// parseBDInfoOutput parses bd_info command output for disc metadata.
func parseBDInfoOutput(output []byte) *BDInfoResult {
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
					// Try to extract studio from provider data
					result.Studio = extractStudioFromProvider(provider)
				}
			}
		}
	}

	// Extract disc name from volume identifier if not directly provided
	if result.DiscName == "" && result.VolumeIdentifier != "" {
		result.DiscName = ExtractDiscNameFromVolumeID(result.VolumeIdentifier)
		// Try to extract year from disc name or volume identifier
		result.Year = extractYearFromIdentifier(result.DiscName, result.VolumeIdentifier)
	}

	return result
}

// ExtractDiscNameFromVolumeID cleans volume identifier to extract disc name.
func ExtractDiscNameFromVolumeID(volumeID string) string {
	if volumeID == "" {
		return ""
	}

	// Remove common prefixes and patterns
	title := volumeID
	title = regexp.MustCompile(`^\d+_`).ReplaceAllString(title, "")               // Remove leading numbers (00000095_)
	title = regexp.MustCompile(`(?i)_S\d+_DISC_\d+$`).ReplaceAllString(title, "") // Remove season/disc suffix
	title = regexp.MustCompile(`(?i)_TV$`).ReplaceAllString(title, "")            // Remove TV suffix
	title = strings.ReplaceAll(title, "_", " ")

	title = strings.TrimSpace(title)

	// Return empty string if result is just numbers or generic
	if title == "" || regexp.MustCompile(`^\d+$`).MatchString(title) {
		return ""
	}

	return title
}

// extractStudioFromProvider extracts studio name from provider data.
func extractStudioFromProvider(provider string) string {
	if provider == "" {
		return ""
	}

	// Known studio mappings from provider data
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

	// If no mapping found, return cleaned provider name
	cleaned := strings.TrimSpace(provider)
	if len(cleaned) <= 3 {
		return "" // Too short to be meaningful
	}
	return cleaned
}

// extractYearFromIdentifier attempts to extract a 4-digit year from disc identifiers.
func extractYearFromIdentifier(discName, volumeID string) int {
	// Common year patterns
	yearPattern := regexp.MustCompile(`\b(19|20)\d{2}\b`)

	// Try disc name first
	if discName != "" {
		if match := yearPattern.FindString(discName); match != "" {
			if year, err := strconv.Atoi(match); err == nil {
				return year
			}
		}
	}

	// Try volume identifier
	if volumeID != "" {
		if match := yearPattern.FindString(volumeID); match != "" {
			if year, err := strconv.Atoi(match); err == nil {
				return year
			}
		}
	}

	return 0
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
	}

	lowerLabel := strings.ToUpper(label)
	for _, pattern := range genericPatterns {
		if strings.Contains(lowerLabel, pattern) {
			return true
		}
	}

	// Check for labels that are just numbers
	if regexp.MustCompile(`^\d+$`).MatchString(label) {
		return true
	}

	// Check for very short alphanumeric codes (1-3 characters)
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

	// Fallback to the first non-empty line if no MSG payload matched heuristics.
	scanner = bufio.NewScanner(strings.NewReader(combined))
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line != "" {
			return line
		}
	}

	return combined
}

func parseScanOutput(data []byte) (*ScanResult, error) {
	text := strings.TrimSpace(string(data))
	if text == "" {
		return nil, errors.New("makemkv produced empty output")
	}

	lines := strings.Split(text, "\n")
	fingerprint := extractFingerprint(lines)
	titles := extractTitles(lines)

	return &ScanResult{Fingerprint: fingerprint, Titles: titles}, nil
}

var fingerprintPattern = regexp.MustCompile(`[0-9A-Fa-f]{16,}`)

func extractFingerprint(lines []string) string {
	for _, line := range lines {
		trimmed := strings.TrimSpace(line)
		if trimmed == "" {
			continue
		}
		if strings.Contains(strings.ToLower(trimmed), "fingerprint") {
			match := fingerprintPattern.FindString(trimmed)
			if match != "" {
				return strings.ToUpper(match)
			}
		}
	}

	for _, line := range lines {
		trimmed := strings.TrimSpace(line)
		if !strings.HasPrefix(trimmed, "CINFO:") {
			continue
		}
		payload := strings.TrimPrefix(trimmed, "CINFO:")
		parts := strings.SplitN(payload, ",", 3)
		if len(parts) < 3 {
			continue
		}
		if strings.TrimSpace(parts[0]) != "32" {
			continue
		}
		value := strings.TrimSpace(parts[2])
		value = strings.Trim(value, "\"")
		match := fingerprintPattern.FindString(value)
		if match != "" {
			return strings.ToUpper(match)
		}
	}

	match := fingerprintPattern.FindString(strings.Join(lines, "\n"))
	if match != "" {
		return strings.ToUpper(match)
	}
	return ""
}

func extractTitles(lines []string) []Title {
	type titleData struct {
		id       int
		name     string
		duration int
	}

	results := make(map[int]*titleData)

	for _, line := range lines {
		trimmed := strings.TrimSpace(line)
		if !strings.HasPrefix(trimmed, "TINFO:") {
			continue
		}
		payload := strings.TrimPrefix(trimmed, "TINFO:")
		parts := strings.SplitN(payload, ",", 4)
		if len(parts) < 4 {
			continue
		}
		titleID, err := strconv.Atoi(strings.TrimSpace(parts[0]))
		if err != nil {
			continue
		}
		attrID, err := strconv.Atoi(strings.TrimSpace(parts[1]))
		if err != nil {
			continue
		}
		value := strings.TrimSpace(parts[3])
		value = strings.Trim(value, "\"")
		entry, ok := results[titleID]
		if !ok {
			entry = &titleData{id: titleID}
			results[titleID] = entry
		}
		switch attrID {
		case 2:
			if value != "" {
				entry.name = value
			}
		case 9:
			entry.duration = parseDuration(value)
		}
	}

	ids := make([]int, 0, len(results))
	for id := range results {
		ids = append(ids, id)
	}
	sort.Ints(ids)

	titles := make([]Title, 0, len(ids))
	for _, id := range ids {
		entry := results[id]
		titles = append(titles, Title{ID: entry.id, Name: entry.name, Duration: entry.duration})
	}
	return titles
}

func parseDuration(value string) int {
	clean := value
	if strings.Contains(clean, ",\"") {
		parts := strings.SplitN(clean, ",\"", 2)
		clean = parts[1]
	}
	clean = strings.Trim(clean, "\"")
	if clean == "" {
		return 0
	}
	segments := strings.Split(clean, ":")
	if len(segments) != 3 {
		return 0
	}
	hours, err := strconv.Atoi(segments[0])
	if err != nil {
		return 0
	}
	minutes, err := strconv.Atoi(segments[1])
	if err != nil {
		return 0
	}
	seconds, err := strconv.Atoi(segments[2])
	if err != nil {
		return 0
	}
	return hours*3600 + minutes*60 + seconds
}
