package auditgather

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"spindle/internal/config"
	"spindle/internal/encodingstate"
	"spindle/internal/media/ffprobe"
	"spindle/internal/queue"
	"spindle/internal/ripcache"
	"spindle/internal/ripspec"
)

// Gather collects all audit artifacts for the given queue item.
func Gather(ctx context.Context, cfg *config.Config, item *queue.Item) (*Report, error) {
	if item == nil {
		return nil, fmt.Errorf("nil queue item")
	}

	r := &Report{}

	// Phase 1: Item summary
	r.Item = buildItemSummary(item)

	// Parse metadata for media type / edition
	meta := queue.MetadataFromJSON(item.MetadataJSON, item.DiscTitle)

	// Parse envelope
	env, envErr := ripspec.Parse(item.RipSpecData)
	if envErr != nil {
		r.addError("parse ripspec: %v", envErr)
	} else if item.RipSpecData != "" {
		r.Envelope = buildEnvelopeReport(&env)
	}

	// Stage gating
	r.StageGate = computeStageGate(item, &meta)

	// Phase 2: Logs
	logReport, logErr := gatherLogs(cfg, item)
	if logErr != nil {
		r.addError("gather logs: %v", logErr)
	} else {
		r.Logs = logReport
	}

	// Infer disc source from logs if not determined from metadata
	if r.StageGate.DiscSource == "unknown" && r.Logs != nil {
		r.StageGate.DiscSource = inferDiscSourceFromLogs(r.Logs)
		// Recalculate external validation eligibility
		r.StageGate.PhaseExternalValidation = r.StageGate.PhaseEncoded && r.StageGate.DiscSource != "dvd"
	}

	// Phase 3: Rip cache
	if r.StageGate.PhaseRipCache {
		cacheReport, cacheErr := gatherRipCache(cfg, item)
		if cacheErr != nil {
			r.addError("gather rip cache: %v", cacheErr)
		} else {
			r.RipCache = cacheReport
		}
	}

	// Phase 4: Encoding details
	if r.StageGate.PhaseEncoded && item.EncodingDetailsJSON != "" {
		snap, snapErr := encodingstate.Unmarshal(item.EncodingDetailsJSON)
		if snapErr != nil {
			r.addError("parse encoding details: %v", snapErr)
		} else if !snap.IsZero() {
			r.Encoding = &EncodingReport{Snapshot: snap}
		}
	}

	// Phase 5: Media probes
	if r.StageGate.PhaseEncoded {
		probes := gatherMediaProbes(ctx, item, &env, &meta)
		if len(probes) > 0 {
			r.Media = probes
		}
	}

	// Phase 6: Pre-computed analysis
	r.Analysis = computeAnalysis(r)

	return r, nil
}

func (r *Report) addError(format string, args ...any) {
	r.Errors = append(r.Errors, fmt.Sprintf(format, args...))
}

func buildItemSummary(item *queue.Item) ItemSummary {
	return ItemSummary{
		ID:              item.ID,
		DiscTitle:       item.DiscTitle,
		Status:          string(item.Status),
		FailedAtStatus:  string(item.FailedAtStatus),
		ErrorMessage:    item.ErrorMessage,
		NeedsReview:     item.NeedsReview,
		ReviewReason:    item.ReviewReason,
		DiscFingerprint: item.DiscFingerprint,
		CreatedAt:       item.CreatedAt.Format("2006-01-02T15:04:05Z"),
		UpdatedAt:       item.UpdatedAt.Format("2006-01-02T15:04:05Z"),
		ProgressStage:   item.ProgressStage,
		ProgressPercent: item.ProgressPercent,
		ProgressMessage: item.ProgressMessage,
		ItemLogPath:     item.ItemLogPath,
		RippedFile:      item.RippedFile,
		EncodedFile:     item.EncodedFile,
		FinalFile:       item.FinalFile,
	}
}

