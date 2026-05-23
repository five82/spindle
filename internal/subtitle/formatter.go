package subtitle

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"unicode/utf8"

	"github.com/five82/spindle/internal/language"
)

var runStableTS = func(ctx context.Context, args []string) ([]byte, error) {
	cmd := exec.CommandContext(ctx, stableTSCommand, args...)
	return cmd.CombinedOutput()
}

type whisperXPayload struct {
	Language         string           `json:"language,omitempty"`
	DetectedLanguage string           `json:"detected_language,omitempty"`
	Segments         []map[string]any `json:"segments,omitempty"`
	SpeechSegments   []map[string]any `json:"speech_segments,omitempty"`
}

func formatSubtitleFromCanonical(ctx context.Context, canonical transcriptionArtifacts, workDir, displayPath string, videoSeconds float64, subtitleLanguage string) (formatResult, error) {
	if err := os.MkdirAll(workDir, 0o755); err != nil {
		return formatResult{}, fmt.Errorf("create subtitle work dir: %w", err)
	}
	filteredJSONPath := filepath.Join(workDir, "audio.filtered.json")
	stats, err := filterWhisperXJSON(canonical.JSONPath, filteredJSONPath, videoSeconds)
	if err != nil {
		return formatResult{}, err
	}
	if err := runStableTSFormatter(ctx, filteredJSONPath, displayPath, subtitleLanguage); err != nil {
		return formatResult{}, err
	}
	postStats, err := postProcessDisplaySRT(displayPath, videoSeconds)
	if err != nil {
		return formatResult{}, err
	}
	return formatResult{
		DisplayPath:                displayPath,
		OriginalSegments:           stats.OriginalSegments,
		FilteredSegments:           stats.FilteredSegments,
		RemovedByTextRules:         stats.RemovedByTextRules,
		RemovedBySegmentHeuristics: stats.RemovedBySegmentHeuristics,
		SplitCues:                  postStats.SplitCues,
		WrappedCues:                postStats.WrappedCues,
		RetimedCues:                postStats.RetimedCues,
		FormatterDecision:          "formatted",
	}, nil
}

func formatForcedSubtitleFromCanonical(ctx context.Context, canonical transcriptionArtifacts, workDir, forcedPath string, videoSeconds float64, subtitleLanguage string) (forcedFormatResult, error) {
	if err := os.MkdirAll(workDir, 0o755); err != nil {
		return forcedFormatResult{}, fmt.Errorf("create subtitle work dir: %w", err)
	}
	payload, field, segments, err := loadWhisperXPayload(canonical.JSONPath)
	if err != nil {
		return forcedFormatResult{}, err
	}
	filtered, _, err := filterWhisperXSegments(segments, videoSeconds)
	if err != nil {
		return forcedFormatResult{}, err
	}
	forcedSegments := make([]map[string]any, 0)
	languageSet := make(map[string]bool)
	for _, segment := range filtered {
		if !isForeignTranslatedSegment(segment) {
			continue
		}
		forcedSegments = append(forcedSegments, segment)
		if lang, _ := segment["source_language"].(string); lang != "" {
			lang = strings.ToLower(strings.TrimSpace(lang))
			if lang != "" {
				languageSet[lang] = true
			}
		}
	}
	if len(forcedSegments) == 0 {
		return forcedFormatResult{Decision: "none_detected"}, nil
	}
	switch field {
	case "segments":
		payload.Segments = forcedSegments
		payload.SpeechSegments = nil
	case "speech_segments":
		payload.SpeechSegments = forcedSegments
		payload.Segments = nil
	default:
		return forcedFormatResult{}, fmt.Errorf("unsupported whisperx segment field %q", field)
	}
	forcedJSONPath := filepath.Join(workDir, "audio.forced.json")
	data, err := json.Marshal(payload)
	if err != nil {
		return forcedFormatResult{}, fmt.Errorf("marshal forced whisperx payload: %w", err)
	}
	if err := os.WriteFile(forcedJSONPath, data, 0o644); err != nil {
		return forcedFormatResult{}, fmt.Errorf("write forced whisperx payload: %w", err)
	}
	if err := runStableTSFormatter(ctx, forcedJSONPath, forcedPath, subtitleLanguage); err != nil {
		return forcedFormatResult{}, err
	}
	postStats, err := postProcessDisplaySRT(forcedPath, videoSeconds)
	if err != nil {
		return forcedFormatResult{}, err
	}
	return forcedFormatResult{
		Path:      forcedPath,
		Segments:  len(forcedSegments),
		Languages: sortedStringSet(languageSet),
		SplitCues: postStats.SplitCues,
		Wrapped:   postStats.WrappedCues,
		Retimed:   postStats.RetimedCues,
		Decision:  "generated",
	}, nil
}

type transcriptionArtifacts struct {
	JSONPath string
}

