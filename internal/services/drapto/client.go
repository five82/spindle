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
	"time"
)

var commandContext = exec.CommandContext

// ProgressUpdate captures Drapto progress events.
type ProgressUpdate struct {
	Percent float64
	Stage   string
	Message string
	ETA     time.Duration
	Speed   float64
	FPS     float64
	Bitrate string
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
	logDir string
}

// NewCLI constructs a CLI client using defaults.
func NewCLI(opts ...Option) *CLI {
	cli := &CLI{binary: "drapto"}
	for _, opt := range opts {
		opt(cli)
	}
	return cli
}

// WithLogDir configures the directory where Drapto should write log files.
func WithLogDir(dir string) Option {
	return func(c *CLI) {
		trimmed := strings.TrimSpace(dir)
		if trimmed != "" {
			c.logDir = trimmed
		}
	}
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

	args := []string{"encode", "--input", inputPath, "--output", cleanOutputDir}
	if logDir := strings.TrimSpace(c.logDir); logDir != "" {
		args = append(args, "--log-dir", logDir)
	}
	args = append(args, "--progress-json")
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
			Type    string   `json:"type"`
			Percent float64  `json:"percent"`
			Stage   string   `json:"stage"`
			Message string   `json:"message"`
			ETA     *float64 `json:"eta_seconds"`
			Speed   *float64 `json:"speed"`
			FPS     *float64 `json:"fps"`
			Bitrate string   `json:"bitrate"`
		}
		if err := json.Unmarshal(line, &payload); err != nil {
			continue
		}
		if progress != nil {
			update := ProgressUpdate{
				Percent: payload.Percent,
				Stage:   payload.Stage,
				Message: payload.Message,
				Bitrate: payload.Bitrate,
			}
			if payload.ETA != nil {
				update.ETA = time.Duration(*payload.ETA) * time.Second
			}
			if payload.Speed != nil {
				update.Speed = *payload.Speed
			}
			if payload.FPS != nil {
				update.FPS = *payload.FPS
			}
			progress(update)
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
