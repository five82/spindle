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
	Rip(ctx context.Context, device, discTitle, destDir string, titleIDs []int, progress func(ProgressUpdate)) (string, error)
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
func (c *Client) Rip(ctx context.Context, device, discTitle, destDir string, titleIDs []int, progress func(ProgressUpdate)) (string, error) {
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

	deviceArg := normalizeDeviceArg(device)

	if len(titleIDs) == 0 {
		return c.executeRip(ripCtx, deviceArg, discTitle, destDir, nil, false, progress)
	}

	var lastPath string
	for _, id := range titleIDs {
		path, err := c.executeRip(ripCtx, deviceArg, discTitle, destDir, []int{id}, multiTitle, progress)
		if err != nil {
			return "", fmt.Errorf("makemkv rip title %d: %w", id, err)
		}
		lastPath = path
	}
	return lastPath, nil
}

func (c *Client) executeRip(ctx context.Context, deviceArg, discTitle, destDir string, titleIDs []int, skipRename bool, progress func(ProgressUpdate)) (string, error) {
	sanitized := textutil.SanitizeFileName(discTitle)
	if sanitized == "" {
		sanitized = "spindle-disc"
	}
	destPath := filepath.Join(destDir, sanitized+".mkv")

	if c.logger != nil {
		c.logger.Debug("makemkv rip starting",
			slog.String("device", deviceArg),
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
	args = append(args, "mkv", deviceArg)
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
	tracker := &progressTracker{}

	if err := c.exec.Run(ctx, c.binary, args, func(line string) {
		// Capture MSG lines for diagnostics (errors, warnings, info)
		if strings.HasPrefix(line, "MSG:") {
			messagesMu.Lock()
			messages = append(messages, line)
			messagesMu.Unlock()
			if c.logger != nil {
				code := parseMSGCode(line)
				text := parseMSGText(line)
				if code >= 5000 {
					c.logger.Warn("makemkv disc error",
						slog.String("event_type", "makemkv_disc_error"),
						slog.String("error_hint", "disc may have read errors; check physical media"),
						slog.String("impact", "rip may produce corrupted or incomplete output"),
						slog.Int("msg_code", code),
						slog.String("msg_text", text),
					)
				} else {
					c.logger.Debug("makemkv message", slog.String("msg", line))
				}
			}
		}
		if progress == nil {
			return
		}
		if update, ok := tracker.parseLine(line); ok {
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
			reason := "largest_file"
			if len(titleIDs) == 1 {
				suffix := strings.ToLower(fmt.Sprintf("_t%02d.mkv", titleIDs[0]))
				if strings.HasSuffix(strings.ToLower(best.path), suffix) {
					reason = "title_id_match"
				}
			}
			c.logger.Info("rip output file selection",
				slog.String("decision_type", "rip_output_selection"),
				slog.String("decision_result", "selected"),
				slog.String("decision_reason", reason),
				slog.String("ripped_file", filepath.Base(best.path)),
				slog.Int64("ripped_size_bytes", best.size),
				slog.Int("candidate_count", len(candidates)),
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

// parseMSGCode extracts the numeric code from a MakeMKV MSG line.
// Format: MSG:code,flags,count,"message","format",...
// Returns -1 if the line is not a valid MSG line.
func parseMSGCode(line string) int {
	if !strings.HasPrefix(line, "MSG:") {
		return -1
	}
	payload := strings.TrimPrefix(line, "MSG:")
	comma := strings.IndexByte(payload, ',')
	if comma < 0 {
		return -1
	}
	code, err := strconv.Atoi(strings.TrimSpace(payload[:comma]))
	if err != nil {
		return -1
	}
	return code
}

// parseMSGText extracts the human-readable message from a MakeMKV MSG line.
// The message is the 4th field (index 3), typically enclosed in quotes.
// Returns an empty string if the line is malformed.
func parseMSGText(line string) string {
	if !strings.HasPrefix(line, "MSG:") {
		return ""
	}
	payload := strings.TrimPrefix(line, "MSG:")
	// Find the 4th comma-separated field (index 3).
	// Fields may contain quoted strings with commas inside, so we track quotes.
	fieldIdx := 0
	inQuote := false
	start := 0
	for i := 0; i < len(payload); i++ {
		switch payload[i] {
		case '"':
			inQuote = !inQuote
		case ',':
			if !inQuote {
				fieldIdx++
				if fieldIdx == 3 {
					start = i + 1
				}
				if fieldIdx == 4 {
					return trimMSGField(payload[start:i])
				}
			}
		}
	}
	// If we reached end of string while in the 4th field
	if fieldIdx >= 3 {
		return trimMSGField(payload[start:])
	}
	return ""
}

// trimMSGField strips surrounding whitespace and quotes from a MSG field value.
func trimMSGField(s string) string {
	s = strings.TrimSpace(s)
	if len(s) >= 2 && s[0] == '"' && s[len(s)-1] == '"' {
		s = s[1 : len(s)-1]
	}
	return s
}

// progressTracker accumulates MakeMKV phase context from PRGT lines so that
// PRGV progress values can be attributed to the correct stage (Analyzing vs
// Ripping). Without this, all progress is reported as "Ripping" even during
// the disc analysis phase that precedes actual ripping.
type progressTracker struct {
	currentPhase string
}

// parseLine processes a single line of MakeMKV robot output. PRGT lines
// update internal phase state. PRGV lines produce a ProgressUpdate with the
// stage resolved from the most recent PRGT context.
func (t *progressTracker) parseLine(line string) (ProgressUpdate, bool) {
	line = strings.TrimSpace(line)

	// Track phase context from PRGT lines (Progress Title).
	if strings.HasPrefix(line, "PRGT:") {
		t.currentPhase = parsePRGTName(line)
		return ProgressUpdate{}, false
	}

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
		update = ProgressUpdate{Stage: t.resolveStage(), Percent: percent}
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

// resolveStage maps the current PRGT phase name to a user-facing stage.
// MakeMKV uses names like "Saving to MKV file" for ripping and various other
// names (e.g. "Analyzing") for pre-rip analysis.
func (t *progressTracker) resolveStage() string {
	phase := strings.ToLower(t.currentPhase)
	if strings.Contains(phase, "sav") {
		return "Ripping"
	}
	if phase != "" {
		return "Analyzing"
	}
	// Default to Analyzing when no PRGT has been seen yet - the first PRGV
	// lines arrive during disc analysis before MakeMKV begins saving.
	return "Analyzing"
}

// parsePRGTName extracts the display name from a PRGT line.
// Format: PRGT:code,id,"name"
func parsePRGTName(line string) string {
	payload := strings.TrimPrefix(line, "PRGT:")
	// The name is the third comma-separated field.
	idx := strings.IndexByte(payload, ',')
	if idx < 0 {
		return ""
	}
	rest := payload[idx+1:]
	idx = strings.IndexByte(rest, ',')
	if idx < 0 {
		return ""
	}
	name := strings.TrimSpace(rest[idx+1:])
	if len(name) >= 2 && name[0] == '"' && name[len(name)-1] == '"' {
		name = name[1 : len(name)-1]
	}
	return name
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

// normalizeDeviceArg converts a user-configured device path into the format
// MakeMKV expects.  "/dev/sr0" becomes "dev:/dev/sr0"; values already prefixed
// with "dev:" or "disc:" are returned as-is; an empty string falls back to
// "disc:0" for backwards compatibility.
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
