package commentary

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"math"
	"math/bits"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strconv"
	"strings"

	"log/slog"

	"spindle/internal/config"
	"spindle/internal/deps"
	"spindle/internal/logging"
	"spindle/internal/media/ffprobe"
)

const (
	defaultSampleRate          = 16000
	defaultFrameMs             = 20
	minSpeechRatioDeltaPrimary = 0.05
	minRelativeSilenceDelta    = 0.08
	minRelativeSimilarityDelta = 0.15
	downmixSimilarityMin       = 0.85
	downmixCorrelationMin      = 0.90
)

// Result captures detected commentary tracks plus per-track decisions.
type Result struct {
	Indices   []int
	Decisions []Decision
}

// Decision captures commentary classifier output for a single stream.
type Decision struct {
	Index    int
	Include  bool
	Reason   string
	Metadata Metadata
	Metrics  Metrics
}

// Metadata captures stream-tag signals for the classifier.
type Metadata struct {
	Language string
	Title    string
	Positive bool
	Negative string
}

// Metrics captures audio analysis signals for the classifier.
type Metrics struct {
	SpeechRatio              float64
	SpeechOverlapWithPrimary float64
	SpeechInPrimarySilence   float64
	FingerprintSimilarity    float64
	PrimarySpeechRatio       float64
	SpeechTimingCorrelation  float64
}

