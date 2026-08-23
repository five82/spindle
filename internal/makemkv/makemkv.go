package makemkv

import (
	"bufio"
	"context"
	"fmt"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/five82/spindle/internal/logs"
)

// DiscInfo represents the results of a MakeMKV disc scan.
type DiscInfo struct {
	Name     string
	Titles   []TitleInfo
	RawLines []string
}

// TitleInfo represents a single title on a disc.
type TitleInfo struct {
	ID           int
	Name         string
	Duration     int
	Chapters     int
	SizeBytes    int64
	SegmentCount int
	SegmentMap   string
	Playlist     string
	Tracks       []Track
}

// RipProgress reports ripping progress.
type RipProgress struct {
	TitleID int
	Current int
	Total   int
	Percent float64
}

// Scan runs makemkvcon info on the given device and parses disc information.
// The device string is normalized: empty defaults to "disc:0", paths starting
// with /dev/ become "dev:<path>", and already-prefixed values pass through.
func Scan(ctx context.Context, device string, timeout time.Duration, minLength int, logger *slog.Logger) (*DiscInfo, error) {
	logger = logs.Default(logger)
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	src := normalizeDevice(device)
	minLenFlag := fmt.Sprintf("--minlength=%d", minLength)

	logger.Info("MakeMKV scan started",
		"event_type", "makemkv_scan_start",
		"device", src,
	)
	start := time.Now()

	cmd := exec.CommandContext(ctx, "makemkvcon", "--robot", "--progress=-same", "info", src, minLenFlag)

	stdout, err := cmd.StdoutPipe()
	if err != nil {
		logger.Error("MakeMKV scan stdout pipe failed",
			"event_type", "makemkv_scan_error",
			"error_hint", "failed to create stdout pipe for makemkvcon",
			"error", err,
		)
		return nil, fmt.Errorf("makemkv scan: stdout pipe: %w", err)
	}

	if err := cmd.Start(); err != nil {
		logger.Error("MakeMKV scan start failed",
			"event_type", "makemkv_scan_error",
			"error_hint", "failed to start makemkvcon process",
			"error", err,
		)
		return nil, fmt.Errorf("makemkv scan: start: %w", err)
	}

	var lines []string
	scanner := bufio.NewScanner(stdout)
	for scanner.Scan() {
		lines = append(lines, scanner.Text())
	}
	if err := scanner.Err(); err != nil {
		logger.Error("MakeMKV scan read failed",
			"event_type", "makemkv_scan_error",
			"error_hint", "failed to read makemkvcon output",
			"error", err,
		)
		return nil, fmt.Errorf("makemkv scan: read output: %w", err)
	}

	if err := cmd.Wait(); err != nil {
		logger.Error("MakeMKV scan failed",
			"event_type", "makemkv_scan_error",
			"error_hint", "makemkvcon exited with error",
			"error", err,
		)
		return nil, fmt.Errorf("makemkv scan: %w", err)
	}

	info := parseRobotOutput(lines)
	logger.Info("MakeMKV scan completed",
		"event_type", "makemkv_scan_complete",
		"device", src,
		"titles_found", len(info.Titles),
		"disc_name", info.Name,
		"duration_ms", time.Since(start).Milliseconds(),
	)
	return info, nil
}

// ripOutcome accumulates the evidence a rip's robot output provides --
// phase state, published progress, error/warning messages, and the final
// "Copy complete" summary counts -- and renders the domain verdict once
// the process has exited.
type ripOutcome struct {
	titleID  int
	progress func(RipProgress)
	logger   *slog.Logger

	errorMsgs        []ripMessage
	warningMsgs      []ripMessage
	savedCount       int // -1 = unknown (no MSG:5036 seen)
	failedCount      int
	lastErrorText    string
	savingTitle      bool
	publishedPercent float64
}

func newRipOutcome(titleID int, progress func(RipProgress), logger *slog.Logger) *ripOutcome {
	return &ripOutcome{
		titleID:     titleID,
		progress:    progress,
		logger:      logger,
		savedCount:  -1,
		failedCount: -1,
	}
}

