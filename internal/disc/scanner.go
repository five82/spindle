package disc

import (
	"context"
	"errors"
	"fmt"
	"os/exec"
	"strings"
)

// Title represents a MakeMKV title entry.
type Title struct {
	ID       int     `json:"id"`
	Name     string  `json:"name"`
	Duration int     `json:"duration"`
	Tracks   []Track `json:"tracks,omitempty"`
}

// BDInfoResult captures bd_info command output for enhanced disc identification.
type BDInfoResult struct {
	VolumeIdentifier string `json:"volume_identifier"`
	DiscName         string `json:"disc_name"`
	Provider         string `json:"provider"`
	DiscID           string `json:"disc_id"`
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
