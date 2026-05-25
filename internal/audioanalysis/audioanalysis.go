package audioanalysis

import (
	"context"
	"fmt"
	"log/slog"
	"os"
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

// Run executes the audio analysis stage.
func (h *Handler) Run(ctx context.Context, sess *stage.Session) error {
	item := sess.Item
	logger := sess.Logger
	logger.Info("audio analysis stage started", "event_type", "stage_start", "stage", "audio_analysis")
	env := sess.Env

	// Collect encoded asset paths for audio analysis.
	keys := env.AssetKeys()
	var encodedPaths []string
	for _, key := range keys {
		asset, ok := env.Assets.FindAsset(ripspec.AssetKindEncoded, key)
		if ok && asset.IsCompleted() {
			encodedPaths = append(encodedPaths, asset.Path)
		}
	}
	if len(encodedPaths) == 0 {
		return fmt.Errorf("no encoded assets available for audio analysis")
	}
	logger.Info("audio analysis plan",
		"event_type", "audio_analysis_plan",
		"encoded_assets", len(encodedPaths),
		"asset_keys", len(keys),
		"commentary_enabled", h.cfg.Commentary.Enabled,
		"llm_configured", h.llmClient != nil,
	)

	analysisData := &ripspec.AudioAnalysisData{}
	_ = sess.Progress(5, "Phase 1/3 - Commentary detection", stage.WithActiveEpisode(keys[0]))

	// Phase 1: Commentary detection on encoded files.
	// Must run BEFORE audio refinement so commentary track indices can be
	// preserved when refinement strips unwanted tracks.
	logger.Info("Phase 1/3 - Commentary detection")
	var commentaryIndices []int
	if h.cfg.Commentary.Enabled && h.llmClient != nil {
		path := encodedPaths[0]
		result, err := ffprobe.Inspect(ctx, "", path)
		if err != nil {
			return fmt.Errorf("ffprobe %s: %w", path, err)
		}

		comms, excluded := h.detectCommentary(ctx, sess, result, path, item.DiscFingerprint, keys[0])
		analysisData.CommentaryTracks = comms
		analysisData.ExcludedTracks = excluded

		for _, c := range comms {
			commentaryIndices = append(commentaryIndices, c.Index)
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

	// Phase 2: Audio refinement on encoded files.
	// Strips non-English and redundant audio tracks, preserving primary +
	// commentary tracks via additionalKeep.
	_ = sess.Progress(40, "Phase 2/3 - Audio refinement")
	logger.Info("Phase 2/3 - Audio refinement")
	refinement, refErr := RefineAudioTargets(ctx, logger, encodedPaths, commentaryIndices)
	if refErr != nil {
		logger.Warn("audio refinement failed",
			"event_type", "audio_refinement_error",
			"error_hint", refErr.Error(),
			"impact", "audio refinement skipped, proceeding with all tracks",
		)
	} else if refinement != nil && refinement.PrimaryAudioDescription != "" {
		analysisData.PrimaryDescription = refinement.PrimaryAudioDescription
	}

	// Phase 3: Post-refinement primary audio selection and commentary disposition.
	_ = sess.Progress(75, "Phase 3/3 - Post-refinement audio analysis")
	logger.Info("Phase 3/3 - Post-refinement audio analysis")
	{
		path := encodedPaths[0]
		result, err := ffprobe.Inspect(ctx, "", path)
		if err != nil {
			return fmt.Errorf("ffprobe post-refinement %s: %w", path, err)
		}

		selection := audio.Select(result.Streams, logger)
		analysisData.PrimaryTrack = ripspec.AudioTrackRef{Index: selection.PrimaryIndex}
		if analysisData.PrimaryDescription == "" {
			analysisData.PrimaryDescription = selection.PrimaryLabel()
		}

		logger.Info("primary audio selected",
			"decision_type", logs.DecisionAudioSelection,
			"decision_result", selection.PrimaryLabel(),
			"decision_reason", fmt.Sprintf("score-based selection from %d tracks", result.AudioStreamCount()),
		)

		// Remap commentary indices to post-refinement positions and apply disposition.
		if len(analysisData.CommentaryTracks) > 0 && refinement != nil {
			remapped := RemapCommentaryIndices(logger, analysisData.CommentaryTracks, refinement.KeptIndices)
			if len(remapped) > 0 {
				analysisData.CommentaryTracks = remapped
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
	}

	if err := validateAudioTargetDurations(ctx, encodedPaths); err != nil {
		reason := "audio_validation: " + err.Error()
		sess.AddReviewReason(reason)
		logger.Warn("audio validation failed",
			"event_type", "audio_validation_failed",
			"error_hint", err.Error(),
			"impact", "item routed to review",
		)
		logger.Info("validation failure flagged for review",
			"decision_type", logs.DecisionValidationFailureRoute,
			"decision_result", "flagged_for_review",
			"decision_reason", "audio duration validation did not pass",
		)
	}

	// Store analysis in envelope attributes.
	env.Attributes.AudioAnalysis = analysisData

	_ = sess.Progress(95, "Phase 3/3 - Persisting audio analysis")

	// Persist.
	if err := sess.Save(); err != nil {
		return err
	}

	logger.Info("audio analysis stage completed",
		"event_type", "stage_complete",
		"stage", "audio_analysis",
		"primary_audio_index", analysisData.PrimaryTrack.Index,
		"primary_audio", analysisData.PrimaryDescription,
		"commentary_tracks", len(analysisData.CommentaryTracks),
		"excluded_tracks", len(analysisData.ExcludedTracks),
		"encoded_assets", len(encodedPaths),
	)
	return nil
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

	// Examine each non-primary audio track.
	candidateNumber := 0
	for _, as := range audioStreams {
		if as.audioIndex == primaryAudioIdx {
			continue
		}
		candidateNumber++
		idx := as.absIndex
		message := fmt.Sprintf("Phase 1/3 - Commentary detection (candidate %d/%d: similarity check)", candidateNumber, candidateCount)
		_ = sess.Progress(commentaryCandidatePercent(candidateNumber, candidateCount, 0), message, stage.WithActiveEpisode(epKey))
		logger.Info("commentary candidate analysis started",
			"event_type", "commentary_candidate_start",
			"episode_key", epKey,
			"candidate_number", candidateNumber,
			"candidate_count", candidateCount,
			"audio_index", as.audioIndex,
			"stream_index", idx,
		)
		stream := result.Streams[idx]
		rawLang, allowed := allowedAudioLanguage(stream.Tags)
		if !allowed {
			logger.Info("track excluded by language",
				"decision_type", "audio_language_filter",
				"decision_result", "excluded",
				"decision_reason", fmt.Sprintf("language=%s is not english or unknown", rawLang),
				"track_index", idx,
				"audio_index", as.audioIndex,
			)
			excluded = append(excluded, ripspec.ExcludedTrackRef{
				Index:  as.audioIndex,
				Reason: "non-English audio",
			})
			continue
		}

		// Stereo similarity check: compare transcription fingerprints of
		// the primary and candidate tracks.
		if h.transcriber != nil {
			sim, simErr := h.stereoSimilarity(ctx, logger, path, itemID, primaryAudioIdx, as.audioIndex, fingerprint, epKey)
			if simErr != nil {
				logger.Warn("stereo similarity check failed",
					"event_type", "commentary_detection_failed",
					"error_hint", "stereo similarity computation error",
					"impact", "skipping similarity filter for track",
					"error", simErr,
					"track_index", idx,
				)
			} else if sim >= h.cfg.Commentary.SimilarityThreshold {
				logger.Info("track excluded as stereo downmix",
					"decision_type", logs.DecisionCommentaryStereoFilter,
					"decision_result", "excluded",
					"decision_reason", fmt.Sprintf("similarity %.3f >= threshold %.3f", sim, h.cfg.Commentary.SimilarityThreshold),
					"track_index", idx,
				)
				excluded = append(excluded, ripspec.ExcludedTrackRef{
					Index:      as.audioIndex,
					Reason:     "stereo downmix of primary",
					Similarity: sim,
				})
				continue
			}
		}

		// LLM classification via transcription.
		message = fmt.Sprintf("Phase 1/3 - Commentary detection (candidate %d/%d: LLM classification)", candidateNumber, candidateCount)
		_ = sess.Progress(commentaryCandidatePercent(candidateNumber, candidateCount, 0.6), message, stage.WithActiveEpisode(epKey))
		ref := h.classifyTrack(ctx, logger, path, itemID, as.audioIndex, stream, fingerprint, epKey)
		if ref != nil {
			comms = append(comms, *ref)
		}
	}

	_ = sess.Progress(40, fmt.Sprintf("Phase 1/3 - Commentary detection complete (%d commentary, %d excluded)", len(comms), len(excluded)), stage.WithActiveEpisode(epKey))
	return comms, excluded
}

func commentaryCandidatePercent(candidateNumber, candidateCount int, fraction float64) float64 {
	if candidateCount <= 0 {
		return 5
	}
	completed := float64(candidateNumber - 1)
	return 5 + (35 * (completed + fraction) / float64(candidateCount))
}

// stereoSimilarity computes the cosine similarity between transcription
// fingerprints of two audio tracks. This detects stereo downmixes of the
// primary audio.
func (h *Handler) stereoSimilarity(
	ctx context.Context,
	logger *slog.Logger,
	path string,
	itemID int64,
	primaryIdx, candidateIdx int,
	fingerprint, epKey string,
) (float64, error) {
	logger.Info("stereo similarity check started",
		"event_type", "commentary_stereo_check_start",
		"decision_type", logs.DecisionCommentaryStereoFilter,
		"decision_result", "started",
		"decision_reason", fmt.Sprintf("primary_audio_index=%d candidate_audio_index=%d", primaryIdx, candidateIdx),
		"episode_key", epKey,
		"primary_audio_index", primaryIdx,
		"candidate_audio_index", candidateIdx,
	)
	primaryResult, err := h.transcriber.Transcribe(ctx, transcription.TranscribeRequest{
		InputPath:  path,
		AudioIndex: primaryIdx,
		Language:   "en",
		OutputDir:  tempOutputDir(fingerprint, epKey, primaryIdx),
		ItemID:     itemID,
		EpisodeKey: epKey,
		Purpose:    "commentary_similarity_primary",
	})
	if err != nil {
		return 0, fmt.Errorf("transcribe primary: %w", err)
	}

	candidateResult, err := h.transcriber.Transcribe(ctx, transcription.TranscribeRequest{
		InputPath:  path,
		AudioIndex: candidateIdx,
		Language:   "en",
		OutputDir:  tempOutputDir(fingerprint, epKey, candidateIdx),
		ItemID:     itemID,
		EpisodeKey: epKey,
		Purpose:    "commentary_similarity_candidate",
	})
	if err != nil {
		return 0, fmt.Errorf("transcribe candidate: %w", err)
	}

	primaryText, err := os.ReadFile(primaryResult.SRTPath)
	if err != nil {
		return 0, fmt.Errorf("read primary srt: %w", err)
	}
	candidateText, err := os.ReadFile(candidateResult.SRTPath)
	if err != nil {
		return 0, fmt.Errorf("read candidate srt: %w", err)
	}

	fpA := textutil.NewFingerprint(string(primaryText))
	fpB := textutil.NewFingerprint(string(candidateText))

	similarity := textutil.CosineSimilarity(fpA, fpB)
	logger.Info("stereo similarity check completed",
		"decision_type", logs.DecisionCommentaryStereoFilter,
		"decision_result", "measured",
		"decision_reason", fmt.Sprintf("similarity %.3f", similarity),
		"episode_key", epKey,
		"primary_audio_index", primaryIdx,
		"candidate_audio_index", candidateIdx,
		"similarity", similarity,
	)
	return similarity, nil
}

// classifyTrack transcribes a candidate audio track and sends the transcript
// to the LLM for commentary classification. Returns a CommentaryTrackRef if
// the track is classified as commentary (or on error, conservatively).
func (h *Handler) classifyTrack(
	ctx context.Context,
	logger *slog.Logger,
	path string,
	itemID int64,
	idx int,
	stream ffprobe.Stream,
	fingerprint, epKey string,
) *ripspec.CommentaryTrackRef {
	if h.transcriber == nil || h.llmClient == nil {
		return nil
	}

	logger.Info("commentary classification transcription started",
		"event_type", "commentary_classification_transcription_start",
		"episode_key", epKey,
		"audio_index", idx,
		"stream_index", stream.Index,
	)
	result, err := h.transcriber.Transcribe(ctx, transcription.TranscribeRequest{
		InputPath:  path,
		AudioIndex: idx,
		Language:   "en",
		OutputDir:  tempOutputDir(fingerprint, epKey, idx),
		Model:      h.cfg.Commentary.WhisperXModel,
		ItemID:     itemID,
		EpisodeKey: epKey,
		Purpose:    "commentary_classification",
	})
	if err != nil {
		logger.Warn("commentary transcription failed, conservatively marking as commentary",
			"event_type", "commentary_detection_failed",
			"error_hint", "whisperx transcription error",
			"impact", "track preserved as commentary",
			"error", err,
			"track_index", idx,
		)
		return &ripspec.CommentaryTrackRef{
			Index:      idx,
			Confidence: 0,
			Reason:     fmt.Sprintf("transcription failed: %v", err),
		}
	}

	transcript, err := os.ReadFile(result.SRTPath)
	if err != nil {
		logger.Warn("failed to read transcript, conservatively marking as commentary",
			"event_type", "commentary_detection_failed",
			"error_hint", "could not read srt file",
			"impact", "track preserved as commentary",
			"error", err,
			"track_index", idx,
		)
		return &ripspec.CommentaryTrackRef{
			Index:      idx,
			Confidence: 0,
			Reason:     fmt.Sprintf("read transcript failed: %v", err),
		}
	}

	// Build user prompt.
	userPrompt := buildCommentaryUserPrompt(stream, string(transcript))

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
