package subtitles

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"log/slog"

	langpkg "spindle/internal/language"
	"spindle/internal/logging"
)

// mkvmerge command and binary name.
const mkvmergeCommand = "mkvmerge"

// MuxRequest describes the inputs for subtitle muxing.
type MuxRequest struct {
	MKVPath           string   // Source MKV file
	SubtitlePaths     []string // SRT files to mux (regular and forced)
	Language          string   // ISO 639-1 code (e.g., "en")
	StripExistingSubs bool     // Remove existing subtitle tracks before muxing
}

// MuxResult reports the outcome of subtitle muxing.
type MuxResult struct {
	OutputPath      string   // Final MKV path (same as input after atomic replace)
	MuxedSubtitles  []string // Subtitles that were muxed
	RemovedSidecars []string // Sidecar files removed after muxing
}

// Muxer embeds SRT subtitles into MKV containers using mkvmerge.
type Muxer struct {
	logger *slog.Logger
	run    commandRunner
}

// NewMuxer constructs a subtitle muxer.
func NewMuxer(logger *slog.Logger) *Muxer {
	return &Muxer{
		logger: logging.NewComponentLogger(logger, "muxer"),
		run:    defaultMuxerCommandRunner,
	}
}

// SetLogger updates the muxer's logging destination.
func (m *Muxer) SetLogger(logger *slog.Logger) {
	if m == nil {
		return
	}
	m.logger = logging.NewComponentLogger(logger, "muxer")
}

// WithCommandRunner allows injecting a custom command runner for tests.
func (m *Muxer) WithCommandRunner(r commandRunner) {
	if m != nil && r != nil {
		m.run = r
	}
}

// MuxSubtitles embeds SRT files into an MKV container.
// The operation is atomic: a temporary file is created and renamed on success.
func (m *Muxer) MuxSubtitles(ctx context.Context, req MuxRequest) (MuxResult, error) {
	if m == nil {
		return MuxResult{}, fmt.Errorf("muxer not initialized")
	}
	if strings.TrimSpace(req.MKVPath) == "" {
		return MuxResult{}, fmt.Errorf("MKV path is required")
	}
	if len(req.SubtitlePaths) == 0 {
		return MuxResult{}, fmt.Errorf("at least one subtitle path is required")
	}

	// Verify source MKV exists
	if _, err := os.Stat(req.MKVPath); err != nil {
		return MuxResult{}, fmt.Errorf("source MKV not found: %w", err)
	}

	// Verify all subtitle files exist
	for _, srtPath := range req.SubtitlePaths {
		if _, err := os.Stat(srtPath); err != nil {
			return MuxResult{}, fmt.Errorf("subtitle file not found %q: %w", srtPath, err)
		}
	}

	// Create temporary output file in same directory for atomic rename
	dir := filepath.Dir(req.MKVPath)
	base := filepath.Base(req.MKVPath)
	tmpPath := filepath.Join(dir, ".mux-"+base+".tmp")

	// Build mkvmerge command
	args := m.buildMkvmergeArgs(req, tmpPath)

	if m.logger != nil {
		m.logger.Debug("executing mkvmerge",
			logging.String("mkv_path", req.MKVPath),
			logging.Int("subtitle_count", len(req.SubtitlePaths)),
			logging.String("language", req.Language),
			logging.Bool("strip_existing", req.StripExistingSubs),
		)
	}

	// Execute mkvmerge
	if err := m.run(ctx, mkvmergeCommand, args...); err != nil {
		// Clean up temp file on failure
		_ = os.Remove(tmpPath)
		return MuxResult{}, fmt.Errorf("mkvmerge failed: %w", err)
	}

	// Verify output was created
	if _, err := os.Stat(tmpPath); err != nil {
		return MuxResult{}, fmt.Errorf("mkvmerge did not produce output file: %w", err)
	}

	// Atomic replace: rename temp file to original
	if err := os.Rename(tmpPath, req.MKVPath); err != nil {
		_ = os.Remove(tmpPath)
		return MuxResult{}, fmt.Errorf("failed to replace original MKV: %w", err)
	}

	// Remove sidecar SRT files after successful mux
	var removed []string
	for _, srtPath := range req.SubtitlePaths {
		if err := os.Remove(srtPath); err != nil {
			if m.logger != nil {
				m.logger.Warn("failed to remove sidecar SRT after muxing",
					logging.Error(err),
					logging.String("srt_path", srtPath),
					logging.String(logging.FieldEventType, "sidecar_removal_failed"),
				)
			}
		} else {
			removed = append(removed, srtPath)
		}
	}

	if m.logger != nil {
		m.logger.Info("subtitles muxed into MKV",
			logging.String(logging.FieldEventType, "subtitle_mux_complete"),
			logging.String("mkv_path", req.MKVPath),
			logging.Int("tracks_added", len(req.SubtitlePaths)),
			logging.Int("sidecars_removed", len(removed)),
		)
	}

	return MuxResult{
		OutputPath:      req.MKVPath,
		MuxedSubtitles:  req.SubtitlePaths,
		RemovedSidecars: removed,
	}, nil
}

// buildMkvmergeArgs constructs the mkvmerge command arguments.
func (m *Muxer) buildMkvmergeArgs(req MuxRequest, outputPath string) []string {
	args := []string{"-o", outputPath}

	// Strip existing subtitle tracks if requested
	if req.StripExistingSubs {
		args = append(args, "-S")
	}

	// Add the source MKV
	args = append(args, req.MKVPath)

	// Map ISO 639-1 to ISO 639-2 for mkvmerge
	lang3 := langpkg.ToISO3(req.Language)

	// Add each subtitle track
	for _, srtPath := range req.SubtitlePaths {
		isForced := isForcedSRT(srtPath)
		trackName := buildTrackName(req.Language, isForced)

		// Language flag (applies to track 0 of the following file)
		args = append(args, "--language", "0:"+lang3)

		// Track name
		args = append(args, "--track-name", "0:"+trackName)

		// Default track: yes for regular, no for forced
		if isForced {
			args = append(args, "--default-track", "0:no")
			args = append(args, "--forced-track", "0:yes")
		} else {
			args = append(args, "--default-track", "0:yes")
		}

		// Add the subtitle file
		args = append(args, srtPath)
	}

	return args
}

// isForcedSRT checks if an SRT path is a forced subtitle based on filename pattern.
func isForcedSRT(path string) bool {
	return strings.Contains(strings.ToLower(path), ".forced.")
}

// buildTrackName creates a human-readable track name.
func buildTrackName(lang string, forced bool) string {
	name := langpkg.DisplayName(lang)
	if forced {
		name += " (Forced)"
	}
	return name
}

// defaultMuxerCommandRunner executes mkvmerge commands.
func defaultMuxerCommandRunner(ctx context.Context, name string, args ...string) error {
	cmd := exec.CommandContext(ctx, name, args...)
	output, err := cmd.CombinedOutput()
	if err != nil {
		// Include output in error for debugging
		return fmt.Errorf("%w: %s", err, strings.TrimSpace(string(output)))
	}
	return nil
}
