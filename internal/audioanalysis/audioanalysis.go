package audioanalysis

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/five82/spindle/internal/config"
	"github.com/five82/spindle/internal/language"
	"github.com/five82/spindle/internal/llm"
	"github.com/five82/spindle/internal/logs"
	"github.com/five82/spindle/internal/media/audio"
	"github.com/five82/spindle/internal/media/ffprobe"
	"github.com/five82/spindle/internal/ripspec"
	"github.com/five82/spindle/internal/stage"
	"github.com/five82/spindle/internal/textutil"
	"github.com/five82/spindle/internal/transcription"
)

// commentarySystemPrompt is the LLM system prompt for commentary classification.
const commentarySystemPrompt = `You are an assistant that determines if an audio track is commentary or not.

IMPORTANT: Commentary tracks come in two forms:
1. Commentary-only: People talking about the film without movie audio
2. Mixed commentary: Movie/TV dialogue plays while commentators talk over it

Both forms are commentary. The presence of movie dialogue does NOT mean it's not commentary.
Mixed commentary will have movie dialogue interspersed with people discussing the film,
providing behind-the-scenes insights, or reacting to scenes.

Commentary tracks include:
- Director/cast commentary over the film (may include movie dialogue in background)
- Behind-the-scenes discussion mixed with film audio
- Any track where people discuss or react to the film while it plays
- Tracks with movie dialogue AND additional voices providing commentary

NOT commentary:
- Alternate language dubs (foreign language replacing original dialogue)
- Audio descriptions for visually impaired (narrator describing on-screen action)
- Stereo downmix of main audio (just the movie audio, no additional commentary)
- Isolated music/effects tracks

Given a transcript sample from an audio track, determine if it is commentary.

You must respond ONLY with JSON: {"decision": "commentary" or "not_commentary", "confidence": 0.0-1.0, "reason": "brief explanation"}`

// maxTranscriptLen is the maximum character length for transcripts sent to
// the LLM. Longer transcripts are truncated with a marker appended.
const maxTranscriptLen = 4000

// commentaryLLMResponse is the expected JSON response from the LLM.
type commentaryLLMResponse struct {
	Decision   string  `json:"decision"`
	Confidence float64 `json:"confidence"`
	Reason     string  `json:"reason"`
}

// Handler implements stage.Handler for audio analysis.
type Handler struct {
	cfg         *config.Config
	llmClient   *llm.Client
	transcriber *transcription.Service
}

// New creates an audio analysis handler.
func New(
	cfg *config.Config,
	llmClient *llm.Client,
	transcriber *transcription.Service,
) *Handler {
	return &Handler{
		cfg:         cfg,
		llmClient:   llmClient,
		transcriber: transcriber,
	}
}