// observe consumes one robot-output line: it tracks which operation phase
// progress lines belong to, publishes throttled progress during the saving
// phase, accumulates error/warning messages, and scrapes the final summary
// counts.
func (o *ripOutcome) observe(line string) {
	if strings.HasPrefix(line, "PRGT:") {
		o.savingTitle = strings.HasPrefix(line, "PRGT:5024,") // Ignore scan/open phase progress.
		return
	}
	if p, ok := parsePRGV(line, o.titleID); ok {
		if o.savingTitle && o.progress != nil && p.Percent > o.publishedPercent && p.Percent < 100 {
			o.publishedPercent = p.Percent
			o.progress(p)
		}
		return
	}
	msg, ok := parseMSG(line)
	if !ok {
		return
	}
	switch {
	case msg.isError():
		o.errorMsgs = append(o.errorMsgs, msg)
		o.lastErrorText = msg.message
		o.logger.Warn("MakeMKV rip reported error message",
			"event_type", "makemkv_rip_message",
			"msg_code", msg.code,
			"msg_flags", msg.flags,
			"message", msg.message,
			"title_id", o.titleID,
		)
	case msg.isWarning():
		o.warningMsgs = append(o.warningMsgs, msg)
		o.logger.Debug("MakeMKV rip reported warning message",
			"event_type", "makemkv_rip_message",
			"msg_code", msg.code,
			"msg_flags", msg.flags,
			"message", msg.message,
			"title_id", o.titleID,
		)
	}
	if msg.code == msgCodeCopyComplete && len(msg.params) >= 2 {
		if n, err := strconv.Atoi(msg.params[0]); err == nil {
			o.savedCount = n
		}
		if n, err := strconv.Atoi(msg.params[1]); err == nil {
			o.failedCount = n
		}
	}
}

// verdict decides whether the rip succeeded. Exit status alone is not a
// reliable signal: makemkvcon has been observed to exit 0 while producing
// no output (for example, on seamless-branch key failures). A successful
// rip must exit 0, produce at least one new .mkv file, and, if the final
// summary was captured, show saved>=1.
func (o *ripOutcome) verdict(waitErr error, newFiles []string, outputDir string) error {
	if waitErr != nil {
		o.logger.Error("MakeMKV rip failed",
			"event_type", "makemkv_rip_error",
			"error_hint", "makemkvcon rip exited with error",
			"error", waitErr,
			"title_id", o.titleID,
			"error_msg_count", len(o.errorMsgs),
			"last_error_message", o.lastErrorText,
		)
		return fmt.Errorf("makemkv rip: %w (error_messages=%d, last=%q)", waitErr, len(o.errorMsgs), o.lastErrorText)
	}
	if len(newFiles) == 0 {
		o.logger.Error("MakeMKV rip produced no output",
			"event_type", "makemkv_rip_error",
			"error_hint", "makemkvcon exited 0 but no new MKV file appeared",
			"title_id", o.titleID,
			"output_dir", outputDir,
			"saved_count", o.savedCount,
			"failed_count", o.failedCount,
			"error_msg_count", len(o.errorMsgs),
			"warning_msg_count", len(o.warningMsgs),
			"last_error_message", o.lastErrorText,
			"stalled_at_percent", o.publishedPercent,
		)
		// publishedPercent is the diagnosis: MakeMKV abandoning a title
		// part-way through means unreadable sectors, not an empty rip.
		return fmt.Errorf("makemkv rip: makemkvcon exited 0 but produced no output (stalled at %.1f%%, saved=%d failed=%d errors=%d last=%q)",
			o.publishedPercent, o.savedCount, o.failedCount, len(o.errorMsgs), o.lastErrorText)
	}
	if o.savedCount == 0 {
		o.logger.Error("MakeMKV rip summary reports zero saved",
			"event_type", "makemkv_rip_error",
			"error_hint", "makemkvcon final summary shows zero titles saved",
			"title_id", o.titleID,
			"saved_count", o.savedCount,
			"failed_count", o.failedCount,
			"new_files", len(newFiles),
			"last_error_message", o.lastErrorText,
		)
		return fmt.Errorf("makemkv rip: summary reports zero saved (failed=%d errors=%d last=%q)",
			o.failedCount, len(o.errorMsgs), o.lastErrorText)
	}
	return nil
}