// Detect runs commentary detection for the provided media path.
func Detect(ctx context.Context, cfg *config.Config, path string, probe ffprobe.Result, primaryIndex int, logger *slog.Logger) (Result, error) {
	if cfg == nil || !cfg.CommentaryDetection.Enabled {
		return Result{}, nil
	}
	settings := cfg.CommentaryDetection
	if strings.TrimSpace(path) == "" {
		return Result{}, errors.New("commentary detect: empty path")
	}
	logger = logging.WithContext(ctx, logging.NewComponentLogger(logger, "commentary"))

	ffmpegBinary := deps.ResolveFFmpegPath()
	if strings.TrimSpace(ffmpegBinary) == "" {
		ffmpegBinary = "ffmpeg"
	}
	if _, err := exec.LookPath(ffmpegBinary); err != nil {
		logger.Info("commentary detection skipped (ffmpeg missing)", logging.String("binary", ffmpegBinary))
		return Result{}, nil
	}
	if _, err := exec.LookPath("fpcalc"); err != nil {
		logger.Info("commentary detection skipped (fpcalc missing)")
		return Result{}, nil
	}
	if _, err := exec.LookPath("cc"); err != nil {
		logger.Info("commentary detection skipped (cgo toolchain missing)")
		return Result{}, nil
	}

	primaryStream, ok := findStream(probe.Streams, primaryIndex)
	if !ok {
		return Result{}, fmt.Errorf("commentary detect: primary stream %d missing", primaryIndex)
	}
	primaryDuration := streamDurationSeconds(primaryStream)
	if primaryDuration <= 0 {
		primaryDuration = probe.DurationSeconds()
	}
	windows := buildWindows(primaryDuration, settings.SampleWindows, settings.WindowSeconds)
	if len(windows) == 0 {
		return Result{}, nil
	}
	logger.Debug("commentary detection criteria",
		logging.Int("primary_stream", primaryIndex),
		logging.String("languages", strings.Join(normalizedLanguages(settings.Languages), ",")),
		logging.Int("channels", settings.Channels),
		logging.Int("sample_windows", settings.SampleWindows),
		logging.Int("window_seconds", settings.WindowSeconds),
		logging.Float64("speech_ratio_min_commentary", settings.SpeechRatioMinCommentary),
		logging.Float64("speech_ratio_max_music", settings.SpeechRatioMaxMusic),
		logging.Float64("speech_overlap_primary_min", settings.SpeechOverlapPrimaryMin),
		logging.Float64("speech_overlap_primary_max_audio_description", settings.SpeechOverlapPrimaryMaxAD),
		logging.Float64("speech_in_silence_max", settings.SpeechInSilenceMax),
		logging.Float64("fingerprint_similarity_duplicate", settings.FingerprintSimilarityDuplicate),
		logging.Int("duration_tolerance_seconds", settings.DurationToleranceSeconds),
		logging.Float64("duration_tolerance_ratio", settings.DurationToleranceRatio),
	)

	primarySpeech, err := analyzeSpeech(ctx, ffmpegBinary, path, primaryIndex, windows)
	if err != nil {
		return Result{}, fmt.Errorf("commentary detect: primary speech analysis: %w", err)
	}
	primarySpeechRatio := primarySpeech.speechRatio()

	workDir, cleanup, err := tempWorkDir(path)
	if err != nil {
		return Result{}, err
	}
	defer cleanup()

	primaryFingerprint, err := fingerprintForStream(ctx, ffmpegBinary, path, primaryIndex, windows, filepath.Join(workDir, fmt.Sprintf("primary-%d.wav", primaryIndex)))
	if err != nil {
		return Result{}, fmt.Errorf("commentary detect: primary fingerprint: %w", err)
	}

	candidates, prefilter := evaluateCandidates(probe.Streams, settings, primaryDuration, primaryIndex)
	decisions := make([]Decision, 0, len(candidates))
	var indices []int
	candidateIndices := make([]int, 0, len(candidates))
	prefilterRejections := make([]string, 0)
	prefilterReasonCounts := make(map[string]int)
	verbosePrefilter := os.Getenv("SPD_DEBUG_COMMENTARY_VERBOSE") != ""
	for _, decision := range prefilter {
		if verbosePrefilter {
			logger.Debug("commentary candidate evaluated",
				logging.Int("stream_index", decision.Index),
				logging.Bool("candidate", decision.Candidate),
				logging.String("reason", decision.Reason),
				logging.String("language", decision.Language),
				logging.String("title", decision.Title),
				logging.Int("channels", decision.Channels),
				logging.Float64("duration_seconds", decision.Duration),
				logging.Bool("metadata_positive", decision.MetadataPositive),
				logging.String("metadata_negative", decision.MetadataNegative),
			)
		}
		if !decision.Candidate {
			reason := strings.TrimSpace(decision.Reason)
			if reason == "" {
				reason = "unknown"
			}
			prefilterReasonCounts[reason]++
			prefilterRejections = append(prefilterRejections, formatPrefilterSummary(decision, reason))
		}
		if decision.Candidate {
			candidateIndices = append(candidateIndices, decision.Index)
		}
	}

	for _, cand := range candidates {
		if cand.stream.Index == primaryIndex {
			continue
		}
		meta := Metadata{
			Language: cand.language,
			Title:    cand.title,
			Positive: cand.metadataPositive,
			Negative: cand.metadataNegative,
		}
		decision := Decision{
			Index:    cand.stream.Index,
			Metadata: meta,
		}
		if meta.Negative != "" {
			decision.Include = false
			decision.Reason = meta.Negative
			decisions = append(decisions, decision)
			continue
		}

		candSpeech, err := analyzeSpeech(ctx, ffmpegBinary, path, cand.stream.Index, windows)
		if err != nil {
			logger.Warn("commentary candidate analysis failed; candidate dropped",
				logging.Int("stream", cand.stream.Index),
				logging.Error(err),
				logging.String(logging.FieldEventType, "commentary_analysis_failed"),
				logging.String(logging.FieldErrorHint, "verify ffmpeg access and try re-encoding or disable commentary detection"),
			)
			decision.Include = false
			decision.Reason = "analysis_failed"
			decisions = append(decisions, decision)
			continue
		}
		speechRatio := candSpeech.speechRatio()
		overlap, inSilence, correlation := speechOverlap(primarySpeech, candSpeech)

		fingerprint, err := fingerprintForStream(ctx, ffmpegBinary, path, cand.stream.Index, windows, filepath.Join(workDir, fmt.Sprintf("cand-%d.wav", cand.stream.Index)))
		if err != nil {
			failure := classifyFingerprintFailure(err)
			logger.Warn("commentary candidate fingerprint failed",
				logging.Int("stream", cand.stream.Index),
				logging.String(logging.FieldEventType, "commentary_fingerprint_failed"),
				logging.String("cause", failure.Cause),
				logging.String("attention", failure.Attention),
				logging.String(logging.FieldImpact, "candidate_dropped"),
				logging.String(logging.FieldErrorHint, failure.Hint),
				logging.Error(err),
			)
			decision.Include = false
			decision.Reason = failure.Reason
			decisions = append(decisions, decision)
			continue
		}
		similarity := compareFingerprints(primaryFingerprint, fingerprint)

		decision.Metrics = Metrics{
			SpeechRatio:              speechRatio,
			SpeechOverlapWithPrimary: overlap,
			SpeechInPrimarySilence:   inSilence,
			FingerprintSimilarity:    similarity,
			PrimarySpeechRatio:       primarySpeechRatio,
			SpeechTimingCorrelation:  correlation,
		}
		decision.Include, decision.Reason = classify(decision.Metrics, meta, settings)
		decisions = append(decisions, decision)
	}

	decisions = applyRelativeScoring(settings, decisions, logger)
	// Speaker embedding is primary signal for voice identity
	if speakerEmbeddingAvailable(cfg) {
		decisions = applySpeakerEmbeddingClassification(ctx, cfg, ffmpegBinary, path, windows, workDir, primaryIndex, decisions, logger)
	}
	// WhisperX is fallback for still-ambiguous candidates
	if whisperxAvailable(cfg) {
		decisions = applyWhisperXClassification(ctx, cfg, ffmpegBinary, path, windows, workDir, decisions, logger)
	}

	// Check if ALL candidates failed due to analysis/fingerprint errors
	analysisFailures := 0
	for _, d := range decisions {
		if strings.HasPrefix(d.Reason, "analysis_failed") ||
			strings.HasPrefix(d.Reason, "fingerprint_failed") {
			analysisFailures++
		}
	}
	if len(candidates) > 0 && analysisFailures == len(candidates) {
		logger.Warn("all commentary candidates failed analysis",
			logging.Int("candidate_count", len(candidates)),
			logging.Int("analysis_failures", analysisFailures),
			logging.String(logging.FieldEventType, "commentary_all_candidates_failed"),
			logging.String(logging.FieldErrorHint, "check ffmpeg, disk permissions, codec support"),
			logging.String(logging.FieldImpact, "all commentary tracks excluded due to analysis failures"),
		)
	}

	indices = indicesFromDecisions(decisions)
	for _, decision := range decisions {
		thresholds := []logging.Attr{
			logging.Float64("speech_ratio_min_commentary", settings.SpeechRatioMinCommentary),
			logging.Float64("speech_ratio_max_music", settings.SpeechRatioMaxMusic),
			logging.Float64("speech_overlap_primary_min", settings.SpeechOverlapPrimaryMin),
			logging.Float64("speech_in_silence_max", settings.SpeechInSilenceMax),
			logging.Float64("fingerprint_similarity_duplicate", settings.FingerprintSimilarityDuplicate),
		}
		logger.Debug("commentary decision",
			logging.Int("stream_index", decision.Index),
			logging.Bool("included", decision.Include),
			logging.String("reason", decision.Reason),
			logging.String("language", decision.Metadata.Language),
			logging.String("title", decision.Metadata.Title),
			logging.Bool("metadata_positive", decision.Metadata.Positive),
			logging.Float64("speech_ratio", decision.Metrics.SpeechRatio),
			logging.Float64("speech_overlap_primary", decision.Metrics.SpeechOverlapWithPrimary),
			logging.Float64("speech_in_primary_silence", decision.Metrics.SpeechInPrimarySilence),
			logging.Float64("speech_timing_correlation", decision.Metrics.SpeechTimingCorrelation),
			logging.Float64("fingerprint_similarity", decision.Metrics.FingerprintSimilarity),
			logging.Group("thresholds", thresholds...),
		)
	}
	sort.Ints(candidateIndices)
	sort.Slice(decisions, func(i, j int) bool {
		return decisions[i].Index < decisions[j].Index
	})

	reasonCounts := make(map[string]int)
	acceptedCount := 0
	rejectedCount := 0

	sort.Slice(candidates, func(i, j int) bool {
		return candidates[i].stream.Index < candidates[j].stream.Index
	})
	for _, decision := range decisions {
		reason := strings.TrimSpace(decision.Reason)
		if reason == "" {
			reason = "unknown"
		}
		reasonCounts[reason]++
		if decision.Include {
			acceptedCount++
		} else {
			rejectedCount++
		}
	}

	infoAttrs := []logging.Attr{
		logging.String(logging.FieldEventType, "decision_summary"),
		logging.String(logging.FieldDecisionType, "commentary_detection"),
		logging.Int("candidate_count", len(candidates)),
		logging.Int("accepted_count", acceptedCount),
		logging.Int("rejected_count", rejectedCount),
	}
	for _, cand := range candidates {
		infoAttrs = append(infoAttrs, logging.String(fmt.Sprintf("candidate_%d", cand.stream.Index), formatCandidateValue(cand)))
	}
	for _, decision := range decisions {
		reason := strings.TrimSpace(decision.Reason)
		if reason == "" {
			reason = "unknown"
		}
		key := fmt.Sprintf("rejected_%d", decision.Index)
		if decision.Include {
			key = fmt.Sprintf("accepted_%d", decision.Index)
		}
		infoAttrs = append(infoAttrs, logging.String(key, formatDecisionValue(decision, reason)))
	}
	if len(reasonCounts) > 0 {
		infoAttrs = append(infoAttrs, logging.Int("reason_count", len(reasonCounts)))
		infoAttrs = appendReasonCounts(infoAttrs, "reason", reasonCounts)
	}

	logger.Info("commentary selection summary",
		logging.Args(infoAttrs...)...,
	)
	if len(prefilterRejections) > 0 {
		debugAttrs := []logging.Attr{
			logging.String(logging.FieldEventType, "commentary_prefilter"),
			logging.Int("candidate_count", len(candidates)),
		}
		for idx, rejection := range prefilterRejections {
			debugAttrs = append(debugAttrs, logging.String(fmt.Sprintf("prefilter_reject_%d", idx+1), rejection))
		}
		if len(prefilterReasonCounts) > 0 {
			debugAttrs = append(debugAttrs, logging.Int("prefilter_reason_count", len(prefilterReasonCounts)))
			debugAttrs = appendReasonCounts(debugAttrs, "prefilter_reason", prefilterReasonCounts)
		}
		debugAttrs = append(debugAttrs, logging.Group("thresholds",
			logging.Float64("speech_ratio_min_commentary", settings.SpeechRatioMinCommentary),
			logging.Float64("speech_ratio_max_music", settings.SpeechRatioMaxMusic),
			logging.Float64("speech_overlap_primary_min", settings.SpeechOverlapPrimaryMin),
			logging.Float64("speech_overlap_primary_max_audio_description", settings.SpeechOverlapPrimaryMaxAD),
			logging.Float64("speech_in_silence_max", settings.SpeechInSilenceMax),
			logging.Float64("fingerprint_similarity_duplicate", settings.FingerprintSimilarityDuplicate),
		))
		logger.Debug("commentary prefilter summary",
			logging.Args(debugAttrs...)...,
		)
	}

	return Result{Indices: indices, Decisions: decisions}, nil
}