type formatResult struct {
	DisplayPath                string
	OriginalSegments           int
	FilteredSegments           int
	RemovedByTextRules         int
	RemovedBySegmentHeuristics int
	SplitCues                  int
	WrappedCues                int
	RetimedCues                int
	FormatterDecision          string
}

type forcedFormatResult struct {
	Path      string
	Segments  int
	Languages []string
	SplitCues int
	Wrapped   int
	Retimed   int
	Decision  string
}

// filterStats summarizes derived-subtitle filtering decisions.
type filterStats struct {
	OriginalSegments           int
	FilteredSegments           int
	RemovedByTextRules         int
	RemovedBySegmentHeuristics int
}

// FormatResult summarizes display-subtitle formatting output.
type FormatResult struct {
	DisplayPath      string
	OriginalSegments int
	FilteredSegments int
	SplitCues        int
	WrappedCues      int
	RetimedCues      int
}

// DisplaySubtitlePath returns the standard sidecar subtitle path for a video.
func DisplaySubtitlePath(videoPath, subtitleLanguage string) string {
	return displaySubtitlePath(videoPath, subtitleLanguage)
}

// ForcedSubtitlePath returns the standard forced sidecar subtitle path for a video.
func ForcedSubtitlePath(videoPath, subtitleLanguage string) string {
	return displayForcedSubtitlePath(videoPath, subtitleLanguage)
}

func runStableTSFormatter(ctx context.Context, jsonPath, outputPath, subtitleLanguage string) error {
	if strings.TrimSpace(jsonPath) == "" {
		return fmt.Errorf("stable-ts formatter missing json path")
	}
	if strings.TrimSpace(outputPath) == "" {
		return fmt.Errorf("stable-ts formatter missing output path")
	}
	if err := os.MkdirAll(filepath.Dir(outputPath), 0o755); err != nil {
		return fmt.Errorf("create subtitle output dir: %w", err)
	}
	tmpPath := outputPath + ".tmp"
	defer func() { _ = os.Remove(tmpPath) }()

	lang := language.ToISO2(subtitleLanguage)
	if lang == "" {
		lang = "en"
	}
	args := []string{
		"--from", stableTSPackage,
		"python", "-c", stableTSFormatterScript,
		jsonPath,
		tmpPath,
		"--language", lang,
	}
	output, err := runStableTS(ctx, args)
	if err != nil {
		return fmt.Errorf("stable-ts formatter: %w: %s", err, strings.TrimSpace(string(output)))
	}
	if err := os.Rename(tmpPath, outputPath); err != nil {
		return fmt.Errorf("finalize formatted subtitles: %w", err)
	}
	return nil
}

func filterWhisperXJSON(srcPath, destPath string, videoSeconds float64) (filterStats, error) {
	payload, field, segments, err := loadWhisperXPayload(srcPath)
	if err != nil {
		return filterStats{}, err
	}
	filtered, stats, err := filterWhisperXSegments(segments, videoSeconds)
	if err != nil {
		return filterStats{}, err
	}
	switch field {
	case "segments":
		payload.Segments = filtered
	case "speech_segments":
		payload.SpeechSegments = filtered
	default:
		return filterStats{}, fmt.Errorf("unsupported whisperx segment field %q", field)
	}
	data, err := json.Marshal(payload)
	if err != nil {
		return filterStats{}, fmt.Errorf("marshal filtered whisperx payload: %w", err)
	}
	if err := os.WriteFile(destPath, data, 0o644); err != nil {
		return filterStats{}, fmt.Errorf("write filtered whisperx payload: %w", err)
	}
	return stats, nil
}

func loadWhisperXPayload(path string) (payload whisperXPayload, field string, segments []map[string]any, err error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return payload, "", nil, fmt.Errorf("read whisperx json: %w", err)
	}
	if err := json.Unmarshal(data, &payload); err != nil {
		return payload, "", nil, fmt.Errorf("parse whisperx json: %w", err)
	}
	switch {
	case len(payload.Segments) > 0:
		return payload, "segments", payload.Segments, nil
	case len(payload.SpeechSegments) > 0:
		return payload, "speech_segments", payload.SpeechSegments, nil
	default:
		return payload, "", nil, fmt.Errorf("whisperx json contained no segments")
	}
}

func filterWhisperXSegments(segments []map[string]any, videoSeconds float64) ([]map[string]any, filterStats, error) {
	indexed := make([]indexedTimedCue, 0, len(segments))
	for i, segment := range segments {
		cue, err := cueFromSegment(i, segment)
		if err != nil {
			return nil, filterStats{}, err
		}
		indexed = append(indexed, cue)
	}
	filtered, err := filterIndexedHallucinations(indexed, videoSeconds)
	if err != nil {
		return nil, filterStats{}, err
	}
	stats := filterStats{
		OriginalSegments:   len(segments),
		RemovedByTextRules: len(segments) - len(filtered),
	}
	result := make([]map[string]any, 0, len(filtered))
	for _, cue := range filtered {
		segment := segments[cue.Orig]
		if shouldDropSegmentByHeuristic(cue, segment) {
			stats.RemovedBySegmentHeuristics++
			continue
		}
		result = append(result, segment)
	}
	if len(result) == 0 {
		return nil, filterStats{}, fmt.Errorf("all cues removed by hallucination filter")
	}
	stats.FilteredSegments = len(result)
	return result, stats, nil
}