// statusOrder maps each status to a numeric position in the lifecycle.
// In-progress statuses (-ing) have the same ordinal as their predecessor
// so that failing *during* a stage does not grant that stage's phase.
var statusOrder = map[queue.Status]int{
	queue.StatusPending:            0,
	queue.StatusIdentifying:        1,
	queue.StatusIdentified:         2,
	queue.StatusRipping:            3,
	queue.StatusRipped:             4,
	queue.StatusEpisodeIdentifying: 5,
	queue.StatusEpisodeIdentified:  6,
	queue.StatusEncoding:           7,
	queue.StatusEncoded:            8,
	queue.StatusAudioAnalyzing:     9,
	queue.StatusAudioAnalyzed:      10,
	queue.StatusSubtitling:         11,
	queue.StatusSubtitled:          12,
	queue.StatusOrganizing:         13,
	queue.StatusCompleted:          14,
	queue.StatusFailed:             -1, // Sentinel; effectiveStatus resolves this
}

// effectiveStatus returns the status to use for stage gating.
// For failed items it uses FailedAtStatus; for all others it uses Status directly.
func effectiveStatus(item *queue.Item) queue.Status {
	if item.Status == queue.StatusFailed {
		if item.FailedAtStatus != "" {
			return item.FailedAtStatus
		}
		// Legacy failed items without FailedAtStatus: fall back to pending
		// so only PhaseLogs is enabled (safe default).
		return queue.StatusPending
	}
	return item.Status
}

func furthestStage(item *queue.Item) string {
	return string(effectiveStatus(item))
}

func reachedAtLeast(item *queue.Item, target queue.Status) bool {
	return statusOrder[effectiveStatus(item)] >= statusOrder[target]
}

func computeStageGate(item *queue.Item, meta *queue.Metadata) StageGate {
	mediaType := "movie"
	if !meta.IsMovie() {
		mediaType = "tv"
	}

	sg := StageGate{
		FurthestStage: furthestStage(item),
		MediaType:     mediaType,
		DiscSource:    "unknown", // Will be refined from logs
		Edition:       meta.GetEdition(),

		PhaseLogs:       true, // Always applicable
		PhaseRipCache:   reachedAtLeast(item, queue.StatusRipped),
		PhaseEpisodeID:  mediaType == "tv" && reachedAtLeast(item, queue.StatusEpisodeIdentified),
		PhaseEncoded:    reachedAtLeast(item, queue.StatusEncoded),
		PhaseCrop:       reachedAtLeast(item, queue.StatusEncoded),
		PhaseEdition:    mediaType == "movie" && reachedAtLeast(item, queue.StatusIdentified),
		PhaseSubtitles:  reachedAtLeast(item, queue.StatusSubtitled),
		PhaseCommentary: reachedAtLeast(item, queue.StatusAudioAnalyzed),
		// External validation requires encoded files AND non-DVD source.
		// DiscSource is unknown here; will be updated after log analysis.
		PhaseExternalValidation: false,
	}

	return sg
}

// gatherLogs finds the item log file and parses structured entries.
func gatherLogs(cfg *config.Config, item *queue.Item) (*LogAnalysis, error) {
	logPath, isDebug := findLogFile(cfg, item)
	if logPath == "" {
		return nil, fmt.Errorf("no log file found for item %d", item.ID)
	}

	f, err := os.Open(logPath)
	if err != nil {
		return nil, fmt.Errorf("open log: %w", err)
	}
	defer f.Close()

	report := &LogAnalysis{
		Path:    logPath,
		IsDebug: isDebug,
	}

	scanner := bufio.NewScanner(f)
	// Allow large lines (some JSON log entries can be big)
	scanner.Buffer(make([]byte, 0, 256*1024), 1024*1024)

	for scanner.Scan() {
		line := scanner.Text()
		report.TotalLines++
		parseLogLine(line, report)
	}
	if err := scanner.Err(); err != nil {
		return report, fmt.Errorf("scan log: %w", err)
	}

	return report, nil
}

// findLogFile locates the best log file for the item.
// Prefers debug logs, falls back to normal item logs.
func findLogFile(cfg *config.Config, item *queue.Item) (string, bool) {
	logDir := cfg.Paths.LogDir

	// If the item has a stored log path, derive the basename and check
	// for a debug variant first.
	if item.ItemLogPath != "" {
		base := filepath.Base(item.ItemLogPath)

		// Check debug directory first
		debugPath := filepath.Join(logDir, "debug", "items", base)
		if fileExists(debugPath) {
			return debugPath, true
		}

		// Check stored path directly
		if fileExists(item.ItemLogPath) {
			return item.ItemLogPath, false
		}
	}

	// Fallback: scan both directories for files containing the item ID
	idStr := fmt.Sprintf("-%d-", item.ID)

	debugDir := filepath.Join(logDir, "debug", "items")
	if path := findFileContaining(debugDir, idStr); path != "" {
		return path, true
	}

	itemsDir := filepath.Join(logDir, "items")
	if path := findFileContaining(itemsDir, idStr); path != "" {
		return path, false
	}

	return "", false
}

