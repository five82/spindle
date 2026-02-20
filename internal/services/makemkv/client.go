package makemkv

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"spindle/internal/logging"
	"spindle/internal/textutil"
)

// ProgressUpdate captures MakeMKV progress output.
type ProgressUpdate struct {
	Stage   string
	Percent float64
	Message string
}

// Ripper defines the behaviour required by the ripping handler.
type Ripper interface {
	Rip(ctx context.Context, discTitle, destDir string, titleIDs []int, progress func(ProgressUpdate)) (string, error)
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

// WithLogger injects a logger for diagnostic output during ripping.
func WithLogger(logger *slog.Logger) Option {
	return func(c *Client) {
		c.logger = logger
	}
}

// Client wraps MakeMKV CLI interactions.
type Client struct {
	binary     string
	ripTimeout time.Duration
	exec       Executor
	logger     *slog.Logger
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
func (c *Client) Rip(ctx context.Context, discTitle, destDir string, titleIDs []int, progress func(ProgressUpdate)) (string, error) {
	if destDir == "" {
		return "", errors.New("destination directory required")
	}
	if err := os.RemoveAll(destDir); err != nil && !errors.Is(err, os.ErrNotExist) {
		return "", fmt.Errorf("prepare destination: %w", err)
	}
	if err := os.MkdirAll(destDir, 0o755); err != nil {
		return "", fmt.Errorf("create destination: %w", err)
	}

	titleIDs = normalizeTitleIDs(titleIDs)
	multiTitle := len(titleIDs) > 1

	ripCtx := ctx
	if c.ripTimeout > 0 {
		var cancel context.CancelFunc
		ripCtx, cancel = context.WithTimeout(ctx, c.ripTimeout)
		defer cancel()
	}

	if len(titleIDs) == 0 {
		return c.executeRip(ripCtx, discTitle, destDir, nil, false, progress)
	}

	var lastPath string
	for _, id := range titleIDs {
		path, err := c.executeRip(ripCtx, discTitle, destDir, []int{id}, multiTitle, progress)
		if err != nil {
			return "", fmt.Errorf("makemkv rip title %d: %w", id, err)
		}
		lastPath = path
	}
	return lastPath, nil
}

func (c *Client) executeRip(ctx context.Context, discTitle, destDir string, titleIDs []int, skipRename bool, progress func(ProgressUpdate)) (string, error) {
	sanitized := textutil.SanitizeFileName(discTitle)
	if sanitized == "" {
		sanitized = "spindle-disc"
	}
	destPath := filepath.Join(destDir, sanitized+".mkv")

	if c.logger != nil {
		c.logger.Debug("makemkv rip starting",
			slog.String("disc_title", discTitle),
			slog.String("sanitized_title", sanitized),
			slog.String("expected_output", destPath),
			slog.String("dest_dir", destDir),
			slog.Bool("skip_rename", skipRename),
			slog.Any("title_ids", titleIDs),
		)
	}

	args := []string{"--robot"}
	if progress != nil {
		args = append(args, "--progress=-same")
	}
	args = append(args, "mkv", "disc:0")
	if len(titleIDs) == 0 {
		args = append(args, "all")
	} else {
		args = append(args, strconv.Itoa(titleIDs[0]))
	}
	args = append(args, destDir)

	// Start file size monitor goroutine
	monitorCtx, cancelMonitor := context.WithCancel(ctx)
	defer cancelMonitor()
	go c.monitorOutputSize(monitorCtx, destDir)

	// Track MakeMKV messages for diagnostic purposes
	var messages []string
	var messagesMu sync.Mutex

	if err := c.exec.Run(ctx, c.binary, args, func(line string) {
		// Capture MSG lines for diagnostics (errors, warnings, info)
		if strings.HasPrefix(line, "MSG:") {
			messagesMu.Lock()
			messages = append(messages, line)
			messagesMu.Unlock()
			if c.logger != nil {
				c.logger.Debug("makemkv message", slog.String("msg", line))
			}
		}
		if progress == nil {
			return
		}
		if update, ok := parseProgress(line); ok {
			progress(update)
		}
	}); err != nil {
		// Log captured messages on failure
		if c.logger != nil && len(messages) > 0 {
			c.logger.Debug("makemkv messages before failure",
				slog.Int("message_count", len(messages)),
				slog.Any("messages", messages),
			)
		}
		return "", fmt.Errorf("makemkv rip: %w", err)
	}

	candidates, err := gatherMKVEntries(destDir)
	if err != nil {
		return "", fmt.Errorf("inspect rip outputs: %w", err)
	}

	// Log what files we found in the output directory
	if c.logger != nil {
		if len(candidates) == 0 {
			c.logger.Debug("no mkv files found in output directory",
				slog.String("dest_dir", destDir),
			)
			// List all files in directory for debugging
			if items, readErr := os.ReadDir(destDir); readErr == nil {
				var names []string
				for _, item := range items {
					info, _ := item.Info()
					if info != nil {
						names = append(names, fmt.Sprintf("%s (%d bytes)", item.Name(), info.Size()))
					} else {
						names = append(names, item.Name())
					}
				}
				c.logger.Debug("directory contents",
					slog.String("dest_dir", destDir),
					slog.Any("files", names),
				)
			}
		} else {
			var candidateInfo []string
			for _, cand := range candidates {
				candidateInfo = append(candidateInfo, fmt.Sprintf("%s (%d bytes, %s)",
					filepath.Base(cand.path), cand.size, cand.modTime.Format(time.RFC3339)))
			}
			c.logger.Debug("found mkv candidates",
				slog.Int("count", len(candidates)),
				slog.Any("candidates", candidateInfo),
			)
		}
	}

	if len(candidates) > 0 {
		best := selectPreferredMKV(candidates, titleIDs)
		if best == nil {
			best = newestEntry(candidates)
		}

		// Log selection decision
		if c.logger != nil && best != nil {
			expectedSuffix := ""
			if len(titleIDs) == 1 {
				expectedSuffix = fmt.Sprintf("_t%02d.mkv", titleIDs[0])
			}
			c.logger.Debug("selected output file",
				slog.String("selected", filepath.Base(best.path)),
				slog.Int64("size_bytes", best.size),
				slog.String("expected_suffix", expectedSuffix),
				slog.String("target_path", destPath),
				slog.Bool("will_rename", !skipRename && len(titleIDs) <= 1 && best.path != destPath),
			)
		}

		if best != nil {
			if skipRename {
				destPath = best.path
			} else if len(titleIDs) <= 1 {
				if c.logger != nil && best.path != destPath {
					c.logger.Debug("renaming output file",
						slog.String("from", best.path),
						slog.String("to", destPath),
					)
				}
				if err := replaceFile(best.path, destPath); err != nil {
					if c.logger != nil {
						c.logger.Debug("rename failed",
							slog.String("from", best.path),
							slog.String("to", destPath),
							slog.String("error", err.Error()),
						)
					}
					return "", err
				}
				// Verify rename succeeded
				if c.logger != nil {
					if info, statErr := os.Stat(destPath); statErr != nil {
						c.logger.Debug("rename succeeded but file not found at destination",
							slog.String("dest_path", destPath),
							slog.String("error", statErr.Error()),
						)
					} else {
						c.logger.Debug("rename verified",
							slog.String("dest_path", destPath),
							slog.Int64("size_bytes", info.Size()),
						)
					}
				}
				for _, file := range candidates {
					if file.path == destPath {
						continue
					}
					_ = os.Remove(file.path)
				}
			} else {
				destPath = best.path
			}
		}
	}

	if _, err := os.Stat(destPath); errors.Is(err, os.ErrNotExist) {
		// Enhanced error with diagnostic details
		if c.logger != nil {
			c.logger.Debug("output validation failed",
				slog.String("expected_path", destPath),
				slog.Int("candidates_found", len(candidates)),
				slog.Int("message_count", len(messages)),
			)
			// Log the last few MakeMKV messages which might explain the failure
			if len(messages) > 0 {
				lastMessages := messages
				if len(lastMessages) > 10 {
					lastMessages = lastMessages[len(lastMessages)-10:]
				}
				c.logger.Debug("recent makemkv messages", slog.Any("messages", lastMessages))
			}
		}
		// Build informative error based on what we found
		if len(candidates) == 0 {
			return "", errors.New("makemkv produced no output file; no mkv files found in output directory; check disc for read errors")
		}
		// We found candidates but destPath doesn't exist - this indicates a rename/processing bug
		var names []string
		for _, c := range candidates {
			names = append(names, filepath.Base(c.path))
		}
		return "", fmt.Errorf("output file missing after processing; found %d candidate(s) [%s] but expected %s; this may indicate a spindle bug in file handling",
			len(candidates), strings.Join(names, ", "), filepath.Base(destPath))
	}

	if c.logger != nil {
		info, _ := os.Stat(destPath)
		var sizeBytes int64
		if info != nil {
			sizeBytes = info.Size()
		}
		c.logger.Debug("rip output validated",
			slog.String("path", destPath),
			slog.Int64("size_bytes", sizeBytes),
		)
	}

	return destPath, nil
}

// monitorOutputSize periodically logs the size of files being written to destDir.
func (c *Client) monitorOutputSize(ctx context.Context, destDir string) {
	if c.logger == nil {
		return
	}
	ticker := time.NewTicker(30 * time.Second)
	defer ticker.Stop()

	var lastSize int64
	var lastFile string

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			entries, err := gatherMKVEntries(destDir)
			if err != nil || len(entries) == 0 {
				continue
			}
			// Find the largest/newest file (likely the one being written)
			var current *mkvEntry
			for i := range entries {
				if current == nil || entries[i].size > current.size {
					current = &entries[i]
				}
			}
			if current == nil {
				continue
			}
			// Only log if there's meaningful change
			currentFile := filepath.Base(current.path)
			if current.size != lastSize || currentFile != lastFile {
				c.logger.Debug("rip progress file size",
					slog.String("file", currentFile),
					slog.Int64("size_bytes", current.size),
					slog.String("size_human", logging.FormatBytes(current.size)),
				)
				lastSize = current.size
				lastFile = currentFile
			}
		}
	}
}

