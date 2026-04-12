package subtitle

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

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
	originalSegments, filteredSegments, err := filterWhisperXJSON(canonical.JSONPath, filteredJSONPath, videoSeconds)
	if err != nil {
		return formatResult{}, err
	}
	if err := runStableTSFormatter(ctx, filteredJSONPath, displayPath, subtitleLanguage); err != nil {
		return formatResult{}, err
	}
	return formatResult{
		DisplayPath:       displayPath,
		OriginalSegments:  originalSegments,
		FilteredSegments:  filteredSegments,
		FormatterDecision: "formatted",
	}, nil
}

type transcriptionArtifacts struct {
	JSONPath string
}

type formatResult struct {
	DisplayPath       string
	OriginalSegments  int
	FilteredSegments  int
	FormatterDecision string
}

// FormatRequest describes how to build a display subtitle from canonical
// WhisperX artifacts.
type FormatRequest struct {
	CanonicalJSONPath string
	WorkDir           string
	DisplayPath       string
	VideoSeconds      float64
	Language          string
}

// FormatResult summarizes display-subtitle formatting output.
type FormatResult struct {
	DisplayPath      string
	OriginalSegments int
	FilteredSegments int
}

// FormatDisplaySubtitle derives a display subtitle from canonical WhisperX
// artifacts using subtitle-package filtering and Stable-TS formatting.
func FormatDisplaySubtitle(ctx context.Context, req FormatRequest) (*FormatResult, error) {
	result, err := formatSubtitleFromCanonical(ctx, transcriptionArtifacts{JSONPath: req.CanonicalJSONPath}, req.WorkDir, req.DisplayPath, req.VideoSeconds, req.Language)
	if err != nil {
		return nil, err
	}
	return &FormatResult{
		DisplayPath:      result.DisplayPath,
		OriginalSegments: result.OriginalSegments,
		FilteredSegments: result.FilteredSegments,
	}, nil
}

// DisplaySubtitlePath returns the standard sidecar subtitle path for a video.
func DisplaySubtitlePath(videoPath, subtitleLanguage string) string {
	return displaySubtitlePath(videoPath, subtitleLanguage)
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

func filterWhisperXJSON(srcPath, destPath string, videoSeconds float64) (originalSegments, filteredSegments int, err error) {
	payload, field, segments, err := loadWhisperXPayload(srcPath)
	if err != nil {
		return 0, 0, err
	}
	originalSegments = len(segments)
	filtered, err := filterWhisperXSegments(segments, videoSeconds)
	if err != nil {
		return 0, 0, err
	}
	filteredSegments = len(filtered)
	switch field {
	case "segments":
		payload.Segments = filtered
	case "speech_segments":
		payload.SpeechSegments = filtered
	default:
		return 0, 0, fmt.Errorf("unsupported whisperx segment field %q", field)
	}
	data, err := json.Marshal(payload)
	if err != nil {
		return 0, 0, fmt.Errorf("marshal filtered whisperx payload: %w", err)
	}
	if err := os.WriteFile(destPath, data, 0o644); err != nil {
		return 0, 0, fmt.Errorf("write filtered whisperx payload: %w", err)
	}
	return originalSegments, filteredSegments, nil
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

func filterWhisperXSegments(segments []map[string]any, videoSeconds float64) ([]map[string]any, error) {
	indexed := make([]indexedTimedCue, 0, len(segments))
	for i, segment := range segments {
		cue, err := cueFromSegment(i, segment)
		if err != nil {
			return nil, err
		}
		indexed = append(indexed, cue)
	}
	filtered, err := filterIndexedHallucinations(indexed, videoSeconds)
	if err != nil {
		return nil, err
	}
	result := make([]map[string]any, 0, len(filtered))
	for _, cue := range filtered {
		result = append(result, segments[cue.Orig])
	}
	return result, nil
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