// Rip runs makemkvcon mkv to rip a single title from disc to outputDir.
// The progress callback, if non-nil, is called with progress updates.
//
// The rip's success rules live in ripOutcome.verdict: it combines MSG
// diagnostics and summary counts from the robot output with whether an
// output file actually appeared on disk, because exit status alone is
// not a reliable success signal.
func Rip(ctx context.Context, device string, titleID int, outputDir string, timeout time.Duration, minLength int, progress func(RipProgress), logger *slog.Logger) error {
	logger = logs.Default(logger)
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	src := normalizeDevice(device)
	titleStr := strconv.Itoa(titleID)
	minLenFlag := fmt.Sprintf("--minlength=%d", minLength)

	logger.Info("MakeMKV rip started",
		"event_type", "makemkv_rip_start",
		"device", src,
		"title_id", titleID,
		"output_dir", outputDir,
	)
	start := time.Now()

	// Snapshot existing .mkv files so we can identify the new one
	// produced by this rip (independent of file name heuristics).
	existing := snapshotMKVFiles(outputDir)

	cmd := exec.CommandContext(ctx, "makemkvcon", "--robot", "--progress=-same", "mkv", src, titleStr, outputDir, minLenFlag)

	stdout, err := cmd.StdoutPipe()
	if err != nil {
		logger.Error("MakeMKV rip stdout pipe failed",
			"event_type", "makemkv_rip_error",
			"error_hint", "failed to create stdout pipe for makemkvcon",
			"error", err,
		)
		return fmt.Errorf("makemkv rip: stdout pipe: %w", err)
	}

	if err := cmd.Start(); err != nil {
		logger.Error("MakeMKV rip start failed",
			"event_type", "makemkv_rip_error",
			"error_hint", "failed to start makemkvcon rip process",
			"error", err,
		)
		return fmt.Errorf("makemkv rip: start: %w", err)
	}

	outcome := newRipOutcome(titleID, progress, logger)
	scanner := bufio.NewScanner(stdout)
	scanner.Buffer(make([]byte, 0, 64*1024), 1024*1024)
	for scanner.Scan() {
		outcome.observe(scanner.Text())
	}
	if err := scanner.Err(); err != nil {
		logger.Error("MakeMKV rip read failed",
			"event_type", "makemkv_rip_error",
			"error_hint", "failed to read makemkvcon rip output",
			"error", err,
		)
		return fmt.Errorf("makemkv rip: read output: %w", err)
	}

	waitErr := cmd.Wait()
	newFiles := newMKVFiles(outputDir, existing)
	if err := outcome.verdict(waitErr, newFiles, outputDir); err != nil {
		return err
	}

	logger.Info("MakeMKV rip completed",
		"event_type", "makemkv_rip_complete",
		"device", src,
		"title_id", titleID,
		"saved_count", outcome.savedCount,
		"failed_count", outcome.failedCount,
		"new_files", len(newFiles),
		"warning_msg_count", len(outcome.warningMsgs),
		"duration_ms", time.Since(start).Milliseconds(),
	)
	return nil
}

// snapshotMKVFiles returns the set of .mkv file names present in dir.
// Returns an empty set if the directory does not exist yet.
func snapshotMKVFiles(dir string) map[string]struct{} {
	result := make(map[string]struct{})
	entries, err := os.ReadDir(dir)
	if err != nil {
		return result
	}
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		if strings.EqualFold(filepath.Ext(e.Name()), ".mkv") {
			result[e.Name()] = struct{}{}
		}
	}
	return result
}

// newMKVFiles returns .mkv file names in dir that are not in existing.
func newMKVFiles(dir string, existing map[string]struct{}) []string {
	var out []string
	entries, err := os.ReadDir(dir)
	if err != nil {
		return out
	}
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		if !strings.EqualFold(filepath.Ext(e.Name()), ".mkv") {
			continue
		}
		if _, was := existing[e.Name()]; was {
			continue
		}
		out = append(out, e.Name())
	}
	return out
}

// normalizeDevice converts a device string to the format expected by makemkvcon.
func normalizeDevice(device string) string {
	switch {
	case device == "":
		return "disc:0"
	case strings.HasPrefix(device, "/dev/"):
		return "dev:" + device
	default:
		return device
	}
}