// Run executes the analysis stage: per-episode commentary detection from
// the RIPPED sources. This stage runs concurrently with encoding, so it is
// progress-silent (encoding owns the item progress columns) and persists
// envelope changes only through merge operations.
func (h *Handler) Run(ctx context.Context, sess *stage.Session) error {
	item := sess.Item
	logger := sess.Logger
	logger.Info("analysis stage started", "event_type", "stage_start", "stage", "analysis")
	env := sess.Env

	keys := env.AssetKeys()
	type rippedInput struct {
		key  string
		path string
	}
	var inputs []rippedInput
	for _, key := range keys {
		asset, ok := env.Assets.FindAsset(ripspec.AssetKindRipped, key)
		if ok && asset.IsCompleted() {
			inputs = append(inputs, rippedInput{key: key, path: asset.Path})
		}
	}
	if len(inputs) == 0 {
		return fmt.Errorf("no ripped assets available for analysis")
	}
	logger.Info("analysis plan",
		"event_type", "analysis_plan",
		"ripped_assets", len(inputs),
		"commentary_enabled", h.cfg.Commentary.Enabled,
		"llm_configured", h.llmClient != nil,
	)

	analysisData := &ripspec.AudioAnalysisData{}
	if h.cfg.Commentary.Enabled && h.llmClient != nil {
		for _, in := range inputs {
			if ctx.Err() != nil {
				return ctx.Err()
			}
			result, err := ffprobe.Inspect(ctx, "", in.path)
			if err != nil {
				return fmt.Errorf("ffprobe %s: %w", in.path, err)
			}
			comms, excluded := h.detectCommentary(ctx, sess, result, in.path, item.DiscFingerprint, in.key)
			analysisData.PerEpisode = append(analysisData.PerEpisode, ripspec.EpisodeAudioAnalysis{
				EpisodeKey:       in.key,
				CommentaryTracks: comms,
				ExcludedTracks:   excluded,
			})
			analysisData.CommentaryTracks = append(analysisData.CommentaryTracks, comms...)
			analysisData.ExcludedTracks = append(analysisData.ExcludedTracks, excluded...)
		}
	} else {
		reason := "commentary disabled"
		if h.cfg.Commentary.Enabled {
			reason = "LLM client not configured"
		}
		logger.Info("commentary detection skipped",
			"decision_type", logs.DecisionCommentaryClassification,
			"decision_result", "skipped",
			"decision_reason", reason,
		)
	}

	if err := sess.MergeSave(func(env *ripspec.Envelope) error {
		env.Attributes.AudioAnalysis = analysisData
		return nil
	}); err != nil {
		return err
	}

	logger.Info("analysis stage completed",
		"event_type", "stage_complete",
		"stage", "analysis",
		"commentary_tracks", len(analysisData.CommentaryTracks),
		"excluded_tracks", len(analysisData.ExcludedTracks),
		"ripped_assets", len(inputs),
	)
	return nil
}

// applyPostRefinementAudio selects the post-refinement primary audio track
// for one file, remaps that episode's commentary indices to their
// post-refinement positions, and applies and validates the commentary
// disposition remux (the disposition half of task apply_audio). Disposition
// and validation failures are degraded (logged, tracks unlabeled), not
// fatal. Returns the selected primary track, its label, and the remapped
// commentary refs for the episode.
func applyPostRefinementAudio(
	ctx context.Context,
	logger *slog.Logger,
	path string,
	refinement *AudioRefinementResult,
	comms []ripspec.CommentaryTrackRef,
) (ripspec.AudioTrackRef, string, []ripspec.CommentaryTrackRef, error) {
	result, err := ffprobe.Inspect(ctx, "", path)
	if err != nil {
		return ripspec.AudioTrackRef{}, "", nil, fmt.Errorf("ffprobe post-refinement %s: %w", path, err)
	}

	selection := audio.Select(result.Streams, logger)
	primary := ripspec.AudioTrackRef{Index: selection.PrimaryIndex}

	logger.Info("primary audio selected",
		"decision_type", logs.DecisionAudioSelection,
		"decision_result", selection.PrimaryLabel(),
		"decision_reason", fmt.Sprintf("score-based selection from %d tracks", result.AudioStreamCount()),
	)

	// Remap commentary indices to post-refinement positions and apply disposition.
	remapped := comms
	if len(comms) > 0 && refinement != nil {
		remapped = RemapCommentaryIndices(logger, comms, refinement.KeptIndices)
		if len(remapped) > 0 {
			audioStreams := result.AudioStreams()
			var targets []CommentaryTarget
			for _, r := range remapped {
				var title string
				if r.Index < len(audioStreams) {
					title = audioStreams[r.Index].Tags["title"]
				}
				targets = append(targets, CommentaryTarget{Index: r.Index, Title: title})
			}
			if err := ApplyCommentaryDisposition(ctx, logger, path, targets); err != nil {
				logger.Warn("commentary disposition failed",
					"event_type", "commentary_disposition_error",
					"error_hint", err.Error(),
					"impact", "commentary tracks not labeled",
				)
			} else {
				var remappedIndices []int
				for _, t := range targets {
					remappedIndices = append(remappedIndices, t.Index)
				}
				if err := ValidateCommentaryLabeling(ctx, logger, path, remappedIndices); err != nil {
					logger.Warn("commentary labeling validation failed",
						"event_type", "commentary_validation_error",
						"error_hint", err.Error(),
						"impact", "commentary labels may be incorrect",
					)
				}
			}
		}
	}
	return primary, selection.PrimaryLabel(), remapped, nil
}

