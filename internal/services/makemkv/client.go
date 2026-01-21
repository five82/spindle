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
	"sort"
	"strconv"
	"strings"
	"sync"
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
	sanitized := sanitizeFileName(discTitle)
	if sanitized == "" {
		sanitized = "spindle-disc"
	}
	destPath := filepath.Join(destDir, sanitized+".mkv")

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

	if err := c.exec.Run(ctx, c.binary, args, func(line string) {
		if progress == nil {
			return
		}
		if update, ok := parseProgress(line); ok {
			progress(update)
		}
	}); err != nil {
		return "", fmt.Errorf("makemkv rip: %w", err)
	}

	candidates, err := gatherMKVEntries(destDir)
	if err != nil {
		return "", fmt.Errorf("inspect rip outputs: %w", err)
	}
	if len(candidates) > 0 {
		best := selectPreferredMKV(candidates, titleIDs)
		if best == nil {
			best = newestEntry(candidates)
		}
		if best != nil {
			if skipRename {
				destPath = best.path
			} else if len(titleIDs) <= 1 {
				if err := replaceFile(best.path, destPath); err != nil {
					return "", err
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
		return "", fmt.Errorf("makemkv produced no output file; check disc for read errors")
	}

	return destPath, nil
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
		expected := strings.ToLower(fmt.Sprintf("title_t%02d.mkv", titleIDs[0]))
		for i := range files {
			if strings.ToLower(filepath.Base(files[i].path)) == expected {
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

func sanitizeFileName(name string) string {
	name = strings.TrimSpace(name)
	if name == "" {
		return ""
	}
	replacer := strings.NewReplacer("/", "-", "\\", "-", ":", "-", "*", "-", "?", "", "\"", "", "<", "", ">", "", "|", "")
	return strings.TrimSpace(replacer.Replace(name))
}
