package commentaryid

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"path/filepath"
	"strings"

	"spindle/internal/config"
	"spindle/internal/logging"
	"spindle/internal/media/audio"
	"spindle/internal/media/ffprobe"
	"spindle/internal/services"
	"spindle/internal/services/presetllm"
	"spindle/internal/subtitles"
)

const llmSystemPrompt = `You are an audio track classifier for ripped discs.

You will be given a primary audio transcript excerpt plus candidate track transcript excerpts.
Classify each candidate into exactly one kind:
- "commentary": people discussing the production, scene, actors, director, etc.
- "audio_description": narration describing on-screen visuals for blind/low-vision viewers
- "music_only": mostly music/effects with little or no spoken content
- "same_as_primary": essentially the same dialogue as the primary program track
- "unknown": cannot determine confidently

Respond with JSON only in the following schema:
{"tracks":[{"index":<int>,"kind":<string>,"confidence":<number 0..1>,"reason":<string>}]}`

type Detector struct {
	cfg        *config.Config
	logger     *slog.Logger
	probe      func(ctx context.Context, binary, path string) (ffprobe.Result, error)
	transcribe snippetTranscriber
	llm        *presetllm.Client
}

type snippetTranscriber interface {
	TranscribeSnippetPlainText(ctx context.Context, req subtitles.SnippetRequest) (subtitles.SnippetResult, error)
}

func New(cfg *config.Config, logger *slog.Logger) *Detector {
	d := &Detector{
		cfg:    cfg,
		logger: logging.NewComponentLogger(logger, "commentaryid"),
		probe:  ffprobe.Inspect,
	}
	if cfg == nil || !cfg.CommentaryDetectionEnabled {
		return d
	}
	d.transcribe = subtitles.NewService(cfg, logger)
	if strings.TrimSpace(cfg.CommentaryDetectionAPIKey) != "" {
		d.llm = presetllm.NewClient(presetllm.Config{
			APIKey:  cfg.CommentaryDetectionAPIKey,
			BaseURL: cfg.CommentaryDetectionBaseURL,
			Model:   cfg.CommentaryDetectionModel,
			Referer: cfg.CommentaryDetectionReferer,
			Title:   cfg.CommentaryDetectionTitle,
		})
	}
	return d
}

func (d *Detector) SetLogger(logger *slog.Logger) {
	if d == nil {
		return
	}
	d.logger = logging.NewComponentLogger(logger, "commentaryid")
	if svc, ok := d.transcribe.(*subtitles.Service); ok {
		svc.SetLogger(logger)
	}
}

type candidate struct {
	stream ffprobe.Stream
	title  string
	lang   string
}

