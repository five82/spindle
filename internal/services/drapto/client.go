package drapto

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os/exec"
	"path/filepath"
	"strings"
)

var commandContext = exec.CommandContext

// ProgressUpdate captures Drapto progress events.
type ProgressUpdate struct {
	Percent float64
	Stage   string
	Message string
}

// Client defines Drapto encoding behaviour.
type Client interface {
	Encode(ctx context.Context, inputPath, outputDir string, progress func(ProgressUpdate)) (string, error)
}

// Option configures the CLI client.
type Option func(*CLI)

// WithBinary overrides the default binary name.
func WithBinary(binary string) Option {
	return func(c *CLI) {
		if binary != "" {
			c.binary = binary
		}
	}
}

// CLI wraps the drapto command-line encoder.
type CLI struct {
	binary string
}

// NewCLI constructs a CLI client using defaults.
func NewCLI(opts ...Option) *CLI {
	cli := &CLI{binary: "drapto"}
	for _, opt := range opts {
		opt(cli)
	}
	return cli
}

// Encode launches drapto encode and returns the output path.
func (c *CLI) Encode(ctx context.Context, inputPath, outputDir string, progress func(ProgressUpdate)) (string, error) {
	if inputPath == "" {
		return "", errors.New("input path required")
	}
	if outputDir == "" {
		return "", errors.New("output directory required")
	}

	cleanOutputDir := strings.TrimSpace(outputDir)
	if cleanOutputDir == "" {
		return "", errors.New("output directory required")
	}

	base := filepath.Base(inputPath)
	stem := strings.TrimSuffix(base, filepath.Ext(base))
	if stem == "" {
		stem = base
	}
	outputPath := filepath.Join(cleanOutputDir, stem+".mkv")

	args := []string{"encode", "--input", inputPath, "--output", cleanOutputDir, "--progress-json"}
	cmd := commandContext(ctx, c.binary, args...) //nolint:gosec
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return "", fmt.Errorf("stdout pipe: %w", err)
	}
	cmd.Stderr = cmd.Stdout
	if err := cmd.Start(); err != nil {
		return "", fmt.Errorf("start drapto: %w", err)
	}

	scanner := bufio.NewScanner(stdout)
	for scanner.Scan() {
		line := scanner.Bytes()
		var payload struct {
			Percent float64 `json:"percent"`
			Stage   string  `json:"stage"`
			Message string  `json:"message"`
		}
		if err := json.Unmarshal(line, &payload); err != nil {
			continue
		}
		if progress != nil {
			progress(ProgressUpdate{Percent: payload.Percent, Stage: payload.Stage, Message: payload.Message})
		}
	}
	if err := scanner.Err(); err != nil {
		return "", fmt.Errorf("read drapto output: %w", err)
	}

	if err := cmd.Wait(); err != nil {
		return "", fmt.Errorf("drapto encode failed: %w", err)
	}

	return outputPath, nil
}

var _ Client = (*CLI)(nil)
