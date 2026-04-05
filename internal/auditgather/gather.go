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
	queue.StageIdentification:        0,
	queue.StageRipping:               1,
	queue.StageEpisodeIdentification: 2,
	queue.StageEncoding:              3,
	queue.StageAudioAnalysis:         4,
	queue.StageSubtitling:            5,
	queue.StageOrganizing:            6,
	queue.StageCompleted:             7,
}

// Gather collects all audit artifacts for a queue item.
func Gather(ctx context.Context, cfg *config.Config, item *queue.Item) (*Report, error) {
	if item == nil {
		return nil, fmt.Errorf("nil queue item")
	}

	r := &Report{
		Item: buildItemSummary(item),
		Paths: AuditPaths{
			ReviewDir:  cfg.Paths.ReviewDir,
			LibraryDir: cfg.Paths.LibraryDir,
		},
	}

	// Parse envelope for media type and disc source.
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

	mediaHint := inferMediaHintFromMetadata(mediaType)

	// Compute stage gate.
	r.StageGate = computeStageGate(item, mediaType, mediaHint, env.Metadata.DiscSource)

	// Log analysis.
	logReport, logErr := gatherLogs(cfg, item)
	if logErr != nil {
		r.addError("gather logs: %v", logErr)
	} else {
		r.Logs = logReport
	}

	// Refine context from logs when envelope metadata is incomplete.
	if r.Logs != nil {
		if r.StageGate.DiscSource == "" && r.Logs.InferredDiscSource != "" {
			r.StageGate.DiscSource = r.Logs.InferredDiscSource
			r.StageGate.PhaseExtVal = r.StageGate.PhaseEncoded && r.StageGate.DiscSource != "dvd"
		}
		if r.StageGate.MediaHint == "" && r.Logs.InferredMediaHint != "" {
			r.StageGate.MediaHint = r.Logs.InferredMediaHint
		}
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
		RippedFile:      item.RippedFile,
		EncodedFile:     item.EncodedFile,
		FinalFile:       item.FinalFile,
	}
}