func formatDecisionValue(decision Decision, reason string) string {
	lang := decision.Metadata.Language
	if lang == "" {
		lang = "und"
	}
	details := ""
	switch reason {
	case "commentary_only", "mixed_commentary", "metadata_commentary", "commentary_relative", "whisperx_commentary", "speaker_commentary":
		details = fmt.Sprintf(" | speech %.0f%% | similarity %.0f%%", decision.Metrics.SpeechRatio*100, decision.Metrics.FingerprintSimilarity*100)
	case "music_or_silent":
		details = fmt.Sprintf(" | speech %.0f%% (low)", decision.Metrics.SpeechRatio*100)
	case "duplicate_downmix":
		details = fmt.Sprintf(" | similarity %.0f%% (high)", decision.Metrics.FingerprintSimilarity*100)
	case "downmix_correlation":
		details = fmt.Sprintf(" | similarity %.0f%% | timing correlation %.0f%%",
			decision.Metrics.FingerprintSimilarity*100,
			decision.Metrics.SpeechTimingCorrelation*100,
		)
	case "audio_description", "audio_description_outlier", "whisperx_audio_description", "speaker_audio_description":
		details = fmt.Sprintf(" | silence speech %.0f%% | overlap %.0f%% | similarity %.0f%%",
			decision.Metrics.SpeechInPrimarySilence*100,
			decision.Metrics.SpeechOverlapWithPrimary*100,
			decision.Metrics.FingerprintSimilarity*100,
		)
	case "speaker_same_voices":
		details = fmt.Sprintf(" | similarity %.0f%% (same voices)", decision.Metrics.FingerprintSimilarity*100)
	case "fingerprint_failed":
		details = " | fingerprint failed"
	case "fingerprint_failed_stream_missing":
		details = " | ffmpeg stream missing"
	case "fingerprint_failed_decode":
		details = " | ffmpeg decode failed"
	case "fingerprint_failed_fpcalc":
		details = " | fpcalc failed"
	case "analysis_failed":
		details = " | speech analysis failed"
	}

	return fmt.Sprintf("%s | %s%s", lang, reason, details)
}

