package auditgather

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/five82/spindle/internal/config"
	"github.com/five82/spindle/internal/encodingstate"
	"github.com/five82/spindle/internal/media/ffprobe"
	"github.com/five82/spindle/internal/queue"
	"github.com/five82/spindle/internal/ripcache"
	"github.com/five82/spindle/internal/ripspec"
)

// stageOrder maps stages to numeric order for furthest-stage computation.
var stageOrder = map[queue.Stage]int{
	queue.StagePending:                0,
	queue.StageIdentification:         1,
	queue.StageRipping:                2,
	queue.StageEpisodeIdentification:  3,
	queue.StageEncoding:               4,
	queue.StageAudioAnalysis:          5,
	queue.StageSubtitling:             6,
	queue.StageOrganizing:             7,
	queue.StageCompleted:              8,
}

// Gather collects all audit artifacts for a queue item.
func Gather(ctx context.Context, cfg *config.Config, item *queue.Item) (*Report, error) {
	if item == nil {
		return nil, fmt.Errorf("nil queue item")
	}

	r := &Report{
		Item: buildItemSummary(item),
	}

	// Parse envelope for media type, disc source, edition.
	var env ripspec.Envelope
	if item.RipSpecData != "" {
		if err := json.Unmarshal([]byte(item.RipSpecData), &env); err != nil {
			r.addError("parse envelope: %v", err)
		} else {
			r.Envelope = &EnvelopeReport{
				Fingerprint: env.Fingerprint,
				ContentKey:  env.ContentKey,
				Metadata:    env.Metadata,
				Titles:      env.Titles,
				Episodes:    env.Episodes,
				Assets:      env.Assets,
				Attributes:  env.Attributes,
			}
		}
	}

	// Fall back to metadata JSON for media type if envelope unavailable.
	mediaType := env.Metadata.MediaType
	if mediaType == "" {
		meta := queue.MetadataFromJSON(item.MetadataJSON, item.DiscTitle)
		if meta.Movie {
			mediaType = "movie"
		} else {
			mediaType = "tv"
		}
	}

	// Compute stage gate.
	r.StageGate = computeStageGate(item, mediaType, env.Metadata.DiscSource, env.Metadata.Edition)

	// Log analysis.
	logReport, logErr := gatherLogs(cfg, item)
	if logErr != nil {
		r.addError("gather logs: %v", logErr)
	} else {
		r.Logs = logReport
	}

	// Refine disc source from logs if still unknown.
	if r.StageGate.DiscSource == "" && r.Logs != nil && r.Logs.InferredDiscSource != "" {
		r.StageGate.DiscSource = r.Logs.InferredDiscSource
		r.StageGate.PhaseExtVal = r.StageGate.PhaseEncoded && r.StageGate.DiscSource != "dvd"
	}

	// Rip cache.
	if r.StageGate.PhaseRipCache {
		cacheReport := gatherRipCache(cfg, item)
		r.RipCache = cacheReport
	}

	// Encoding snapshot.
	if r.StageGate.PhaseEncoded && item.EncodingDetailsJSON != "" {
		snap, err := encodingstate.Unmarshal(item.EncodingDetailsJSON)
		if err != nil {
			r.addError("parse encoding snapshot: %v", err)
		} else if !snap.IsZero() {
			r.Encoding = &EncodingReport{Snapshot: snap}
		}
	}

	// Media probes.
	if r.StageGate.PhaseEncoded {
		probes := gatherMediaProbes(ctx, &env, mediaType)
		if len(probes) > 0 {
			r.Media = probes
		}
	}

	// Pre-computed analysis.
	r.Analysis = computeAnalysis(r)

	// Compress TV probes.
	if r.Analysis != nil {
		r.Media, r.MediaOmitted = compressMediaProbes(r.Media, r.Analysis.EpisodeConsistency)
	}

	return r, nil
}

func (r *Report) addError(format string, args ...any) {
	r.Errors = append(r.Errors, fmt.Sprintf(format, args...))
}

func buildItemSummary(item *queue.Item) ItemSummary {
	return ItemSummary{
		ID:              item.ID,
		DiscTitle:       item.DiscTitle,
		Stage:           string(item.Stage),
		FailedAtStage:   item.FailedAtStage,
		ErrorMessage:    item.ErrorMessage,
		NeedsReview:     item.NeedsReview != 0,
		ReviewReason:    item.ReviewReason,
		DiscFingerprint: item.DiscFingerprint,
		CreatedAt:       item.CreatedAt,
		UpdatedAt:       item.UpdatedAt,
		ProgressStage:   item.ProgressStage,
		ProgressPercent: item.ProgressPercent,
		ProgressMessage: item.ProgressMessage,
	}
}