// detectCommentary examines non-primary audio tracks for commentary content.
// For each candidate, it first checks stereo similarity against the primary
// track. Tracks that pass the similarity threshold are excluded as downmixes.
// Remaining candidates are transcribed and classified via LLM.
//
// Commentary detection is non-fatal: failures are logged and the track is
// conservatively preserved as commentary.
func (h *Handler) detectCommentary(
	ctx context.Context,
	sess *stage.Session,
	result *ffprobe.Result,
	path string,
	fingerprint string,
	epKey string,
) ([]ripspec.CommentaryTrackRef, []ripspec.ExcludedTrackRef) {
	logger := sess.Logger
	itemID := sess.Item.ID
	var (
		comms    []ripspec.CommentaryTrackRef
		excluded []ripspec.ExcludedTrackRef
	)

	// Identify audio streams with both absolute and audio-relative indices.
	type audioStream struct {
		absIndex   int // absolute stream index (for ffprobe metadata)
		audioIndex int // audio-relative index (for ffmpeg -map 0:a:N)
	}
	var audioStreams []audioStream
	audioCount := 0
	for i, s := range result.Streams {
		if s.CodecType == "audio" {
			audioStreams = append(audioStreams, audioStream{absIndex: i, audioIndex: audioCount})
			audioCount++
		}
	}

	if len(audioStreams) <= 1 {
		logger.Info("commentary detection skipped",
			"decision_type", logs.DecisionCommentaryClassification,
			"decision_result", "skipped",
			"decision_reason", fmt.Sprintf("audio_streams=%d, need >1", len(audioStreams)),
		)
		return nil, nil
	}

	selection := audio.Select(result.Streams, logger)
	primaryAudioIdx := selection.PrimaryIndex
	if primaryAudioIdx < 0 {
		logger.Info("commentary detection skipped",
			"decision_type", logs.DecisionCommentaryClassification,
			"decision_result", "skipped",
			"decision_reason", "no primary audio selected",
		)
		return nil, nil
	}

	candidateCount := len(audioStreams) - 1
	logger.Info("commentary detection plan",
		"decision_type", logs.DecisionCommentaryClassification,
		"decision_result", "analyzing",
		"decision_reason", fmt.Sprintf("primary_audio_index=%d candidates=%d", primaryAudioIdx, candidateCount),
		"episode_key", epKey,
		"audio_streams", len(audioStreams),
		"candidate_tracks", candidateCount,
	)

	// Language filter first: it needs only ffprobe tags and decides which
	// candidates are worth transcribing at all.
	type candidateTrack struct {
		audioIndex int
		stream     ffprobe.Stream
	}
	var candidates []candidateTrack
	for _, as := range audioStreams {
		if as.audioIndex == primaryAudioIdx {
			continue
		}
		stream := result.Streams[as.absIndex]
		rawLang, allowed := allowedAudioLanguage(stream.Tags)
		if !allowed {
			logger.Info("track excluded by language",
				"decision_type", "audio_language_filter",
				"decision_result", "excluded",
				"decision_reason", fmt.Sprintf("language=%s is not english or unknown", rawLang),
				"track_index", as.absIndex,
				"audio_index", as.audioIndex,
			)
			excluded = append(excluded, ripspec.ExcludedTrackRef{
				Index:  as.audioIndex,
				Reason: "non-English audio",
			})
			continue
		}
		candidates = append(candidates, candidateTrack{audioIndex: as.audioIndex, stream: stream})
	}
	if len(candidates) == 0 {
		return comms, excluded
	}

	// Primary fingerprint: reuse the shared transcript artifact when episode
	// identification already produced one; otherwise transcribe the primary
	// once and record it as the artifact so subtitle generation can reuse it.
	primaryFP := h.primaryFingerprint(ctx, sess, path, primaryAudioIdx, epKey)

	// Transcribe ALL candidates in one WhisperX invocation. Each candidate is
	// transcribed exactly once; the same transcript feeds both the stereo
	// similarity filter and LLM classification.
	logger.Info("commentary candidate transcription started",
		"event_type", "commentary_candidates_transcribe",
		"episode_key", epKey,
		"candidate_count", len(candidates),
	)
	candidateText := make(map[int]string, len(candidates))
	if h.transcriber != nil {
		reqs := make([]transcription.TranscribeRequest, len(candidates))
		for i, c := range candidates {
			reqs[i] = transcription.TranscribeRequest{
				InputPath:  path,
				AudioIndex: c.audioIndex,
				Language:   "en",
				OutputDir:  tempOutputDir(fingerprint, epKey, c.audioIndex),
				ItemID:     itemID,
				EpisodeKey: epKey,
				Purpose:    "commentary_candidate",
			}
		}
		results, err := h.transcriber.TranscribeBatch(ctx, reqs)
		if err != nil {
			logger.Warn("candidate transcription batch failed",
				"event_type", "commentary_detection_failed",
				"error_hint", "whisperx batch transcription error",
				"impact", "candidates will be conservatively preserved as commentary",
				"error", err,
				"candidate_count", len(candidates),
			)
		} else {
			for i, c := range candidates {
				text, readErr := os.ReadFile(results[i].SRTPath)
				if readErr != nil {
					logger.Warn("failed to read candidate transcript",
						"event_type", "commentary_detection_failed",
						"error_hint", "could not read srt file",
						"impact", "track will be conservatively preserved as commentary",
						"error", readErr,
						"audio_index", c.audioIndex,
					)
					continue
				}
				candidateText[c.audioIndex] = string(text)
			}
		}
	}

	for i, c := range candidates {
		candidateNumber := i + 1
		text, transcribed := candidateText[c.audioIndex]

		// Stereo similarity filter: compare transcript fingerprints so a
		// stereo downmix of the primary is excluded before LLM classification.
		if transcribed && primaryFP != nil {
			if fp := textutil.NewFingerprint(text); fp != nil {
				sim := textutil.CosineSimilarity(primaryFP, fp)
				logger.Info("stereo similarity check completed",
					"decision_type", logs.DecisionCommentaryStereoFilter,
					"decision_result", "measured",
					"decision_reason", fmt.Sprintf("similarity %.3f", sim),
					"episode_key", epKey,
					"primary_audio_index", primaryAudioIdx,
					"candidate_audio_index", c.audioIndex,
					"similarity", sim,
				)
				if sim >= h.cfg.Commentary.SimilarityThreshold {
					logger.Info("track excluded as stereo downmix",
						"decision_type", logs.DecisionCommentaryStereoFilter,
						"decision_result", "excluded",
						"decision_reason", fmt.Sprintf("similarity %.3f >= threshold %.3f", sim, h.cfg.Commentary.SimilarityThreshold),
						"audio_index", c.audioIndex,
					)
					excluded = append(excluded, ripspec.ExcludedTrackRef{
						Index:      c.audioIndex,
						Reason:     "stereo downmix of primary",
						Similarity: sim,
					})
					continue
				}
			}
		}

		logger.Info("commentary candidate classification",
			"event_type", "commentary_candidate_classify",
			"episode_key", epKey,
			"candidate_number", candidateNumber,
			"candidate_count", candidateCount,
		)
		ref := h.classifyTrack(ctx, logger, c.audioIndex, c.stream, epKey, text, transcribed)
		if ref != nil {
			comms = append(comms, *ref)
		}
	}

	logger.Info("commentary detection complete",
		"event_type", "commentary_detection_complete",
		"episode_key", epKey,
		"commentary_tracks", len(comms),
		"excluded_tracks", len(excluded),
	)
	return comms, excluded
}