func (d *Detector) Refine(ctx context.Context, sourcePath string, workDir string) (Refinement, error) {
	var empty Refinement
	if d == nil || d.cfg == nil || !d.cfg.CommentaryDetectionEnabled {
		return empty, nil
	}
	if d.transcribe == nil {
		return empty, services.Wrap(services.ErrConfiguration, "commentaryid", "init", "WhisperX transcription service unavailable", nil)
	}
	sourcePath = strings.TrimSpace(sourcePath)
	if sourcePath == "" {
		return empty, services.Wrap(services.ErrValidation, "commentaryid", "validate input", "Source path is empty", nil)
	}
	ffprobeBinary := "ffprobe"
	if d.cfg != nil {
		ffprobeBinary = d.cfg.FFprobeBinary()
	}
	probe, err := d.probe(ctx, ffprobeBinary, sourcePath)
	if err != nil {
		return empty, services.Wrap(services.ErrExternalTool, "commentaryid", "ffprobe", "Failed to inspect media with ffprobe", err)
	}
	if probe.AudioStreamCount() <= 1 {
		return Refinement{PrimaryIndex: -1}, nil
	}

	selection := audio.Select(probe.Streams)
	if selection.PrimaryIndex < 0 {
		return empty, services.Wrap(services.ErrValidation, "commentaryid", "select audio", "No primary audio stream available", nil)
	}

	candidates := englishStereoCandidates(probe.Streams, selection.PrimaryIndex)
	if len(candidates) == 0 {
		keep := keepList(selection.PrimaryIndex, nil)
		return Refinement{PrimaryIndex: selection.PrimaryIndex, KeepIndices: keep}, nil
	}

	windows := selectWindows(probe.DurationSeconds())
	if len(windows) == 0 {
		keep := keepList(selection.PrimaryIndex, nil)
		return Refinement{PrimaryIndex: selection.PrimaryIndex, KeepIndices: keep}, nil
	}

	workDir = strings.TrimSpace(workDir)
	if workDir == "" {
		workDir = filepath.Join(filepath.Dir(sourcePath), "commentary")
	}

	primaryText, perWindow, err := d.transcribePrimary(ctx, sourcePath, selection.PrimaryIndex, windows, workDir)
	if err != nil {
		return empty, err
	}
	if strings.TrimSpace(primaryText) == "" {
		keep := keepList(selection.PrimaryIndex, candidateIndices(candidates))
		return Refinement{PrimaryIndex: selection.PrimaryIndex, KeepIndices: keep}, nil
	}

	decisions := make(map[int]TrackDecision, len(candidates))
	remaining := make([]candidate, 0, len(candidates))
	candidateText := make(map[int]string, len(candidates))

	for _, cand := range candidates {
		text, windowsOut, err := d.transcribeCandidate(ctx, sourcePath, cand.stream.Index, windows, workDir)
		if err != nil {
			if d.logger != nil {
				d.logger.Warn("commentary snippet transcription failed; keeping candidate",
					logging.Int("audio_index", cand.stream.Index),
					logging.Error(err),
				)
			}
			decisions[cand.stream.Index] = TrackDecision{
				Index:      cand.stream.Index,
				Kind:       TrackKindUnknown,
				Confidence: 0,
				Reason:     "transcription failed",
			}
			continue
		}
		candidateText[cand.stream.Index] = text

		sims := make([]similarityWindow, 0, len(windows))
		for _, w := range windowsOut {
			primary := perWindow[w.startKey]
			sims = append(sims, compareWindow(primary, w.text))
		}
		summary := summarizeSimilarity(sims)
		if isSameAsPrimary(sims) {
			decisions[cand.stream.Index] = TrackDecision{
				Index:      cand.stream.Index,
				Kind:       TrackKindSameAsPrimary,
				Confidence: clamp01((summary.cosineMedian + summary.purityMedian + summary.coverageMedian) / 3),
				Reason:     fmt.Sprintf("high transcript similarity (cos=%.2f purity=%.2f coverage=%.2f)", summary.cosineMedian, summary.purityMedian, summary.coverageMedian),
			}
			continue
		}
		if likelyMusicOnly(text) {
			decisions[cand.stream.Index] = TrackDecision{
				Index:      cand.stream.Index,
				Kind:       TrackKindMusicOnly,
				Confidence: 0.8,
				Reason:     "too little transcribed speech across sampled windows",
			}
			continue
		}

		remaining = append(remaining, cand)
		decisions[cand.stream.Index] = TrackDecision{
			Index:      cand.stream.Index,
			Kind:       TrackKindUnknown,
			Confidence: 0,
			Reason:     fmt.Sprintf("needs classification (cos=%.2f)", summary.cosineMedian),
		}
	}

	llmDecisions := map[int]TrackDecision{}
	if len(remaining) > 0 && d.llm != nil {
		resp, err := d.classifyWithLLM(ctx, sourcePath, selection.PrimaryIndex, primaryText, remaining, candidateText)
		if err != nil {
			if d.logger != nil {
				d.logger.Warn("commentary LLM classification failed; keeping unknown candidates", logging.Error(err))
			}
		} else {
			for _, track := range resp {
				llmDecisions[track.Index] = track
			}
		}
	}

	kept := make([]TrackDecision, 0, len(candidates)+1)
	dropped := make([]TrackDecision, 0, len(candidates))

	keepCommentary := make([]int, 0, len(candidates))
	for _, cand := range candidates {
		decision := decisions[cand.stream.Index]
		if llmDecision, ok := llmDecisions[cand.stream.Index]; ok {
			decision = llmDecision
		}
		switch decision.Kind {
		case TrackKindSameAsPrimary, TrackKindMusicOnly, TrackKindAudioDescription:
			dropped = append(dropped, decision)
		case TrackKindCommentary, TrackKindUnknown:
			keepCommentary = append(keepCommentary, cand.stream.Index)
			kept = append(kept, decision)
		default:
			keepCommentary = append(keepCommentary, cand.stream.Index)
			decision.Kind = TrackKindUnknown
			kept = append(kept, decision)
		}
	}

	keep := keepList(selection.PrimaryIndex, keepCommentary)
	return Refinement{
		PrimaryIndex: selection.PrimaryIndex,
		KeepIndices:  keep,
		Dropped:      dropped,
		Kept:         kept,
	}, nil
}

