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
	"time"

	"github.com/five82/spindle/internal/config"
	"github.com/five82/spindle/internal/encodingstate"
	"github.com/five82/spindle/internal/httpapi"
	"github.com/five82/spindle/internal/logs"
	"github.com/five82/spindle/internal/media/ffprobe"
	"github.com/five82/spindle/internal/mediameta"
	"github.com/five82/spindle/internal/queue"
	"github.com/five82/spindle/internal/ripcache"
	"github.com/five82/spindle/internal/ripspec"
)

// stageOrder maps stages to numeric order for furthest-stage computation,
// derived from queue.StageOrder so the enumeration cannot drift from the
// pipeline template. StageCompleted ranks past every execution stage.
var stageOrder = func() map[queue.Stage]int {
	m := make(map[queue.Stage]int, len(queue.StageOrder)+1)
	for i, s := range queue.StageOrder {
		m[s] = i
	}
	m[queue.StageCompleted] = len(queue.StageOrder)
	return m
}()

// Gather collects all audit artifacts for a queue item.
func Gather(ctx context.Context, cfg *config.Config, item *httpapi.ItemResponse) (*Report, error) {
	if item == nil {
		return nil, fmt.Errorf("nil queue item")
	}

	r := &Report{
		Paths: AuditPaths{
			ReviewDir:  cfg.Paths.ReviewDir,
			LibraryDir: cfg.Paths.LibraryDir,
		},
	}

	// Parse envelope for media type and disc source.
	var env ripspec.Envelope
	if ripSpecData := rawJSONString(item.RipSpec); ripSpecData != "" {
		if err := json.Unmarshal([]byte(ripSpecData), &env); err != nil {
			r.addError("parse envelope: %v", err)
		} else {
			r.Envelope = &env
		}
	}

	r.Item = buildItemSummary(item, &env)

	mediaType := resolveMediaType(&env, item)
	mediaHint := inferMediaHintFromMetadata(mediaType)

	// Compute stage gate.
	r.StageGate = computeStageGate(item, mediaType, mediaHint, env.Metadata.DiscSource)

	// Log analysis. A scan error mid-file still leaves usable parsed entries,
	// so keep the partial report alongside the recorded error.
	logReport, logErr := gatherLogs(cfg, item)
	if logErr != nil {
		r.addError("gather logs: %v", logErr)
	}
	if logReport != nil {
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
	if encodingDetails := rawJSONString(item.Encoding); r.StageGate.PhaseEncoded && encodingDetails != "" {
		snap, err := encodingstate.Unmarshal(encodingDetails)
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
	r.Media, r.MediaOmitted = compressMediaProbes(r.Media, r.Analysis.EpisodeConsistency)

	return r, nil
}

func (r *Report) addError(format string, args ...any) {
	r.Errors = append(r.Errors, fmt.Sprintf(format, args...))
}

func rawJSONString(raw json.RawMessage) string {
	if len(raw) == 0 || string(raw) == "null" {
		return ""
	}
	return string(raw)
}

func buildItemSummary(item *httpapi.ItemResponse, env *ripspec.Envelope) ItemSummary {
	summary := ItemSummary{
		ID:              item.ID,
		DiscTitle:       item.DiscTitle,
		Stage:           item.Stage,
		FailedAtStage:   item.FailedAtStage,
		ErrorMessage:    item.ErrorMessage,
		NeedsReview:     item.NeedsReview,
		ReviewReasons:   item.ReviewReasons,
		DiscFingerprint: item.DiscFingerprint,
		CreatedAt:       item.CreatedAt,
		UpdatedAt:       item.UpdatedAt,
		Tasks:           buildTaskSummaries(item.Tasks),
	}
	if env != nil {
		summary.RippedFile = lastCompletedAssetPath(env.Assets.Ripped)
		summary.EncodedFile = lastCompletedAssetPath(env.Assets.Encoded)
		summary.FinalFile = lastCompletedAssetPath(env.Assets.Final)
	}
	return summary
}

// buildTaskSummaries condenses the item's task rows into the audit report's
// compact per-task shape.
func buildTaskSummaries(tasks []httpapi.TaskResponse) []TaskSummary {
	if len(tasks) == 0 {
		return nil
	}
	out := make([]TaskSummary, 0, len(tasks))
	for _, t := range tasks {
		out = append(out, TaskSummary{
			Type:            t.Type,
			State:           t.State,
			Attempts:        t.Attempts,
			Error:           t.Error,
			ProgressPercent: t.Progress.Percent,
			ProgressMessage: t.Progress.Message,
			ActiveAssetKey:  t.ActiveAssetKey,
		})
	}
	return out
}

func lastCompletedAssetPath(assets []ripspec.Asset) string {
	for i := len(assets) - 1; i >= 0; i-- {
		if assets[i].IsCompleted() {
			return assets[i].Path
		}
	}
	return ""
}

// resolveMediaType resolves the item's media type from the envelope, falling
// back to the metadata JSON. Items with neither (pre-identification failures)
// are "unknown"; the log-inferred media hint covers those.
func resolveMediaType(env *ripspec.Envelope, item *httpapi.ItemResponse) string {
	if env.Metadata.MediaType != "" {
		return env.Metadata.MediaType
	}
	metaJSON := rawJSONString(item.Metadata)
	if metaJSON == "" {
		return "unknown"
	}
	if mediameta.FromJSON(metaJSON, item.DiscTitle).Movie {
		return "movie"
	}
	return "tv"
}

func computeStageGate(item *httpapi.ItemResponse, mediaType, mediaHint, discSource string) StageGate {
	furthest := queue.Stage(item.Stage)
	if furthest == queue.StageFailed && item.FailedAtStage != "" {
		furthest = queue.Stage(item.FailedAtStage)
	}

	order := stageOrder[furthest]

	// Task rows are the exact record of what ran. The pipeline is a DAG, so
	// the linear ordinal both lags running tasks during overlap and cannot
	// say whether a parallel branch (encoding vs analysis) has started. When
	// the rows carry dependency edges, a stage is reached iff its own task
	// left pending or a started task transitively depends on it; the ordinal
	// comparison stays as the fallback for items whose task rows are gone
	// (the gap between a retry and the scheduler recompiling them) or
	// edge-less.
	started := make(map[queue.Stage]bool, len(item.Tasks))
	deps := make(map[queue.Stage][]queue.Stage, len(item.Tasks))
	hasEdges := false
	for _, t := range item.Tasks {
		typ := queue.Stage(t.Type)
		for _, d := range t.DependsOn {
			deps[typ] = append(deps[typ], queue.Stage(d))
			hasEdges = true
		}
		if queue.TaskState(t.State) == queue.TaskPending {
			continue
		}
		started[typ] = true
		if o, ok := stageOrder[typ]; ok && o > order {
			order = o
			furthest = typ
		}
	}
	reached := make(map[queue.Stage]bool, len(item.Tasks))
	if hasEdges {
		var mark func(s queue.Stage)
		mark = func(s queue.Stage) {
			if reached[s] {
				return
			}
			reached[s] = true
			for _, d := range deps[s] {
				mark(d)
			}
		}
		for s := range started {
			mark(s)
		}
	}
	phaseReached := func(s queue.Stage) bool {
		if hasEdges {
			return reached[s]
		}
		return order >= stageOrder[s]
	}

	return StageGate{
		FurthestStage: string(furthest),
		MediaType:     mediaType,
		MediaHint:     mediaHint,
		DiscSource:    discSource,

		PhaseLogs:       true,
		PhaseRipCache:   phaseReached(queue.StageRipping),
		PhaseEpisodeID:  mediaType == "tv" && phaseReached(queue.StageEpisodeIdentification),
		PhaseEncoded:    phaseReached(queue.StageEncoding),
		PhaseCrop:       phaseReached(queue.StageEncoding),
		PhaseSubtitles:  phaseReached(queue.StageSubtitling),
		PhaseCommentary: phaseReached(queue.StageAnalysis),
		PhaseExtVal:     phaseReached(queue.StageEncoding) && discSource != "dvd",
	}
}

// itemLogGrace admits log lines slightly older than the item row: disc
// detection and enqueue events precede item creation by a few seconds.
const itemLogGrace = 2 * time.Minute

// gatherLogs parses structured log entries for this item from every daemon
// log file that overlaps the item's lifetime: the file active at creation
// plus all later files (daemon restarts mid-item start a new file). A scan
// error is returned alongside the partially-filled report.
func gatherLogs(cfg *config.Config, item *httpapi.ItemResponse) (*LogAnalysis, error) {
	logPaths := findLogFiles(cfg.DaemonLogDir(), item.CreatedAt)
	if len(logPaths) == 0 {
		return nil, fmt.Errorf("no log file found for item %d", item.ID)
	}

	// Item IDs and disc titles recur across queue clears and re-rips; the
	// creation-time cutoff keeps earlier items' lines out of this report.
	var cutoff time.Time
	if created, err := time.Parse(time.RFC3339, item.CreatedAt); err == nil {
		cutoff = created.Add(-itemLogGrace)
	}

	report := &LogAnalysis{Paths: logPaths}
	var firstErr error
	for _, path := range logPaths {
		if err := scanLogFile(path, item, report, cutoff); err != nil && firstErr == nil {
			firstErr = err
		}
	}

	report.Events, report.EventsOmitted = compactProgressEvents(report.Events)

	return report, firstErr
}

func scanLogFile(path string, item *httpapi.ItemResponse, report *LogAnalysis, cutoff time.Time) error {
	f, err := os.Open(path)
	if err != nil {
		return fmt.Errorf("open log: %w", err)
	}
	defer func() { _ = f.Close() }()

	scanner := bufio.NewScanner(f)
	scanner.Buffer(make([]byte, 0, 4096), 1024*1024)

	for scanner.Scan() {
		line := scanner.Text()
		report.LinesScanned++
		parseLogLine(line, item, report, cutoff)
	}
	if err := scanner.Err(); err != nil {
		return fmt.Errorf("scan log %s: %w", filepath.Base(path), err)
	}
	return nil
}

// findLogFiles locates the daemon log files covering the item's lifetime: the
// file active when the item was created and every later file. Log files are
// named spindle-{timestamp}.log in the state directory.
func findLogFiles(stateDir, createdAt string) []string {
	entries, err := os.ReadDir(stateDir)
	if err != nil {
		return nil
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
		return nil
	}

	sort.Strings(logFiles)

	// Normalize createdAt to "YYYY-MM-DD HH:MM:SS" so it matches the format
	// produced by extractLogTimestamp. createdAt may use "T" separator and
	// trailing "Z" (e.g. "2026-03-22T02:18:41Z").
	normalized := strings.Replace(createdAt, "T", " ", 1)
	if idx := strings.IndexByte(normalized, 'Z'); idx > 0 {
		normalized = normalized[:idx]
	}

	// Start at the last log file whose timestamp is <= createdAt (the file
	// that was active at creation), defaulting to the earliest.
	start := 0
	for i, path := range logFiles {
		ts := extractLogTimestamp(filepath.Base(path))
		if ts <= normalized {
			start = i
		}
	}

	return logFiles[start:]
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
// Lines timestamped before cutoff belong to earlier items that shared this
// item's ID, fingerprint, or title (IDs reset on queue clear) and are dropped;
// a zero cutoff disables the clamp.
func parseLogLine(line string, item *httpapi.ItemResponse, report *LogAnalysis, cutoff time.Time) {
	line = strings.TrimSpace(line)
	if line == "" || line[0] != '{' {
		return
	}

	var entry map[string]any
	if err := json.Unmarshal([]byte(line), &entry); err != nil {
		return
	}

	if !cutoff.IsZero() {
		if ts, err := time.Parse(time.RFC3339Nano, getString(entry, "time")); err == nil && ts.Before(cutoff) {
			return
		}
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
	decType := getString(entry, "decision_type")
	if decType != "" {
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
	isStageEvent := strings.HasPrefix(eventType, "stage_")
	if isStageEvent {
		report.Stages = append(report.Stages, StageEvent{
			TS:              ts,
			EventType:       eventType,
			Stage:           getString(entry, "stage"),
			Message:         msg,
			DurationSeconds: getStageDurationSeconds(entry),
		})
	}

	// Item-specific INFO events that are not decisions or stage transitions.
	// These carry progress/operation visibility such as encoding_progress,
	// transcription completions, mux/copy progress, and plan summaries.
	if strings.EqualFold(level, "INFO") && eventType != "" && decType == "" && !isStageEvent {
		report.Events = append(report.Events, LogEntry{
			TS:        ts,
			Level:     level,
			Message:   msg,
			EventType: eventType,
			Extras:    buildExtras(entry),
		})
	}
}

// maxProgressEventsPerType caps how many entries of each *_progress event
// type survive into the report. Long encodes emit hundreds of near-identical
// ticks that inflate the JSON without adding audit signal.
const maxProgressEventsPerType = 20

// compactProgressEvents downsamples *_progress event types that exceed the
// cap, keeping the first, last, and an evenly-strided subset in between.
// Non-progress events always pass through. Returns the kept events and the
// omitted count.
func compactProgressEvents(events []LogEntry) ([]LogEntry, int) {
	counts := make(map[string]int)
	for _, e := range events {
		if strings.HasSuffix(e.EventType, "_progress") {
			counts[e.EventType]++
		}
	}
	needsCompaction := false
	for _, c := range counts {
		if c > maxProgressEventsPerType {
			needsCompaction = true
			break
		}
	}
	if !needsCompaction {
		return events, 0
	}

	seen := make(map[string]int)
	kept := make([]LogEntry, 0, len(events))
	omitted := 0
	for _, e := range events {
		total := counts[e.EventType]
		if total <= maxProgressEventsPerType {
			kept = append(kept, e)
			continue
		}
		i := seen[e.EventType]
		seen[e.EventType]++
		stride := (total + maxProgressEventsPerType - 1) / maxProgressEventsPerType
		if i%stride == 0 || i == total-1 {
			kept = append(kept, e)
		} else {
			omitted++
		}
	}
	return kept, omitted
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

// getStageDurationSeconds extracts stage duration in seconds; the format
// contract (string vs legacy nanoseconds) lives with the writer in
// internal/logs.
func getStageDurationSeconds(entry map[string]any) float64 {
	return logs.DurationSeconds(entry["stage_duration"])
}

func logLineMatchesItem(entry map[string]any, item *httpapi.ItemResponse) bool {
	if item == nil {
		return false
	}
	// An explicit item_id is authoritative: a mismatch excludes the line even
	// when the fingerprint or disc title also matches (same disc, other item).
	if id, ok := getFloat(entry, "item_id"); ok {
		return int64(id) == item.ID
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
	if getString(entry, "decision_type") == logs.DecisionTMDBSearch {
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
	if getString(entry, "decision_type") == logs.DecisionBDInfoAvailability {
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
func gatherRipCache(cfg *config.Config, item *httpapi.ItemResponse) *RipCacheReport {
	if !cfg.RipCache.Enabled {
		return &RipCacheReport{Disabled: true}
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
		Metadata: redactRipCacheMetadata(meta),
	}
}

func redactRipCacheMetadata(meta *ripcache.EntryMetadata) *ripCacheMetadata {
	if meta == nil {
		return nil
	}
	return &ripCacheMetadata{
		Version:     meta.Version,
		Fingerprint: meta.Fingerprint,
		DiscTitle:   meta.DiscTitle,
		CachedAt:    meta.CachedAt,
		TitleCount:  meta.TitleCount,
		TotalBytes:  meta.TotalBytes,
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
	for _, stage := range []string{ripspec.AssetKindFinal, ripspec.AssetKindSubtitled, ripspec.AssetKindEncoded} {
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