// primaryFingerprint returns the transcript fingerprint of the primary audio
// track. It reuses the shared per-episode transcript artifact when one exists
// (recorded by episode identification); otherwise it transcribes the primary
// once into the staging transcripts directory and records the artifact so
// subtitle generation can reuse it. Returns nil (logged) on failure --
// callers then skip the similarity filter, matching the previous behavior.
func (h *Handler) primaryFingerprint(
	ctx context.Context,
	sess *stage.Session,
	path string,
	primaryIdx int,
	epKey string,
) *textutil.Fingerprint {
	logger := sess.Logger

	if asset, ok := sess.Env.Assets.FindAsset(ripspec.AssetKindTranscript, epKey); ok && asset.IsCompleted() {
		if text, err := os.ReadFile(asset.Path); err == nil {
			logger.Info("primary transcript artifact reused",
				"decision_type", logs.DecisionCommentaryStereoFilter,
				"decision_result", "artifact_reused",
				"decision_reason", "canonical transcript already produced earlier in the pipeline",
				"episode_key", epKey,
				"srt_path", asset.Path,
			)
			return textutil.NewFingerprint(string(text))
		}
	}

	if h.transcriber == nil {
		return nil
	}
	stagingRoot, err := sess.Item.StagingRoot(h.cfg.Paths.StagingDir)
	if err != nil {
		logger.Warn("primary transcription skipped",
			"event_type", "commentary_detection_failed",
			"error_hint", "staging root unavailable",
			"impact", "similarity filter disabled for this item",
			"error", err,
		)
		return nil
	}
	result, err := h.transcriber.Transcribe(ctx, transcription.TranscribeRequest{
		InputPath:  path,
		AudioIndex: primaryIdx,
		Language:   "en",
		OutputDir:  filepath.Join(stagingRoot, "transcripts", epKey),
		ItemID:     sess.Item.ID,
		EpisodeKey: epKey,
		Purpose:    "commentary_similarity_primary",
	})
	if err != nil {
		logger.Warn("primary transcription failed",
			"event_type", "commentary_detection_failed",
			"error_hint", "whisperx transcription error",
			"impact", "similarity filter disabled for this item",
			"error", err,
		)
		return nil
	}
	if err := sess.SaveAssetSuccess(ripspec.AssetKindTranscript, ripspec.Asset{
		EpisodeKey: epKey,
		Path:       result.SRTPath,
		Status:     ripspec.AssetStatusCompleted,
	}); err != nil {
		logger.Warn("transcript artifact record failed",
			"event_type", "commentary_detection_failed",
			"error_hint", "could not persist transcript asset",
			"impact", "later stages will re-transcribe the primary track",
			"error", err,
		)
	}
	text, err := os.ReadFile(result.SRTPath)
	if err != nil {
		logger.Warn("failed to read primary transcript",
			"event_type", "commentary_detection_failed",
			"error_hint", "could not read srt file",
			"impact", "similarity filter disabled for this item",
			"error", err,
		)
		return nil
	}
	return textutil.NewFingerprint(string(text))
}