func formatCandidateValue(cand candidate) string {
	lang := cand.language
	if lang == "" {
		lang = "und"
	}
	title := strings.TrimSpace(cand.title)
	if title == "" {
		title = "untitled"
	}
	durationLabel := ""
	if cand.duration > 0 {
		durationLabel = fmt.Sprintf("%.0fs", cand.duration)
	} else {
		durationLabel = "unknown"
	}
	return fmt.Sprintf("%s | %dch | %s | %s", lang, cand.channels, durationLabel, title)
}

func appendReasonCounts(attrs []logging.Attr, prefix string, counts map[string]int) []logging.Attr {
	if len(counts) == 0 {
		return attrs
	}
	keys := make([]string, 0, len(counts))
	for key := range counts {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	for _, key := range keys {
		label := fmt.Sprintf("%s_%s", prefix, key)
		attrs = append(attrs, logging.Int(label, counts[key]))
	}
	return attrs
}

func formatPrefilterSummary(decision candidateDecision, reason string) string {
	status := "Rejected"
	if decision.Candidate {
		status = "Candidate"
	}
	lang := decision.Language
	if lang == "" {
		lang = "und"
	}
	title := strings.TrimSpace(decision.Title)
	if title == "" {
		title = "untitled"
	}
	durationLabel := ""
	if decision.Duration > 0 {
		durationLabel = fmt.Sprintf("%.0fs", decision.Duration)
	} else {
		durationLabel = "unknown"
	}
	return fmt.Sprintf("#%d (%s): %s (%s) [Channels: %d, Duration: %s, Title: %s]",
		decision.Index,
		lang,
		status,
		reason,
		decision.Channels,
		durationLabel,
		title,
	)
}

type candidate struct {
	stream           ffprobe.Stream
	language         string
	title            string
	channels         int
	duration         float64
	metadataPositive bool
	metadataNegative string
}

type candidateDecision struct {
	Index            int
	Candidate        bool
	Reason           string
	Language         string
	Title            string
	Channels         int
	Duration         float64
	MetadataPositive bool
	MetadataNegative string
}

func evaluateCandidates(streams []ffprobe.Stream, cfg config.CommentaryDetection, primaryDuration float64, primaryIndex int) ([]candidate, []candidateDecision) {
	result := make([]candidate, 0)
	decisions := make([]candidateDecision, 0)
	langs := normalizedLanguages(cfg.Languages)
	for _, stream := range streams {
		if !strings.EqualFold(stream.CodecType, "audio") {
			continue
		}
		language := normalizeLanguage(stream.Tags)
		channels := channelCount(stream)
		duration := streamDurationSeconds(stream)
		title := normalizeTitle(stream.Tags)
		metaPos, metaNeg := classifyMetadata(title)
		decision := candidateDecision{
			Index:            stream.Index,
			Language:         language,
			Title:            title,
			Channels:         channels,
			Duration:         duration,
			MetadataPositive: metaPos,
			MetadataNegative: metaNeg,
		}
		switch {
		case stream.Index == primaryIndex:
			decision.Candidate = false
			decision.Reason = "primary_stream"
		case language == "":
			decision.Candidate = false
			decision.Reason = "language_missing"
		case !languageAllowed(language, langs):
			decision.Candidate = false
			decision.Reason = "language_not_allowed"
		case cfg.Channels > 0 && channels != cfg.Channels:
			decision.Candidate = false
			decision.Reason = "channel_mismatch"
		case duration > 0 && primaryDuration > 0 && !durationWithinTolerance(duration, primaryDuration, cfg):
			decision.Candidate = false
			decision.Reason = "duration_mismatch"
		default:
			decision.Candidate = true
			decision.Reason = "candidate"
		}
		decisions = append(decisions, decision)
		if decision.Candidate {
			result = append(result, candidate{
				stream:           stream,
				language:         language,
				title:            title,
				channels:         channels,
				duration:         duration,
				metadataPositive: metaPos,
				metadataNegative: metaNeg,
			})
		}
	}
	return result, decisions
}

func classify(metrics Metrics, meta Metadata, cfg config.CommentaryDetection) (bool, string) {
	if meta.Negative != "" {
		return false, meta.Negative
	}
	if metrics.SpeechRatio <= cfg.SpeechRatioMaxMusic {
		return false, "music_or_silent"
	}

	// Duplicate detection for very high similarity
	if metrics.FingerprintSimilarity >= cfg.FingerprintSimilarityDuplicate &&
		math.Abs(metrics.SpeechRatio-metrics.PrimarySpeechRatio) <= minSpeechRatioDeltaPrimary {
		return false, "duplicate_downmix"
	}
	// Correlation-based downmix detection
	if metrics.FingerprintSimilarity >= downmixSimilarityMin &&
		metrics.SpeechTimingCorrelation >= downmixCorrelationMin {
		return false, "downmix_correlation"
	}
	adSimilarityMin := cfg.FingerprintSimilarityDuplicate - 0.10
	if adSimilarityMin < 0.70 {
		adSimilarityMin = 0.70
	}
	if adSimilarityMin > 0.95 {
		adSimilarityMin = 0.95
	}
	if metrics.FingerprintSimilarity >= adSimilarityMin &&
		metrics.SpeechInPrimarySilence >= cfg.SpeechInSilenceMax &&
		metrics.SpeechRatio >= cfg.SpeechRatioMinCommentary {
		return false, "audio_description"
	}
	if metrics.SpeechInPrimarySilence >= cfg.SpeechInSilenceMax &&
		metrics.SpeechOverlapWithPrimary <= cfg.SpeechOverlapPrimaryMaxAD &&
		metrics.SpeechRatio >= cfg.SpeechRatioMinCommentary {
		return false, "audio_description"
	}

	commentaryMax := cfg.FingerprintSimilarityDuplicate - 0.15
	if commentaryMax < 0.3 {
		commentaryMax = 0.3
	}
	if commentaryMax > 0.7 {
		commentaryMax = 0.7
	}

	if metrics.FingerprintSimilarity <= commentaryMax && metrics.SpeechRatio >= cfg.SpeechRatioMinCommentary {
		return true, "commentary_only"
	}
	if metrics.SpeechOverlapWithPrimary >= cfg.SpeechOverlapPrimaryMin &&
		metrics.FingerprintSimilarity < cfg.FingerprintSimilarityDuplicate &&
		metrics.FingerprintSimilarity < downmixSimilarityMin {
		return true, "mixed_commentary"
	}
	if meta.Positive && metrics.SpeechRatio >= cfg.SpeechRatioMinCommentary &&
		metrics.FingerprintSimilarity < cfg.FingerprintSimilarityDuplicate {
		return true, "metadata_commentary"
	}
	return false, "ambiguous"
}

func classifyMetadata(title string) (bool, string) {
	if title == "" {
		return false, ""
	}
	value := strings.ToLower(title)
	positive := []string{
		"commentary",
		"director commentary",
		"cast commentary",
		"commentary by",
		"screenwriter commentary",
	}
	negative := map[string]string{
		"audio description":       "audio_description",
		"descriptive":             "audio_description",
		"dvs":                     "audio_description",
		"visually impaired":       "audio_description",
		"narration for the blind": "audio_description",
		"isolated score":          "music_only",
		"music only":              "music_only",
		"score":                   "music_only",
		"soundtrack":              "music_only",
		"karaoke":                 "music_only",
		"sing-along":              "music_only",
	}
	for key, reason := range negative {
		if strings.Contains(value, key) {
			return false, reason
		}
	}
	for _, key := range positive {
		if strings.Contains(value, key) {
			return true, ""
		}
	}
	return false, ""
}

func normalizedLanguages(langs []string) []string {
	out := make([]string, 0, len(langs))
	seen := make(map[string]struct{}, len(langs))
	for _, lang := range langs {
		normalized := strings.ToLower(strings.TrimSpace(lang))
		if normalized == "" {
			continue
		}
		if _, ok := seen[normalized]; ok {
			continue
		}
		seen[normalized] = struct{}{}
		out = append(out, normalized)
	}
	if len(out) == 0 {
		out = []string{"en"}
	}
	return out
}

func languageAllowed(language string, allowed []string) bool {
	if language == "" {
		return false
	}
	lang := strings.ToLower(language)
	for _, allow := range allowed {
		if strings.HasPrefix(lang, allow) {
			return true
		}
	}
	return false
}

func normalizeLanguage(tags map[string]string) string {
	if len(tags) == 0 {
		return ""
	}
	for _, key := range []string{"language", "LANGUAGE", "Language", "language_ietf", "LANG"} {
		if value, ok := tags[key]; ok {
			return strings.ToLower(strings.TrimSpace(value))
		}
	}
	return ""
}

func normalizeTitle(tags map[string]string) string {
	if len(tags) == 0 {
		return ""
	}
	for _, key := range []string{"title", "TITLE", "handler_name", "HANDLER_NAME"} {
		if value, ok := tags[key]; ok {
			return strings.ToLower(strings.TrimSpace(value))
		}
	}
	return ""
}

func channelCount(stream ffprobe.Stream) int {
	if stream.Channels > 0 {
		return stream.Channels
	}
	layout := strings.ToLower(strings.TrimSpace(stream.ChannelLayout))
	if layout == "" {
		return 0
	}
	if strings.HasPrefix(layout, "2.0") {
		return 2
	}
	if strings.HasPrefix(layout, "2.1") {
		return 3
	}
	if strings.HasPrefix(layout, "1.0") {
		return 1
	}
	if strings.Contains(layout, ".") {
		parts := strings.Split(layout, ".")
		total := 0
		for _, part := range parts {
			part = strings.Trim(part, "abcdefghijklmnopqrstuvwxyz ()")
			if part == "" {
				continue
			}
			if n, err := strconv.Atoi(part); err == nil {
				total += n
			}
		}
		if total > 0 {
			return total
		}
	}
	return 0
}

func streamDurationSeconds(stream ffprobe.Stream) float64 {
	value := strings.TrimSpace(stream.Duration)
	if value == "" {
		return 0
	}
	if parsed, err := strconv.ParseFloat(value, 64); err == nil && parsed > 0 {
		return parsed
	}
	return 0
}

func durationWithinTolerance(candidate, primary float64, cfg config.CommentaryDetection) bool {
	if candidate <= 0 || primary <= 0 {
		return true
	}
	absTolerance := float64(cfg.DurationToleranceSeconds)
	ratioTolerance := cfg.DurationToleranceRatio * primary
	tolerance := math.Max(absTolerance, ratioTolerance)
	if tolerance <= 0 {
		tolerance = math.Max(120, primary*0.02)
	}
	diff := math.Abs(candidate - primary)
	return diff <= tolerance
}

type window struct {
	start    float64
	duration float64
}

func buildWindows(duration float64, count int, seconds int) []window {
	if count <= 0 {
		return nil
	}
	windowSeconds := float64(seconds)
	if windowSeconds <= 0 {
		windowSeconds = 90
	}
	if duration <= 0 {
		return []window{{start: 0, duration: windowSeconds}}
	}
	out := make([]window, 0, count)
	for i, ratio := range windowRatios(count) {
		if i >= count {
			break
		}
		start := ratio * duration
		if start < 0 {
			start = 0
		}
		if start+windowSeconds > duration {
			start = math.Max(0, duration-windowSeconds)
		}
		winDuration := windowSeconds
		if duration-start < winDuration {
			winDuration = duration - start
		}
		if winDuration <= 0 {
			continue
		}
		out = append(out, window{start: start, duration: winDuration})
	}
	return out
}

func windowRatios(count int) []float64 {
	if count <= 1 {
		return []float64{0.5}
	}
	if count == 3 {
		return []float64{0.1, 0.5, 0.9}
	}
	out := make([]float64, 0, count)
	for i := 0; i < count; i++ {
		out = append(out, float64(i+1)/float64(count+1))
	}
	return out
}

type speechAnalysis struct {
	totalFrames  int
	speechFrames int
	windows      [][]bool
}

func (s speechAnalysis) speechRatio() float64 {
	if s.totalFrames == 0 {
		return 0
	}
	return float64(s.speechFrames) / float64(s.totalFrames)
}

func analyzeSpeech(ctx context.Context, ffmpegBinary, path string, streamIndex int, windows []window) (speechAnalysis, error) {
	vad, err := newVAD()
	if err != nil {
		return speechAnalysis{}, err
	}
	out := speechAnalysis{windows: make([][]bool, 0, len(windows))}
	for _, win := range windows {
		data, err := extractPCM(ctx, ffmpegBinary, path, streamIndex, win)
		if err != nil {
			return speechAnalysis{}, err
		}
		flags, frames := vadFrames(vad, data, defaultSampleRate, defaultFrameMs)
		out.windows = append(out.windows, flags)
		out.totalFrames += frames
		for _, flag := range flags {
			if flag {
				out.speechFrames++
			}
		}
	}
	return out, nil
}

func vadFrames(vad *vadProcessor, data []byte, sampleRate, frameMs int) ([]bool, int) {
	if vad == nil || len(data) == 0 {
		return nil, 0
	}
	samplesPerFrame := sampleRate * frameMs / 1000
	frameBytes := samplesPerFrame * 2
	if frameBytes <= 0 {
		return nil, 0
	}
	flags := make([]bool, 0, len(data)/frameBytes)
	total := 0
	for offset := 0; offset+frameBytes <= len(data); offset += frameBytes {
		frame := data[offset : offset+frameBytes]
		isSpeech, err := vad.Process(sampleRate, frame)
		if err != nil {
			continue
		}
		flags = append(flags, isSpeech)
		total++
	}
	return flags, total
}

func speechOverlap(primary, candidate speechAnalysis) (float64, float64, float64) {
	totalSpeech := 0
	overlap := 0
	inSilence := 0

	// Collect all frames for correlation calculation
	var primFrames, candFrames []float64

	for i := 0; i < len(candidate.windows) && i < len(primary.windows); i++ {
		cand := candidate.windows[i]
		prim := primary.windows[i]
		frames := len(cand)
		if len(prim) < frames {
			frames = len(prim)
		}
		for j := 0; j < frames; j++ {
			// Collect for correlation (convert bool to float64)
			primVal := 0.0
			if prim[j] {
				primVal = 1.0
			}
			candVal := 0.0
			if cand[j] {
				candVal = 1.0
			}
			primFrames = append(primFrames, primVal)
			candFrames = append(candFrames, candVal)

			// Original overlap/silence calculation
			if !cand[j] {
				continue
			}
			totalSpeech++
			if prim[j] {
				overlap++
			} else {
				inSilence++
			}
		}
	}

	overlapRatio := 0.0
	inSilenceRatio := 0.0
	if totalSpeech > 0 {
		overlapRatio = float64(overlap) / float64(totalSpeech)
		inSilenceRatio = float64(inSilence) / float64(totalSpeech)
	}

	correlation := pearsonCorrelation(primFrames, candFrames)
	return overlapRatio, inSilenceRatio, correlation
}

// pearsonCorrelation computes the Pearson correlation coefficient between two series.
// Returns 0 if the series are empty or have zero variance.
func pearsonCorrelation(x, y []float64) float64 {
	n := len(x)
	if n == 0 || len(y) != n {
		return 0
	}

	// Compute means
	var sumX, sumY float64
	for i := 0; i < n; i++ {
		sumX += x[i]
		sumY += y[i]
	}
	meanX := sumX / float64(n)
	meanY := sumY / float64(n)

	// Compute correlation components
	var numerator, sumSqX, sumSqY float64
	for i := 0; i < n; i++ {
		dx := x[i] - meanX
		dy := y[i] - meanY
		numerator += dx * dy
		sumSqX += dx * dx
		sumSqY += dy * dy
	}

	denominator := math.Sqrt(sumSqX * sumSqY)
	if denominator == 0 {
		return 0
	}
	return numerator / denominator
}

func extractPCM(ctx context.Context, ffmpegBinary, path string, streamIndex int, win window) ([]byte, error) {
	if strings.TrimSpace(ffmpegBinary) == "" {
		ffmpegBinary = "ffmpeg"
	}
	args := []string{
		"-hide_banner", "-loglevel", "error",
		"-ss", fmt.Sprintf("%.3f", win.start),
		"-t", fmt.Sprintf("%.3f", win.duration),
		"-i", path,
		"-map", fmt.Sprintf("0:%d", streamIndex),
		"-ac", "1",
		"-ar", fmt.Sprintf("%d", defaultSampleRate),
		"-f", "s16le",
		"-",
	}
	cmd := exec.CommandContext(ctx, ffmpegBinary, args...) //nolint:gosec
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	var stdout bytes.Buffer
	cmd.Stdout = &stdout
	if err := cmd.Run(); err != nil {
		return nil, fmt.Errorf("ffmpeg pcm extract: %w: %s", err, strings.TrimSpace(stderr.String()))
	}
	return stdout.Bytes(), nil
}

func fingerprintForStream(ctx context.Context, ffmpegBinary, path string, streamIndex int, windows []window, outputPath string) ([]int, error) {
	if err := extractFingerprintAudio(ctx, ffmpegBinary, path, streamIndex, windows, outputPath); err != nil {
		return nil, err
	}
	return runFPcalc(ctx, outputPath)
}

type fingerprintFailure struct {
	Reason    string
	Cause     string
	Attention string
	Hint      string
}

func classifyFingerprintFailure(err error) fingerprintFailure {
	message := strings.ToLower(err.Error())
	switch {
	case strings.Contains(message, "stream specifier") || strings.Contains(message, "matches no streams") || strings.Contains(message, "no such stream"):
		return fingerprintFailure{
			Reason:    "fingerprint_failed_stream_missing",
			Cause:     "ffmpeg_stream_missing",
			Attention: "investigate_if_frequent",
			Hint:      "ffmpeg could not map the audio stream; candidate skipped",
		}
	case strings.Contains(message, "error while decoding") || strings.Contains(message, "invalid data found when processing input") || strings.Contains(message, "could not find codec parameters"):
		return fingerprintFailure{
			Reason:    "fingerprint_failed_decode",
			Cause:     "ffmpeg_decode_error",
			Attention: "investigate_if_frequent",
			Hint:      "ffmpeg could not decode the stream; candidate skipped",
		}
	case strings.Contains(message, "fpcalc"):
		return fingerprintFailure{
			Reason:    "fingerprint_failed_fpcalc",
			Cause:     "fpcalc_error",
			Attention: "investigate",
			Hint:      "fpcalc failed to produce a fingerprint; candidate skipped",
		}
	default:
		return fingerprintFailure{
			Reason:    "fingerprint_failed",
			Cause:     "unknown_error",
			Attention: "investigate_if_frequent",
			Hint:      "fingerprint extraction failed; candidate skipped",
		}
	}
}

func extractFingerprintAudio(ctx context.Context, ffmpegBinary, path string, streamIndex int, windows []window, outputPath string) error {
	if strings.TrimSpace(ffmpegBinary) == "" {
		ffmpegBinary = "ffmpeg"
	}
	filter, label := buildFilter(streamIndex, windows)
	args := []string{"-hide_banner", "-loglevel", "error", "-i", path}
	if filter != "" {
		args = append(args, "-filter_complex", filter, "-map", label)
	} else {
		args = append(args, "-map", fmt.Sprintf("0:%d", streamIndex))
	}
	args = append(args,
		"-ac", "1",
		"-ar", "11025",
		"-c:a", "pcm_s16le",
		outputPath,
	)
	cmd := exec.CommandContext(ctx, ffmpegBinary, args...) //nolint:gosec
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("ffmpeg fingerprint extract: %w: %s", err, strings.TrimSpace(stderr.String()))
	}
	return nil
}