func findFileContaining(dir, substr string) string {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return ""
	}
	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}
		if strings.Contains(entry.Name(), substr) {
			return filepath.Join(dir, entry.Name())
		}
	}
	return ""
}

func fileExists(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}

// parseLogLine extracts structured data from a single JSON log line.
func parseLogLine(line string, report *LogAnalysis) {
	line = strings.TrimSpace(line)
	if line == "" || line[0] != '{' {
		return
	}

	// Infer disc source from raw line content (before parsing)
	if report.InferredDiscSource == "" || report.InferredDiscSource == "unknown" {
		if source := inferDiscSourceFromJSON(line); source != "unknown" {
			report.InferredDiscSource = source
		}
	}

	var entry map[string]any
	if err := json.Unmarshal([]byte(line), &entry); err != nil {
		return
	}

	level := getString(entry, "level")
	msg := getString(entry, "msg")
	ts := getString(entry, "ts")
	eventType := getString(entry, "event_type")

	// Collect decisions
	if decType := getString(entry, "decision_type"); decType != "" {
		report.Decisions = append(report.Decisions, LogDecision{
			Timestamp:      ts,
			DecisionType:   decType,
			DecisionResult: getString(entry, "decision_result"),
			DecisionReason: getString(entry, "decision_reason"),
			Message:        msg,
			RawJSON:        line,
		})
	}

	// Collect warnings
	if strings.EqualFold(level, "warn") {
		report.Warnings = append(report.Warnings, LogEntry{
			Timestamp: ts,
			Level:     level,
			Message:   msg,
			EventType: eventType,
			ErrorHint: getString(entry, "error_hint"),
			RawJSON:   line,
		})
	}

	// Collect errors
	if strings.EqualFold(level, "error") {
		report.Errors = append(report.Errors, LogEntry{
			Timestamp: ts,
			Level:     level,
			Message:   msg,
			EventType: eventType,
			ErrorHint: getString(entry, "error_hint"),
			RawJSON:   line,
		})
	}

	// Collect stage events
	if strings.HasPrefix(eventType, "stage_") {
		report.Stages = append(report.Stages, StageEvent{
			Timestamp: ts,
			EventType: eventType,
			Stage:     getString(entry, "stage"),
			Message:   msg,
			Duration:  getStageDurationSeconds(entry),
			RawJSON:   line,
		})
	}
}

func getString(m map[string]any, key string) string {
	v, ok := m[key]
	if !ok {
		return ""
	}
	s, ok := v.(string)
	if !ok {
		return fmt.Sprintf("%v", v)
	}
	return s
}

func getFloat(m map[string]any, key string) (float64, bool) {
	v, ok := m[key]
	if !ok {
		return 0, false
	}
	f, ok := v.(float64)
	return f, ok
}

// getStageDurationSeconds extracts stage duration in seconds.
// The workflow manager logs this as "stage_duration" in nanoseconds (slog.Duration).
func getStageDurationSeconds(entry map[string]any) float64 {
	if ns, ok := getFloat(entry, "stage_duration"); ok && ns > 0 {
		return ns / 1e9
	}
	return 0
}

// inferDiscSourceFromLogs uses the disc source already detected during log parsing.
func inferDiscSourceFromLogs(logs *LogAnalysis) string {
	if logs.InferredDiscSource != "" {
		return logs.InferredDiscSource
	}
	return "unknown"
}

func inferDiscSourceFromJSON(rawJSON string) string {
	if strings.Contains(rawJSON, `"is_blu_ray":true`) || strings.Contains(rawJSON, `"is_blu_ray": true`) {
		// Could be 4K or regular Blu-ray; check for UHD indicators
		if strings.Contains(rawJSON, "uhd") || strings.Contains(rawJSON, "UHD") || strings.Contains(rawJSON, "2160") {
			return "4k_bluray"
		}
		return "bluray"
	}
	if strings.Contains(rawJSON, "VIDEO_TS") || strings.Contains(rawJSON, "video_ts") {
		return "dvd"
	}
	return "unknown"
}