// classifyTrack sends a candidate track's transcript to the LLM for
// commentary classification. The transcript comes from the shared candidate
// batch transcription; transcribed=false means that transcription failed and
// the track is conservatively preserved as commentary. Returns a
// CommentaryTrackRef if the track is classified as commentary (or on error,
// conservatively).
func (h *Handler) classifyTrack(
	ctx context.Context,
	logger *slog.Logger,
	idx int,
	stream ffprobe.Stream,
	epKey string,
	transcript string,
	transcribed bool,
) *ripspec.CommentaryTrackRef {
	if h.llmClient == nil {
		return nil
	}
	if !transcribed {
		logger.Warn("commentary transcription unavailable, conservatively marking as commentary",
			"event_type", "commentary_detection_failed",
			"error_hint", "candidate transcript missing",
			"impact", "track preserved as commentary",
			"track_index", idx,
		)
		return &ripspec.CommentaryTrackRef{
			Index:      idx,
			Confidence: 0,
			Reason:     "transcription failed",
		}
	}

	// Build user prompt.
	userPrompt := buildCommentaryUserPrompt(stream, transcript)

	logger.Info("LLM commentary classification started",
		"event_type", "commentary_llm_start",
		"episode_key", epKey,
		"audio_index", idx,
		"stream_index", stream.Index,
	)
	llmStart := time.Now()
	var resp commentaryLLMResponse
	if err := h.llmClient.CompleteJSON(ctx, commentarySystemPrompt, userPrompt, &resp); err != nil {
		logger.Warn("LLM commentary classification failed, conservatively marking as commentary",
			"event_type", "commentary_detection_failed",
			"error_hint", "llm api error",
			"impact", "track preserved as commentary",
			"error", err,
			"track_index", idx,
		)
		return &ripspec.CommentaryTrackRef{
			Index:      idx,
			Confidence: 0,
			Reason:     fmt.Sprintf("llm classification failed: %v", err),
		}
	}

	logger.Info("LLM commentary classification completed",
		"event_type", "commentary_llm_complete",
		"episode_key", epKey,
		"audio_index", idx,
		"stream_index", stream.Index,
		"duration_ms", time.Since(llmStart).Milliseconds(),
	)

	if resp.Decision == "commentary" && resp.Confidence >= h.cfg.Commentary.ConfidenceThreshold {
		logger.Info("track classified as commentary",
			"decision_type", logs.DecisionCommentaryClassification,
			"decision_result", "commentary",
			"decision_reason", resp.Reason,
			"track_index", idx,
			"confidence", resp.Confidence,
		)
		return &ripspec.CommentaryTrackRef{
			Index:      idx,
			Confidence: resp.Confidence,
			Reason:     resp.Reason,
		}
	}

	logger.Info("track classified as not commentary",
		"decision_type", logs.DecisionCommentaryClassification,
		"decision_result", "not_commentary",
		"decision_reason", resp.Reason,
		"track_index", idx,
		"confidence", resp.Confidence,
	)
	return nil
}