func newestEntry(entries []mkvEntry) *mkvEntry {
	if len(entries) == 0 {
		return nil
	}
	newest := 0
	for i := 1; i < len(entries); i++ {
		if entries[i].modTime.After(entries[newest].modTime) {
			newest = i
		}
	}
	return &entries[newest]
}

type mkvEntry struct {
	path    string
	size    int64
	modTime time.Time
}

func gatherMKVEntries(dir string) ([]mkvEntry, error) {
	items, err := os.ReadDir(dir)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, nil
		}
		return nil, err
	}
	result := make([]mkvEntry, 0, len(items))
	for _, item := range items {
		if item.IsDir() {
			continue
		}
		if !strings.HasSuffix(strings.ToLower(item.Name()), ".mkv") {
			continue
		}
		info, err := item.Info()
		if err != nil {
			continue
		}
		result = append(result, mkvEntry{
			path:    filepath.Join(dir, item.Name()),
			size:    info.Size(),
			modTime: info.ModTime(),
		})
	}
	return result, nil
}

func selectPreferredMKV(files []mkvEntry, titleIDs []int) *mkvEntry {
	if len(files) == 0 {
		return nil
	}
	if len(titleIDs) == 1 {
		// MakeMKV creates files like "{disc_name}_t{NN}.mkv" where NN is the title number
		suffix := strings.ToLower(fmt.Sprintf("_t%02d.mkv", titleIDs[0]))
		for i := range files {
			if strings.HasSuffix(strings.ToLower(files[i].path), suffix) {
				return &files[i]
			}
		}
	}
	bestIdx := 0
	for i := 1; i < len(files); i++ {
		if files[i].size > files[bestIdx].size {
			bestIdx = i
			continue
		}
		if files[i].size == files[bestIdx].size && files[i].modTime.After(files[bestIdx].modTime) {
			bestIdx = i
		}
	}
	return &files[bestIdx]
}