type window struct {
	start    float64
	duration float64
	startKey string
}

func selectWindows(totalSeconds float64) []window {
	if totalSeconds <= 0 {
		return nil
	}
	snippet := 75.0
	if totalSeconds < 8*60 {
		snippet = 45
	}
	if totalSeconds < snippet+30 {
		return []window{{start: 0, duration: totalSeconds, startKey: "0"}}
	}
	clamp := func(start float64) float64 {
		maxStart := totalSeconds - snippet - 5
		if maxStart < 0 {
			maxStart = 0
		}
		if start < 0 {
			return 0
		}
		if start > maxStart {
			return maxStart
		}
		return start
	}
	points := []float64{
		clamp(60),
		clamp(totalSeconds * 0.33),
		clamp(totalSeconds * 0.66),
	}
	uniq := make([]window, 0, 3)
	seen := map[int64]struct{}{}
	for _, start := range points {
		key := int64(start * 1000)
		if _, ok := seen[key]; ok {
			continue
		}
		seen[key] = struct{}{}
		uniq = append(uniq, window{start: start, duration: snippet, startKey: fmt.Sprintf("%d", key)})
	}
	return uniq
}

type windowTranscript struct {
	startKey string
	text     string
}

func (d *Detector) transcribePrimary(ctx context.Context, sourcePath string, audioIndex int, windows []window, workDir string) (string, map[string]string, error) {
	perWindow := make(map[string]string, len(windows))
	var combined strings.Builder
	for _, w := range windows {
		res, err := d.transcribe.TranscribeSnippetPlainText(ctx, subtitles.SnippetRequest{
			SourcePath:                sourcePath,
			AudioIndex:                audioIndex,
			StartSeconds:              w.start,
			DurationSeconds:           w.duration,
			Language:                  "en",
			WorkDir:                   filepath.Join(workDir, "primary"),
			AllowTranscriptCacheRead:  true,
			AllowTranscriptCacheWrite: true,
		})
		if err != nil {
			return "", nil, services.Wrap(services.ErrExternalTool, "commentaryid", "transcribe primary", "Failed to transcribe primary audio snippet with WhisperX", err)
		}
		text := strings.TrimSpace(res.PlainText)
		perWindow[w.startKey] = text
		if text != "" {
			combined.WriteString(text)
			combined.WriteString("\n")
		}
	}
	return strings.TrimSpace(combined.String()), perWindow, nil
}

func (d *Detector) transcribeCandidate(ctx context.Context, sourcePath string, audioIndex int, windows []window, workDir string) (string, []windowTranscript, error) {
	var combined strings.Builder
	out := make([]windowTranscript, 0, len(windows))
	for _, w := range windows {
		res, err := d.transcribe.TranscribeSnippetPlainText(ctx, subtitles.SnippetRequest{
			SourcePath:                sourcePath,
			AudioIndex:                audioIndex,
			StartSeconds:              w.start,
			DurationSeconds:           w.duration,
			Language:                  "en",
			WorkDir:                   filepath.Join(workDir, fmt.Sprintf("a%d", audioIndex)),
			AllowTranscriptCacheRead:  true,
			AllowTranscriptCacheWrite: true,
		})
		if err != nil {
			return "", nil, err
		}
		text := strings.TrimSpace(res.PlainText)
		out = append(out, windowTranscript{startKey: w.startKey, text: text})
		if text != "" {
			combined.WriteString(text)
			combined.WriteString("\n")
		}
	}
	return strings.TrimSpace(combined.String()), out, nil
}