// buildCommentaryUserPrompt constructs the user prompt for commentary LLM
// classification from the stream metadata and transcript text.
func allowedAudioLanguage(tags map[string]string) (string, bool) {
	raw := strings.ToLower(strings.TrimSpace(language.ExtractFromTags(tags)))
	switch raw {
	case "", "und", "nolang", "unknown":
		return raw, true
	}
	return raw, language.ToISO2(raw) == "en"
}

func buildCommentaryUserPrompt(stream ffprobe.Stream, transcript string) string {
	title := strings.TrimSpace(stream.Tags["title"])

	// Truncate transcript if needed.
	if len(transcript) > maxTranscriptLen {
		transcript = transcript[:maxTranscriptLen] + "\n[truncated]"
	}

	var b strings.Builder
	if title != "" {
		_, _ = fmt.Fprintf(&b, "Title: %s\n\n", title)
	}
	_, _ = fmt.Fprintf(&b, "Transcript sample:\n%s", transcript)
	return b.String()
}

// tempOutputDir returns a temporary directory path for transcription output,
// scoped by fingerprint, episode key, and audio index.
func tempOutputDir(fingerprint, epKey string, audioIdx int) string {
	return fmt.Sprintf("/tmp/spindle-commentary-%s-%s-%d", fingerprint, epKey, audioIdx)
}