func replaceFile(src, dest string) error {
	if src == dest {
		return nil
	}
	if err := os.Remove(dest); err != nil && !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("remove existing rip target: %w", err)
	}
	if err := os.Rename(src, dest); err != nil {
		return fmt.Errorf("rename rip output: %w", err)
	}
	return nil
}

func normalizeTitleIDs(ids []int) []int {
	if len(ids) == 0 {
		return nil
	}
	uniq := make([]int, 0, len(ids))
	seen := make(map[int]struct{}, len(ids))
	for _, id := range ids {
		if id < 0 {
			continue
		}
		if _, ok := seen[id]; ok {
			continue
		}
		seen[id] = struct{}{}
		uniq = append(uniq, id)
	}
	if len(uniq) == 0 {
		return nil
	}
	sort.Ints(uniq)
	return uniq
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
	first := strings.TrimSpace(parts[0])
	second := strings.TrimSpace(parts[1])
	third := strings.TrimSpace(parts[2])

	var update ProgressUpdate

	if _, err := strconv.Atoi(first); err == nil {
		// Robot format: PRGV:current,total,max[,message]
		total, totalErr := strconv.ParseFloat(second, 64)
		maximum, err := strconv.ParseFloat(third, 64)
		if err != nil || maximum <= 0 {
			return ProgressUpdate{}, false
		}
		if totalErr != nil {
			total = 0
		}
		percent := (total / maximum) * 100
		update = ProgressUpdate{Stage: "Ripping", Percent: percent}
		if len(parts) > 3 {
			update.Message = strings.TrimSpace(strings.Join(parts[3:], ","))
		}
		// Preserve the raw totals in the message if no message present to aid debugging.
		if update.Message == "" {
			update.Message = fmt.Sprintf("Progress %.2f%% (%0.f/%0.f)", percent, total, maximum)
		}
		return update, true
	}

	percent, err := strconv.ParseFloat(second, 64)
	if err != nil {
		percent = 0
	}
	message := strings.TrimSpace(strings.Join(parts[2:], ","))
	stage := first
	update = ProgressUpdate{Stage: stage, Percent: percent, Message: message}
	return update, true
}