func (d *Detector) classifyWithLLM(ctx context.Context, sourcePath string, primaryIndex int, primaryText string, candidates []candidate, candidateText map[int]string) ([]TrackDecision, error) {
	payload := map[string]any{
		"source":  filepath.Base(sourcePath),
		"primary": map[string]any{"index": primaryIndex, "sample": truncate(primaryText, 1200)},
		"tracks":  make([]map[string]any, 0, len(candidates)),
	}

	for _, cand := range candidates {
		text := candidateText[cand.stream.Index]
		entry := map[string]any{
			"index":    cand.stream.Index,
			"channels": cand.stream.Channels,
			"language": cand.lang,
			"title":    cand.title,
			"sample":   truncate(text, 1200),
		}
		payload["tracks"] = append(payload["tracks"].([]map[string]any), entry)
	}

	if len(payload["tracks"].([]map[string]any)) == 0 {
		return nil, nil
	}

	encoded, _ := json.Marshal(payload)
	content, err := d.llm.CompleteJSON(ctx, llmSystemPrompt, string(encoded))
	if err != nil {
		return nil, err
	}
	var parsed struct {
		Tracks []TrackDecision `json:"tracks"`
	}
	if err := json.Unmarshal([]byte(content), &parsed); err != nil {
		return nil, fmt.Errorf("parse LLM response: %w", err)
	}
	out := make([]TrackDecision, 0, len(parsed.Tracks))
	for _, decision := range parsed.Tracks {
		decision.Kind = normalizeKind(decision.Kind)
		decision.Confidence = clamp01(decision.Confidence)
		decision.Reason = strings.TrimSpace(decision.Reason)
		if decision.Index < 0 {
			continue
		}
		out = append(out, decision)
	}
	return out, nil
}

func normalizeKind(kind TrackKind) TrackKind {
	switch strings.ToLower(strings.TrimSpace(string(kind))) {
	case string(TrackKindSameAsPrimary):
		return TrackKindSameAsPrimary
	case string(TrackKindCommentary):
		return TrackKindCommentary
	case string(TrackKindAudioDescription):
		return TrackKindAudioDescription
	case string(TrackKindMusicOnly):
		return TrackKindMusicOnly
	default:
		return TrackKindUnknown
	}
}

func clamp01(v float64) float64 {
	if v < 0 {
		return 0
	}
	if v > 1 {
		return 1
	}
	return v
}

func truncate(text string, max int) string {
	text = strings.TrimSpace(text)
	if max <= 0 || len(text) <= max {
		return text
	}
	return strings.TrimSpace(text[:max])
}

func englishStereoCandidates(streams []ffprobe.Stream, primaryIndex int) []candidate {
	out := make([]candidate, 0)
	for _, stream := range streams {
		if !strings.EqualFold(stream.CodecType, "audio") {
			continue
		}
		if stream.Index == primaryIndex {
			continue
		}
		if stream.Channels != 2 {
			continue
		}
		if stream.Disposition != nil && stream.Disposition["dub"] == 1 {
			continue
		}
		lang := normalizeLanguage(stream.Tags)
		if !strings.HasPrefix(lang, "en") {
			continue
		}
		title := normalizeTitle(stream.Tags)
		out = append(out, candidate{stream: stream, title: title, lang: lang})
	}
	return out
}

func candidateIndices(cands []candidate) []int {
	out := make([]int, 0, len(cands))
	for _, c := range cands {
		out = append(out, c.stream.Index)
	}
	return out
}

func keepList(primary int, candidates []int) []int {
	seen := map[int]struct{}{}
	out := make([]int, 0, 1+len(candidates))
	if primary >= 0 {
		out = append(out, primary)
		seen[primary] = struct{}{}
	}
	for _, idx := range candidates {
		if _, ok := seen[idx]; ok {
			continue
		}
		seen[idx] = struct{}{}
		out = append(out, idx)
	}
	return out
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
			return strings.TrimSpace(value)
		}
	}
	return ""
}

func (d *Detector) DebugLog(ref Refinement) {
	if d == nil || d.logger == nil {
		return
	}
	attrs := []logging.Attr{
		logging.Int("primary_audio_index", ref.PrimaryIndex),
		logging.Int("keep_audio_streams", len(ref.KeepIndices)),
		logging.Any("keep_audio_indices", ref.KeepIndices),
	}
	if len(ref.Dropped) > 0 {
		attrs = append(attrs,
			logging.Int("dropped_audio_streams", len(ref.Dropped)),
			logging.Any("dropped_decisions", ref.Dropped),
		)
	}
	if len(ref.Kept) > 0 {
		attrs = append(attrs, logging.Any("kept_decisions", ref.Kept))
	}
	d.logger.Info("commentary detection summary", logging.Args(attrs...)...)
}
