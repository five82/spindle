package subtitle

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"

	"github.com/five82/spindle/internal/transcription"
	"path/filepath"
	"strings"
	"unicode/utf8"

	"github.com/five82/spindle/internal/language"
)

var runStableTS = func(ctx context.Context, args []string) ([]byte, error) {
	cmd := exec.CommandContext(ctx, stableTSCommand, args...)
	// uvx grandchild processes must die with the group or they outlive
	// cancellation and block the pipe wait (see ConfigureGroupKill).
	transcription.ConfigureGroupKill(cmd)
	return cmd.CombinedOutput()
}

var stableTSSegmentFields = map[string]bool{
	"avg_logprob":       true,
	"compression_ratio": true,
	"end":               true,
	"id":                true,
	"no_speech_prob":    true,
	"seek":              true,
	"start":             true,
	"temperature":       true,
	"text":              true,
	"tokens":            true,
	"words":             true,
}

var stableTSWordFields = map[string]bool{
	"end":          true,
	"id":           true,
	"left_locked":  true,
	"probability":  true,
	"right_locked": true,
	"segment_id":   true,
	"start":        true,
	"tokens":       true,
	"word":         true,
}

type whisperXPayload struct {
	Language         string           `json:"language,omitempty"`
	DetectedLanguage string           `json:"detected_language,omitempty"`
	Segments         []map[string]any `json:"segments,omitempty"`
	SpeechSegments   []map[string]any `json:"speech_segments,omitempty"`
}

func formatSubtitleFromCanonical(ctx context.Context, canonical transcriptionArtifacts, workDir, displayPath string, videoSeconds float64, subtitleLanguage string) (FormatResult, error) {
	if err := os.MkdirAll(workDir, 0o755); err != nil {
		return FormatResult{}, fmt.Errorf("create subtitle work dir: %w", err)
	}
	filteredJSONPath := filepath.Join(workDir, "audio.filtered.json")
	stats, err := filterWhisperXJSON(canonical.JSONPath, filteredJSONPath, videoSeconds)
	if err != nil {
		return FormatResult{}, err
	}
	if err := runStableTSFormatter(ctx, filteredJSONPath, displayPath, subtitleLanguage); err != nil {
		return FormatResult{}, err
	}
	postStats, err := postProcessDisplaySRT(displayPath, videoSeconds)
	if err != nil {
		return FormatResult{}, err
	}
	return FormatResult{
		DisplayPath:                displayPath,
		OriginalSegments:           stats.OriginalSegments,
		FilteredSegments:           stats.FilteredSegments,
		RemovedByTextRules:         stats.RemovedByTextRules,
		RemovedBySegmentHeuristics: stats.RemovedBySegmentHeuristics,
		SplitCues:                  postStats.SplitCues,
		MergedCues:                 postStats.MergedCues,
		WrappedCues:                postStats.WrappedCues,
		RetimedCues:                postStats.RetimedCues,
	}, nil
}

type transcriptionArtifacts struct {
	JSONPath string
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
	DisplayPath                string
	OriginalSegments           int
	FilteredSegments           int
	RemovedByTextRules         int
	RemovedBySegmentHeuristics int
	SplitCues                  int
	MergedCues                 int
	WrappedCues                int
	RetimedCues                int
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
	formatterSegments := stableTSSegments(filtered)
	switch field {
	case "segments":
		payload.Segments = formatterSegments
	case "speech_segments":
		payload.SpeechSegments = formatterSegments
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

func stableTSSegments(segments []map[string]any) []map[string]any {
	cleaned := make([]map[string]any, 0, len(segments))
	for _, segment := range segments {
		cleanSegment := make(map[string]any, len(segment))
		for key, value := range segment {
			if !stableTSSegmentFields[key] {
				continue
			}
			if key == "words" {
				cleanSegment[key] = stableTSWords(value)
				continue
			}
			cleanSegment[key] = value
		}
		cleaned = append(cleaned, cleanSegment)
	}
	return cleaned
}

func stableTSWords(value any) []map[string]any {
	var words []any
	switch typed := value.(type) {
	case []any:
		words = typed
	case []map[string]any:
		words = make([]any, 0, len(typed))
		for _, word := range typed {
			words = append(words, word)
		}
	default:
		return nil
	}
	cleaned := make([]map[string]any, 0, len(words))
	for _, entry := range words {
		word, ok := entry.(map[string]any)
		if !ok {
			continue
		}
		cleanWord := make(map[string]any, len(word))
		for key, value := range word {
			if key == "score" {
				if _, hasProbability := word["probability"]; !hasProbability {
					cleanWord["probability"] = value
				}
				continue
			}
			if stableTSWordFields[key] {
				cleanWord[key] = value
			}
		}
		cleaned = append(cleaned, cleanWord)
	}
	return cleaned
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
