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

type makeMKVInfoCommand interface {
	Info(ctx context.Context, device string) ([]byte, error)
}

type makeMKVOutputParser interface {
	Parse(data []byte) (*ScanResult, error)
}

type bdInfoCommand interface {
	Inspect(ctx context.Context, device string) ([]byte, error)
}

type bdInfoOutputParser interface {
	Parse(data []byte) *BDInfoResult
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

	makeMKVCmd    makeMKVInfoCommand
	makeMKVParser makeMKVOutputParser
	bdInfoCmd     bdInfoCommand
	bdInfoParser  bdInfoOutputParser
}

// NewScanner constructs a Scanner for the provided MakeMKV binary.
func NewScanner(binary string) *Scanner {
	return newScanner(strings.TrimSpace(binary), commandExecutor{})
}

// NewScannerWithExecutor allows injecting a custom executor for testing.
func NewScannerWithExecutor(binary string, exec Executor) *Scanner {
	if exec == nil {
		exec = commandExecutor{}
	}
	return newScanner(strings.TrimSpace(binary), exec)
}

func newScanner(binary string, exec Executor) *Scanner {
	return &Scanner{
		binary:        binary,
		makeMKVCmd:    newMakeMKVCommand(binary, exec),
		makeMKVParser: makeMKVParser{},
		bdInfoCmd:     newBDInfoCommand(exec),
		bdInfoParser:  bdInfoParser{},
	}
}

// Scan executes MakeMKV to gather disc details, with bd_info fallback for title identification.
func (s *Scanner) Scan(ctx context.Context, device string) (*ScanResult, error) {
	if s.binary == "" {
		return nil, errors.New("makemkv binary not configured")
	}

	output, err := s.makeMKVCmd.Info(ctx, device)
	if err != nil {
		return nil, err
	}

	result, err := s.makeMKVParser.Parse(output)
	if err != nil {
		return nil, err
	}
	result.RawOutput = string(output)

	if shouldQueryBDInfo(result) {
		if info := s.lookupBDInfo(ctx, device); info != nil {
			result.BDInfo = info
			if len(result.Titles) > 0 && info.DiscName != "" {
				result.Titles[0].Name = info.DiscName
			}
		}
	}

	return result, nil
}

func shouldQueryBDInfo(result *ScanResult) bool {
	if result == nil {
		return false
	}
	if len(result.Titles) == 0 {
		return true
	}
	return IsGenericLabel(result.Titles[0].Name)
}

func (s *Scanner) lookupBDInfo(ctx context.Context, device string) *BDInfoResult {
	if s.bdInfoCmd == nil || s.bdInfoParser == nil {
		return nil
	}

	output, err := s.bdInfoCmd.Inspect(ctx, device)
	if err != nil || len(output) == 0 {
		return nil
	}

	return s.bdInfoParser.Parse(output)
}

type makeMKVCommand struct {
	binary string
	exec   Executor
}

func newMakeMKVCommand(binary string, exec Executor) *makeMKVCommand {
	return &makeMKVCommand{binary: strings.TrimSpace(binary), exec: exec}
}

func (c *makeMKVCommand) Info(ctx context.Context, device string) ([]byte, error) {
	if c.binary == "" {
		return nil, errors.New("makemkv binary not configured")
	}
	args := []string{"-r", "--cache=1", "info", normalizeDeviceArg(device), "--robot"}
	output, err := c.exec.Run(ctx, c.binary, args)
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
	return output, nil
}

type bdInfoCommandRunner struct {
	exec Executor
}

func newBDInfoCommand(exec Executor) *bdInfoCommandRunner {
	return &bdInfoCommandRunner{exec: exec}
}

func (c *bdInfoCommandRunner) Inspect(ctx context.Context, device string) ([]byte, error) {
	if c == nil || c.exec == nil {
		return nil, errors.New("executor not configured")
	}

	bdInfoDevice := extractDevicePath(device)
	if bdInfoDevice == "" {
		bdInfoDevice = normalizeDeviceArg(device)
		bdInfoDevice = strings.TrimPrefix(bdInfoDevice, "dev:")
	}

	return c.exec.Run(ctx, "bd_info", []string{bdInfoDevice})
}

type makeMKVParser struct{}

func (makeMKVParser) Parse(data []byte) (*ScanResult, error) {
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

// ExtractDiscNameFromVolumeID cleans volume identifier to extract disc name.
func ExtractDiscNameFromVolumeID(volumeID string) string {
	if volumeID == "" {
		return ""
	}

	title := volumeID
	title = regexp.MustCompile(`^\d+_`).ReplaceAllString(title, "")               // Remove leading numbers (00000095_)
	title = regexp.MustCompile(`(?i)_S\d+_DISC_\d+$`).ReplaceAllString(title, "") // Remove season/disc suffix
	title = regexp.MustCompile(`(?i)_TV$`).ReplaceAllString(title, "")            // Remove TV suffix
	title = strings.ReplaceAll(title, "_", " ")

	title = strings.TrimSpace(title)

	if title == "" || regexp.MustCompile(`^\d+$`).MatchString(title) {
		return ""
	}

	return title
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