func buildFilter(streamIndex int, windows []window) (string, string) {
	if len(windows) == 0 {
		return "", ""
	}
	if len(windows) == 1 {
		win := windows[0]
		return fmt.Sprintf("[0:%d]atrim=start=%.3f:duration=%.3f,asetpts=PTS-STARTPTS[aout]", streamIndex, win.start, win.duration), "[aout]"
	}
	parts := make([]string, 0, len(windows))
	labels := make([]string, 0, len(windows))
	for i, win := range windows {
		label := fmt.Sprintf("a%d", i)
		labels = append(labels, "["+label+"]")
		parts = append(parts, fmt.Sprintf("[0:%d]atrim=start=%.3f:duration=%.3f,asetpts=PTS-STARTPTS[%s]", streamIndex, win.start, win.duration, label))
	}
	concat := fmt.Sprintf("%sconcat=n=%d:v=0:a=1[aout]", strings.Join(labels, ""), len(windows))
	parts = append(parts, concat)
	return strings.Join(parts, ";"), "[aout]"
}

func runFPcalc(ctx context.Context, path string) ([]int, error) {
	cmd := exec.CommandContext(ctx, "fpcalc", "-raw", "-plain", path) //nolint:gosec
	output, err := cmd.Output()
	if err != nil {
		return nil, fmt.Errorf("fpcalc: %w", err)
	}
	lines := strings.Split(string(output), "\n")
	for _, line := range lines {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		line = strings.TrimPrefix(line, "FINGERPRINT=")
		values := parseFingerprint(line)
		if len(values) > 0 {
			return values, nil
		}
	}
	return nil, errors.New("fpcalc: fingerprint missing")
}