func computeStageGate(item *queue.Item, mediaType, discSource, edition string) StageGate {
	furthest := item.Stage
	if item.Stage == queue.StageFailed && item.FailedAtStage != "" {
		furthest = queue.Stage(item.FailedAtStage)
	}

	order := stageOrder[furthest]

	return StageGate{
		FurthestStage: string(furthest),
		MediaType:     mediaType,
		DiscSource:    discSource,
		Edition:       edition,

		PhaseLogs:       true,
		PhaseRipCache:   order >= stageOrder[queue.StageRipping],
		PhaseEpisodeID:  mediaType == "tv" && order >= stageOrder[queue.StageEpisodeIdentification],
		PhaseEncoded:    order >= stageOrder[queue.StageEncoding],
		PhaseCrop:       order >= stageOrder[queue.StageEncoding],
		PhaseEdition:    mediaType == "movie" && order >= stageOrder[queue.StageIdentification],
		PhaseSubtitles:  order >= stageOrder[queue.StageSubtitling],
		PhaseCommentary: order >= stageOrder[queue.StageAudioAnalysis],
		PhaseExtVal:     order >= stageOrder[queue.StageEncoding] && discSource != "dvd",
	}
}

// gatherLogs finds the daemon log file and parses structured entries for this item.
func gatherLogs(cfg *config.Config, item *queue.Item) (*LogAnalysis, error) {
	logPath := findLogFile(cfg.DaemonLogDir(), item.CreatedAt)
	if logPath == "" {
		return nil, fmt.Errorf("no log file found for item %d", item.ID)
	}

	f, err := os.Open(logPath)
	if err != nil {
		return nil, fmt.Errorf("open log: %w", err)
	}
	defer func() { _ = f.Close() }()

	report := &LogAnalysis{Path: logPath}

	scanner := bufio.NewScanner(f)
	scanner.Buffer(make([]byte, 0, 4096), 1024*1024)

	for scanner.Scan() {
		line := scanner.Text()
		report.TotalLines++
		parseLogLine(line, item.ID, report)
	}
	if err := scanner.Err(); err != nil {
		return report, fmt.Errorf("scan log: %w", err)
	}

	return report, nil
}

// findLogFile locates the daemon log file that was active when the item was created.
// Log files are named spindle-{timestamp}.log in the state directory.
func findLogFile(stateDir, createdAt string) string {
	entries, err := os.ReadDir(stateDir)
	if err != nil {
		return ""
	}

	// Collect log file names sorted by name (which sorts by timestamp).
	var logFiles []string
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		name := e.Name()
		if strings.HasPrefix(name, "spindle-") && strings.HasSuffix(name, ".log") {
			logFiles = append(logFiles, filepath.Join(stateDir, name))
		}
	}

	if len(logFiles) == 0 {
		return ""
	}

	sort.Strings(logFiles)

	// Find the last log file whose timestamp is <= createdAt.
	// Log filenames encode the timestamp: spindle-20260322T011625.285Z.log
	// Item createdAt is like: 2026-03-22 01:16:59
	// We pick the last log file whose name sorts before or at the item creation time.
	best := logFiles[0] // Default to earliest if none match.
	for _, path := range logFiles {
		ts := extractLogTimestamp(filepath.Base(path))
		if ts <= createdAt {
			best = path
		}
	}

	return best
}

// extractLogTimestamp converts "spindle-20260322T011625.285Z.log" to
// "2026-03-22 01:16:25" for comparison with item.CreatedAt.
func extractLogTimestamp(filename string) string {
	// Strip prefix and suffix: "20260322T011625.285Z"
	s := strings.TrimPrefix(filename, "spindle-")
	s = strings.TrimSuffix(s, ".log")

	// Take just the date+time part before any fractional seconds.
	if idx := strings.IndexByte(s, '.'); idx > 0 {
		s = s[:idx]
	}

	// Parse "20260322T011625" into "2026-03-22 01:16:25"
	if len(s) < 15 {
		return ""
	}
	return fmt.Sprintf("%s-%s-%s %s:%s:%s",
		s[0:4], s[4:6], s[6:8],
		s[9:11], s[11:13], s[13:15])
}

// knownLogKeys are fields already extracted into struct fields or universal
// context that appears on every line. buildExtras returns everything else.
var knownLogKeys = map[string]bool{
	"time": true, "level": true, "msg": true,
	"decision_type": true, "decision_result": true, "decision_reason": true,
	"event_type": true, "error_hint": true, "error": true,
	"stage": true, "stage_duration": true,
	"item_id": true,
}

