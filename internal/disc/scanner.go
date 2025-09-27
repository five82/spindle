package disc

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os/exec"
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

// ErrFingerprintMissing is returned when MakeMKV output lacks a fingerprint.
var ErrFingerprintMissing = errors.New("makemkv scan missing fingerprint")

// Scan executes MakeMKV to gather disc details.
func (s *Scanner) Scan(ctx context.Context, device string) (*ScanResult, error) {
	if s.binary == "" {
		return nil, errors.New("makemkv binary not configured")
	}
	if device == "" {
		device = "disc:0"
	}

	args := []string{"-r", "--cache=1", "--messages", "json", "info", device}
	output, err := s.exec.Run(ctx, s.binary, args)
	if err != nil {
		return nil, fmt.Errorf("makemkv info failed: %w", err)
	}

	result, err := parseScanOutput(output)
	if err != nil {
		return nil, err
	}
	result.RawOutput = string(output)
	if result.Fingerprint == "" {
		return nil, ErrFingerprintMissing
	}
	return result, nil
}

func parseScanOutput(data []byte) (*ScanResult, error) {
	scanner := bufio.NewScanner(strings.NewReader(string(data)))
	scanner.Buffer(make([]byte, 0, 64*1024), 1024*1024)

	var payload struct {
		Fingerprint string            `json:"fingerprint"`
		Titles      []json.RawMessage `json:"titles"`
	}
	combined := strings.Builder{}
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" {
			continue
		}
		if strings.HasPrefix(line, "MSG:") {
			// Skip legacy log lines
			continue
		}
		combined.WriteString(line)
	}
	if err := scanner.Err(); err != nil {
		return nil, fmt.Errorf("scan output read: %w", err)
	}

	if combined.Len() == 0 {
		return nil, errors.New("makemkv produced empty output")
	}

	if err := json.Unmarshal([]byte(combined.String()), &payload); err != nil {
		return nil, fmt.Errorf("parse makemkv json: %w", err)
	}

	titles := make([]Title, 0, len(payload.Titles))
	for _, raw := range payload.Titles {
		var entry struct {
			ID       int    `json:"id"`
			Name     string `json:"name"`
			Duration int    `json:"length"`
		}
		if err := json.Unmarshal(raw, &entry); err != nil {
			continue
		}
		titles = append(titles, Title{ID: entry.ID, Name: entry.Name, Duration: entry.Duration})
	}

	return &ScanResult{Fingerprint: payload.Fingerprint, Titles: titles}, nil
}