func isForeignTranslatedSegment(segment map[string]any) bool {
	foreign, _ := segment["foreign"].(bool)
	if foreign {
		return true
	}
	task, _ := segment["task"].(string)
	if !strings.EqualFold(strings.TrimSpace(task), "translate") {
		return false
	}
	sourceLanguage, _ := segment["source_language"].(string)
	sourceLanguage = strings.ToLower(strings.TrimSpace(strings.SplitN(sourceLanguage, "-", 2)[0]))
	return sourceLanguage != "" && sourceLanguage != "en"
}

func sortedStringSet(set map[string]bool) []string {
	values := make([]string, 0, len(set))
	for value := range set {
		values = append(values, value)
	}
	for i := 1; i < len(values); i++ {
		for j := i; j > 0 && values[j] < values[j-1]; j-- {
			values[j], values[j-1] = values[j-1], values[j]
		}
	}
	return values
}

func cueFromSegment(index int, segment map[string]any) (indexedTimedCue, error) {
	start, ok := floatValue(segment["start"])
	if !ok {
		return indexedTimedCue{}, fmt.Errorf("whisperx segment %d missing valid start", index)
	}
	end, ok := floatValue(segment["end"])
	if !ok {
		return indexedTimedCue{}, fmt.Errorf("whisperx segment %d missing valid end", index)
	}
	text, _ := segment["text"].(string)
	return indexedTimedCue{Orig: index, Start: start, End: end, Text: text}, nil
}

type segmentMetrics struct {
	LexicalWords   int
	AvgProbability float64
	HasProbability bool
	TextRunes      int
}

func shouldDropSegmentByHeuristic(cue indexedTimedCue, segment map[string]any) bool {
	duration := cue.End - cue.Start
	metrics := computeSegmentMetrics(cue.Text, segment)
	if isLowInformationLongCueMetrics(duration, metrics.LexicalWords, metrics.TextRunes) {
		return true
	}
	if metrics.HasProbability {
		if duration >= 5 && metrics.LexicalWords <= 3 && metrics.AvgProbability < 0.35 {
			return true
		}
		if duration >= 3.5 && metrics.LexicalWords <= 1 && metrics.AvgProbability < 0.45 && metrics.TextRunes <= 20 {
			return true
		}
	}
	return false
}

func computeSegmentMetrics(text string, segment map[string]any) segmentMetrics {
	metrics := segmentMetrics{TextRunes: utf8.RuneCountInString(strings.TrimSpace(text))}
	tokens := lexicalTokens(text)
	metrics.LexicalWords = len(tokens)
	words, ok := segment["words"].([]any)
	if !ok || len(words) == 0 {
		return metrics
	}
	var (
		tokenCount int
		sumProb    float64
		probCount  int
	)
	for _, entry := range words {
		word, ok := entry.(map[string]any)
		if !ok {
			continue
		}
		token, _ := word["word"].(string)
		tokenCount += len(lexicalTokens(token))
		if prob, ok := segmentProbability(word); ok {
			sumProb += prob
			probCount++
		}
	}
	if tokenCount > metrics.LexicalWords {
		metrics.LexicalWords = tokenCount
	}
	if probCount > 0 {
		metrics.HasProbability = true
		metrics.AvgProbability = sumProb / float64(probCount)
	}
	return metrics
}

func segmentProbability(word map[string]any) (float64, bool) {
	if prob, ok := floatValue(word["probability"]); ok {
		return prob, true
	}
	if prob, ok := floatValue(word["score"]); ok {
		return prob, true
	}
	return 0, false
}

func floatValue(v any) (float64, bool) {
	switch n := v.(type) {
	case float64:
		return n, true
	case float32:
		return float64(n), true
	case int:
		return float64(n), true
	case int64:
		return float64(n), true
	case json.Number:
		f, err := n.Float64()
		return f, err == nil
	default:
		return 0, false
	}
}

func displaySubtitlePath(videoPath, subtitleLanguage string) string {
	base := strings.TrimSuffix(videoPath, filepath.Ext(videoPath))
	lang := language.ToISO2(subtitleLanguage)
	if lang == "" {
		lang = "en"
	}
	return base + "." + lang + ".srt"
}

func displayForcedSubtitlePath(videoPath, subtitleLanguage string) string {
	base := strings.TrimSuffix(videoPath, filepath.Ext(videoPath))
	lang := language.ToISO2(subtitleLanguage)
	if lang == "" {
		lang = "en"
	}
	return base + "." + lang + ".forced.srt"
}