// parseLogLine extracts structured data from a single JSON log line.
// Only lines matching the given itemID are included.
func parseLogLine(line string, itemID int64, report *LogAnalysis) {
	line = strings.TrimSpace(line)
	if line == "" || line[0] != '{' {
		return
	}

	var entry map[string]any
	if err := json.Unmarshal([]byte(line), &entry); err != nil {
		return
	}

	// Filter by item_id.
	if id, ok := getFloat(entry, "item_id"); !ok || int64(id) != itemID {
		return
	}

	// Infer disc source from this item's log lines.
	if report.InferredDiscSource == "" {
		if src := inferDiscSourceFromJSON(line); src != "" {
			report.InferredDiscSource = src
		}
	}

	level := getString(entry, "level")
	msg := getString(entry, "msg")
	ts := getString(entry, "time")
	eventType := getString(entry, "event_type")

	// Track debug level.
	if strings.EqualFold(level, "DEBUG") {
		report.IsDebug = true
	}

	// Decisions.
	if decType := getString(entry, "decision_type"); decType != "" {
		report.Decisions = append(report.Decisions, LogDecision{
			TS:             ts,
			DecisionType:   decType,
			DecisionResult: getString(entry, "decision_result"),
			DecisionReason: getString(entry, "decision_reason"),
			Message:        msg,
		})
	}

	// Warnings.
	if strings.EqualFold(level, "WARN") {
		report.Warnings = append(report.Warnings, LogEntry{
			TS:        ts,
			Level:     level,
			Message:   msg,
			EventType: eventType,
			ErrorHint: getString(entry, "error_hint"),
			Extras:    buildExtras(entry),
		})
	}

	// Errors.
	if strings.EqualFold(level, "ERROR") {
		report.Errors = append(report.Errors, LogEntry{
			TS:        ts,
			Level:     level,
			Message:   msg,
			EventType: eventType,
			ErrorHint: getString(entry, "error_hint"),
			Extras:    buildExtras(entry),
		})
	}

	// Stage events.
	if strings.HasPrefix(eventType, "stage_") {
		report.Stages = append(report.Stages, StageEvent{
			TS:              ts,
			EventType:       eventType,
			Stage:           getString(entry, "stage"),
			Message:         msg,
			DurationSeconds: getStageDurationSeconds(entry),
		})
	}
}

// buildExtras returns a map of all log line fields not in knownLogKeys.
func buildExtras(entry map[string]any) map[string]any {
	var extras map[string]any
	for k, v := range entry {
		if knownLogKeys[k] {
			continue
		}
		if extras == nil {
			extras = make(map[string]any)
		}
		extras[k] = v
	}
	return extras
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

func inferDiscSourceFromJSON(rawJSON string) string {
	lower := strings.ToLower(rawJSON)
	if strings.Contains(lower, `"disc_type":"blu-ray"`) || strings.Contains(lower, `"disc_type": "blu-ray"`) {
		if strings.Contains(lower, "uhd") || strings.Contains(lower, "2160") {
			return "4k_bluray"
		}
		return "bluray"
	}
	if strings.Contains(lower, "video_ts") {
		return "dvd"
	}
	return ""
}

// gatherRipCache reads the rip cache metadata for the item.
func gatherRipCache(cfg *config.Config, item *queue.Item) *RipCacheReport {
	if !cfg.RipCache.Enabled {
		return &RipCacheReport{Found: false}
	}

	store := ripcache.New(cfg.RipCacheDir(), cfg.RipCache.MaxGiB)
	cachePath := filepath.Join(cfg.RipCacheDir(), item.DiscFingerprint)

	meta, err := store.GetMetadata(item.DiscFingerprint)
	if err != nil {
		return &RipCacheReport{Path: cachePath, Found: store.HasCache(item.DiscFingerprint)}
	}

	return &RipCacheReport{
		Path:     cachePath,
		Found:    true,
		Metadata: meta,
	}
}

// gatherMediaProbes runs ffprobe on encoded and final files.
func gatherMediaProbes(ctx context.Context, env *ripspec.Envelope, mediaType string) []MediaFileProbe {
	var probes []MediaFileProbe

	if mediaType == "movie" {
		// Single encoded file.
		if asset, ok := env.Assets.FindAsset("encoded", "main"); ok && asset.IsCompleted() {
			p := probeFile(ctx, asset.Path, "encoded", "")
			if p.Error != "" {
				// Staging cleaned up; try final.
				if final, fok := env.Assets.FindAsset("final", "main"); fok && final.IsCompleted() {
					p = probeFile(ctx, final.Path, "final", "")
				}
			}
			probes = append(probes, p)
		}
	} else {
		// TV: probe each encoded episode, fall back to final.
		for _, asset := range env.Assets.Encoded {
			if asset.IsFailed() || strings.TrimSpace(asset.Path) == "" {
				continue
			}
			p := probeFile(ctx, asset.Path, "encoded", asset.EpisodeKey)
			if p.Error != "" {
				if final, ok := env.Assets.FindAsset("final", asset.EpisodeKey); ok && final.IsCompleted() {
					p = probeFile(ctx, final.Path, "final", asset.EpisodeKey)
				}
			}
			probes = append(probes, p)
		}
	}

	return probes
}

func probeFile(ctx context.Context, path, role, episodeKey string) MediaFileProbe {
	result, err := ffprobe.Inspect(ctx, "", path)
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
		DurationSeconds: result.DurationSeconds(),
	}
}
