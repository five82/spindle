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

// ScanResult captures MakeMKV scan output used for identification.
type ScanResult struct {
	Fingerprint string  `json:"fingerprint"`
	Titles      []Title `json:"titles"`
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

// Scan executes MakeMKV to gather disc details.
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
	return result, nil
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