type commandExecutor struct{}

func (commandExecutor) Run(ctx context.Context, binary string, args []string, onStdout func(string)) error {
	cmd := exec.CommandContext(ctx, binary, args...) //nolint:gosec
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return fmt.Errorf("stdout pipe: %w", err)
	}
	stderr, err := cmd.StderrPipe()
	if err != nil {
		return fmt.Errorf("stderr pipe: %w", err)
	}
	if err := cmd.Start(); err != nil {
		return fmt.Errorf("start command: %w", err)
	}

	var wg sync.WaitGroup
	var scanErr error
	var once sync.Once

	scan := func(r io.Reader, forward func(string)) {
		defer wg.Done()
		scanner := bufio.NewScanner(r)
		for scanner.Scan() {
			forward(scanner.Text())
		}
		if err := scanner.Err(); err != nil {
			once.Do(func() {
				scanErr = err
			})
		}
	}

	forward := func(line string) {
		if onStdout != nil {
			onStdout(line)
			return
		}
		fmt.Fprintln(os.Stderr, line)
	}

	wg.Add(2)
	go scan(stdout, forward)
	go scan(stderr, forward)

	wg.Wait()
	if scanErr != nil {
		_ = cmd.Process.Kill()
		return fmt.Errorf("scan output: %w", scanErr)
	}

	if err := cmd.Wait(); err != nil {
		return fmt.Errorf("wait command: %w", err)
	}
	return nil
}