// gatherRipCache reads the rip cache metadata for the item.
func gatherRipCache(cfg *config.Config, item *queue.Item) (*RipCacheReport, error) {
	if !cfg.RipCache.Enabled || cfg.RipCache.Dir == "" {
		return &RipCacheReport{Found: false}, nil
	}

	// Build cache path the same way ripcache.Manager does
	cachePath := buildCachePath(cfg.RipCache.Dir, item)

	meta, found, err := ripcache.LoadMetadata(cachePath)
	if err != nil {
		return &RipCacheReport{Path: cachePath, Found: found}, fmt.Errorf("load cache metadata: %w", err)
	}

	return &RipCacheReport{
		Path:     cachePath,
		Found:    found,
		Metadata: &meta,
	}, nil
}

// buildCachePath mirrors ripcache.Manager.cachePath logic.
func buildCachePath(root string, item *queue.Item) string {
	segment := strings.TrimSpace(item.DiscFingerprint)
	if segment == "" && item.ID > 0 {
		segment = fmt.Sprintf("queue-%d", item.ID)
	}
	if segment == "" {
		segment = strings.TrimSpace(item.DiscTitle)
	}
	if segment == "" {
		segment = "queue-temp"
	}
	// Sanitize: lowercase, replace spaces with dashes, trim
	segment = strings.ToLower(segment)
	segment = strings.ReplaceAll(segment, " ", "-")
	segment = strings.Trim(segment, "-_.")
	if segment == "" {
		segment = "queue"
	}
	return filepath.Join(root, segment)
}

func buildEnvelopeReport(env *ripspec.Envelope) *EnvelopeReport {
	return &EnvelopeReport{
		Fingerprint: env.Fingerprint,
		ContentKey:  env.ContentKey,
		Metadata:    env.Metadata,
		Titles:      env.Titles,
		Episodes:    env.Episodes,
		Assets:      env.Assets,
		Attributes:  env.Attributes,
	}
}

// gatherMediaProbes runs ffprobe on encoded (and optionally ripped) files.
func gatherMediaProbes(ctx context.Context, item *queue.Item, env *ripspec.Envelope, meta *queue.Metadata) []MediaFileProbe {
	var probes []MediaFileProbe

	if meta.IsMovie() {
		// Single encoded file
		if path := strings.TrimSpace(item.EncodedFile); path != "" {
			probes = append(probes, probeFile(ctx, path, "encoded", ""))
		}
		// Final file if different
		if path := strings.TrimSpace(item.FinalFile); path != "" && path != item.EncodedFile {
			probes = append(probes, probeFile(ctx, path, "final", ""))
		}
	} else {
		// TV: probe each encoded episode asset, fall back to final if staging cleaned up
		for _, asset := range env.Assets.Encoded {
			if path := strings.TrimSpace(asset.Path); path != "" && asset.Status != ripspec.AssetStatusFailed {
				p := probeFile(ctx, path, "encoded", asset.EpisodeKey)
				if p.Error != "" {
					// Staging cleaned up; try final path
					if final, ok := env.Assets.FindAsset("final", asset.EpisodeKey); ok {
						if fpath := strings.TrimSpace(final.Path); fpath != "" {
							p = probeFile(ctx, fpath, "final", asset.EpisodeKey)
						}
					}
				}
				probes = append(probes, p)
			}
		}
	}

	return probes
}

func probeFile(ctx context.Context, path, role, episodeKey string) MediaFileProbe {
	result, err := ffprobe.Inspect(ctx, "ffprobe", path)
	if err != nil {
		return MediaFileProbe{
			Path:       path,
			Role:       role,
			EpisodeKey: episodeKey,
			Error:      err.Error(),
		}
	}
	return MediaFileProbe{
		Path:        path,
		Role:        role,
		EpisodeKey:  episodeKey,
		Probe:       result,
		SizeBytes:   result.SizeBytes(),
		DurationSec: result.DurationSeconds(),
	}
}