func computeStageGate(item *queue.Item, mediaType, mediaHint, discSource string) StageGate {
	furthest := item.Stage
	if item.Stage == queue.StageFailed && item.FailedAtStage != "" {
		furthest = queue.Stage(item.FailedAtStage)
	}

	order := stageOrder[furthest]

	return StageGate{
		FurthestStage: string(furthest),
		MediaType:     mediaType,
		MediaHint:     mediaHint,
		DiscSource:    discSource,

		PhaseLogs:       true,
		PhaseRipCache:   order >= stageOrder[queue.StageRipping],
		PhaseEpisodeID:  mediaType == "tv" && order >= stageOrder[queue.StageEpisodeIdentification],
		PhaseEncoded:    order >= stageOrder[queue.StageEncoding],
		PhaseCrop:       order >= stageOrder[queue.StageEncoding],
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
		parseLogLine(line, item, report)
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

	// Normalize createdAt to "YYYY-MM-DD HH:MM:SS" so it matches the format
	// produced by extractLogTimestamp. createdAt may use "T" separator and
	// trailing "Z" (e.g. "2026-03-22T02:18:41Z").
	normalized := strings.Replace(createdAt, "T", " ", 1)
	if idx := strings.IndexByte(normalized, 'Z'); idx > 0 {
		normalized = normalized[:idx]
	}

	// Find the last log file whose timestamp is <= createdAt.
	best := logFiles[0] // Default to earliest if none match.
	for _, path := range logFiles {
		ts := extractLogTimestamp(filepath.Base(path))
		if ts <= normalized {
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
// Lines are associated with the item by item_id when present, otherwise by
// stable identifiers such as disc fingerprint or disc title/label fields.
func parseLogLine(line string, item *queue.Item, report *LogAnalysis) {
	line = strings.TrimSpace(line)
	if line == "" || line[0] != '{' {
		return
	}

	var entry map[string]any
	if err := json.Unmarshal([]byte(line), &entry); err != nil {
		return
	}

	if !logLineMatchesItem(entry, item) {
		return
	}

	// Infer disc source and media hint from this item's log lines.
	if report.InferredDiscSource == "" {
		if src := inferDiscSource(entry); src != "" {
			report.InferredDiscSource = src
		}
	}
	if report.InferredMediaHint == "" {
		if hint := inferMediaHint(entry); hint != "" {
			report.InferredMediaHint = hint
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
			Extras:         buildExtras(entry),
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

func logLineMatchesItem(entry map[string]any, item *queue.Item) bool {
	if item == nil {
		return false
	}
	if id, ok := getFloat(entry, "item_id"); ok && int64(id) == item.ID {
		return true
	}
	if fp := getString(entry, "fingerprint"); fp != "" && item.DiscFingerprint != "" && strings.EqualFold(strings.TrimSpace(fp), strings.TrimSpace(item.DiscFingerprint)) {
		return true
	}
	for _, key := range []string{"disc_title", "label", "volume_id", "raw_title"} {
		if sameAuditString(getString(entry, key), item.DiscTitle) {
			return true
		}
	}
	return false
}

func inferMediaHintFromMetadata(mediaType string) string {
	switch strings.ToLower(strings.TrimSpace(mediaType)) {
	case "movie", "tv":
		return strings.ToLower(strings.TrimSpace(mediaType))
	default:
		return ""
	}
}

func sameAuditString(a, b string) bool {
	a = strings.TrimSpace(a)
	b = strings.TrimSpace(b)
	if a == "" || b == "" {
		return false
	}
	return strings.EqualFold(a, b)
}

func inferMediaHint(entry map[string]any) string {
	if getString(entry, "decision_type") == "tmdb_search" {
		switch strings.ToLower(strings.TrimSpace(getString(entry, "decision_result"))) {
		case "tv", "movie":
			return strings.ToLower(strings.TrimSpace(getString(entry, "decision_result")))
		}
	}
	return ""
}

func inferDiscSource(entry map[string]any) string {
	if discType := strings.ToLower(strings.TrimSpace(getString(entry, "disc_type"))); discType != "" {
		switch discType {
		case "blu-ray", "bluray", "bd":
			return "bluray"
		case "dvd":
			return "dvd"
		}
	}
	if getString(entry, "decision_type") == "bdinfo_availability" {
		switch strings.ToLower(strings.TrimSpace(getString(entry, "decision_result"))) {
		case "bluray":
			return "bluray"
		case "dvd":
			return "dvd"
		case "unknown":
			return "unknown"
		}
	}
	lower := strings.ToLower(getString(entry, "decision_reason") + " " + getString(entry, "msg") + " " + getString(entry, "device"))
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
		// Prefer final (most complete, with subtitles), fall back to subtitled, then encoded.
		p := probeBestAsset(ctx, env, "main")
		if p.Path != "" {
			probes = append(probes, p)
		}
	} else {
		// TV: probe each episode, preferring final > subtitled > encoded.
		for _, asset := range env.Assets.Encoded {
			if asset.IsFailed() || strings.TrimSpace(asset.Path) == "" {
				continue
			}
			p := probeBestAsset(ctx, env, asset.EpisodeKey)
			if p.Path != "" {
				probes = append(probes, p)
			}
		}
	}

	return probes
}

// probeBestAsset probes the most complete version of an asset: final > subtitled > encoded.
func probeBestAsset(ctx context.Context, env *ripspec.Envelope, episodeKey string) MediaFileProbe {
	for _, stage := range []string{"final", "subtitled", "encoded"} {
		asset, ok := env.Assets.FindAsset(stage, episodeKey)
		if !ok || !asset.IsCompleted() {
			continue
		}
		p := probeFile(ctx, asset.Path, stage, episodeKey)
		if p.Error == "" {
			return p
		}
	}
	return MediaFileProbe{}
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
		Path:            path,
		Role:            role,
		EpisodeKey:      episodeKey,
		Probe:           result,
		SizeBytes:       result.SizeBytes(),
		DurationSeconds: result.DurationSeconds(),
	}
}
