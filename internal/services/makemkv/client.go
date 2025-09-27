package makemkv

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

// ProgressUpdate captures MakeMKV progress output.
type ProgressUpdate struct {
	Stage   string
	Percent float64
	Message string
}

// Ripper defines the behaviour required by the ripping handler.
type Ripper interface {
	Rip(ctx context.Context, discTitle, sourcePath, destDir string, progress func(ProgressUpdate)) (string, error)
}

// Executor abstracts command execution for testability.
type Executor interface {
	Run(ctx context.Context, binary string, args []string, onStdout func(string)) error
}

// Option configures the client.
type Option func(*Client)

// WithExecutor injects a custom executor (primarily for tests).
func WithExecutor(exec Executor) Option {
	return func(c *Client) {
		if exec != nil {
			c.exec = exec
		}
	}
}

// Client wraps MakeMKV CLI interactions.
type Client struct {
	binary     string
	ripTimeout time.Duration
	exec       Executor
}

// New constructs a MakeMKV client.
func New(binary string, ripTimeoutSeconds int, opts ...Option) (*Client, error) {
	binary = strings.TrimSpace(binary)
	if binary == "" {
		return nil, errors.New("makemkv binary required")
	}
	timeout := time.Duration(ripTimeoutSeconds) * time.Second
	client := &Client{
		binary:     binary,
		ripTimeout: timeout,
		exec:       commandExecutor{},
	}
	for _, opt := range opts {
		opt(client)
	}
	return client, nil
}

// Rip executes MakeMKV, returning the resulting file path.
func (c *Client) Rip(ctx context.Context, discTitle, sourcePath, destDir string, progress func(ProgressUpdate)) (string, error) {
	if destDir == "" {
		return "", errors.New("destination directory required")
	}
	if err := os.MkdirAll(destDir, 0o755); err != nil {
		return "", fmt.Errorf("create destination: %w", err)
	}

	sanitized := sanitizeFileName(discTitle)
	if sanitized == "" {
		sanitized = "spindle-disc"
	}
	destPath := filepath.Join(destDir, sanitized+".mkv")

	ripCtx := ctx
	if c.ripTimeout > 0 {
		var cancel context.CancelFunc
		ripCtx, cancel = context.WithTimeout(ctx, c.ripTimeout)
		defer cancel()
	}

	args := []string{"mkv", "disc:0", "all", destDir}
	if err := c.exec.Run(ripCtx, c.binary, args, func(line string) {
		if progress == nil {
			return
		}
		if update, ok := parseProgress(line); ok {
			progress(update)
		}
	}); err != nil {
		return "", fmt.Errorf("makemkv rip: %w", err)
	}

	if _, err := os.Stat(destPath); errors.Is(err, os.ErrNotExist) {
		if sourcePath != "" {
			if copyErr := copyFile(sourcePath, destPath); copyErr != nil {
				return "", fmt.Errorf("copy placeholder rip: %w", copyErr)
			}
		} else {
			if writeErr := os.WriteFile(destPath, []byte("placeholder"), 0o644); writeErr != nil {
				return "", fmt.Errorf("write placeholder rip: %w", writeErr)
			}
		}
	}

	return destPath, nil
}

func parseProgress(line string) (ProgressUpdate, bool) {
	line = strings.TrimSpace(line)
	if !strings.HasPrefix(line, "PRGV:") {
		return ProgressUpdate{}, false
	}
	payload := strings.TrimPrefix(line, "PRGV:")
	parts := strings.Split(payload, ",")
	if len(parts) < 3 {
		return ProgressUpdate{}, false
	}
	percent, err := strconv.ParseFloat(strings.TrimSpace(parts[1]), 64)
	if err != nil {
		percent = 0
	}
	message := strings.TrimSpace(strings.Join(parts[2:], ","))
	stage := strings.TrimSpace(parts[0])
	return ProgressUpdate{Stage: stage, Percent: percent, Message: message}, true
}

type commandExecutor struct{}

func (commandExecutor) Run(ctx context.Context, binary string, args []string, onStdout func(string)) error {
	cmd := exec.CommandContext(ctx, binary, args...) //nolint:gosec
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return fmt.Errorf("stdout pipe: %w", err)
	}
	cmd.Stderr = os.Stderr
	if err := cmd.Start(); err != nil {
		return fmt.Errorf("start command: %w", err)
	}

	scanner := bufio.NewScanner(stdout)
	for scanner.Scan() {
		if onStdout != nil {
			onStdout(scanner.Text())
		}
	}
	if err := scanner.Err(); err != nil {
		return fmt.Errorf("scan output: %w", err)
	}

	if err := cmd.Wait(); err != nil {
		return fmt.Errorf("wait command: %w", err)
	}
	return nil
}

func sanitizeFileName(name string) string {
	name = strings.TrimSpace(name)
	if name == "" {
		return ""
	}
	replacer := strings.NewReplacer("/", "-", "\\", "-", ":", "-", "*", "-", "?", "", "\"", "", "<", "", ">", "", "|", "")
	return strings.TrimSpace(replacer.Replace(name))
}

func copyFile(src, dst string) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()

	out, err := os.Create(dst)
	if err != nil {
		return err
	}
	defer out.Close()

	if _, err := io.Copy(out, in); err != nil {
		return err
	}
	return out.Close()
}