func parseFingerprint(value string) []int {
	parts := strings.Split(value, ",")
	out := make([]int, 0, len(parts))
	for _, part := range parts {
		part = strings.TrimSpace(part)
		if part == "" {
			continue
		}
		if v, err := strconv.Atoi(part); err == nil {
			out = append(out, v)
		}
	}
	return out
}

func compareFingerprints(primary, candidate []int) float64 {
	if len(primary) == 0 || len(candidate) == 0 {
		return 0
	}
	limit := len(primary)
	if len(candidate) < limit {
		limit = len(candidate)
	}
	var matches uint64
	for i := 0; i < limit; i++ {
		xor := uint32(primary[i]) ^ uint32(candidate[i])
		matches += uint64(32 - bits.OnesCount32(xor))
	}
	totalBits := uint64(limit) * 32
	if totalBits == 0 {
		return 0
	}
	return float64(matches) / float64(totalBits)
}

func tempWorkDir(path string) (string, func(), error) {
	base := filepath.Dir(path)
	dir, err := os.MkdirTemp(base, "spindle-commentary-")
	if err != nil {
		return "", func() {}, fmt.Errorf("commentary detect: temp dir: %w", err)
	}
	cleanup := func() {
		if os.Getenv("SPD_DEBUG_COMMENTARY_KEEP") == "" {
			_ = os.RemoveAll(dir)
		}
	}
	return dir, cleanup, nil
}

func findStream(streams []ffprobe.Stream, index int) (ffprobe.Stream, bool) {
	for _, stream := range streams {
		if stream.Index == index {
			return stream, true
		}
	}
	return ffprobe.Stream{}, false
}
