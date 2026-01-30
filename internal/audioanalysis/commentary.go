package audioanalysis

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"log/slog"

	"spindle/internal/config"
	"spindle/internal/deps"
	"spindle/internal/logging"
	"spindle/internal/media/audio"
	"spindle/internal/media/ffprobe"
	"spindle/internal/queue"
	"spindle/internal/ripspec"
	"spindle/internal/services/llm"
	"spindle/internal/services/whisperx"
	"spindle/internal/textutil"
)

// CommentaryResult contains the results of commentary track detection.
type CommentaryResult struct {
	PrimaryTrack     TrackInfo
	CommentaryTracks []CommentaryTrack
	ExcludedTracks   []ExcludedTrack
}

// TrackInfo describes an audio track.
type TrackInfo struct {
	Index    int
	Codec    string
	Channels int
	Language string
}

// CommentaryTrack describes a detected commentary track.
type CommentaryTrack struct {
	Index      int
	Confidence float64
	Reason     string
}

// ExcludedTrack describes a track excluded from commentary detection.
type ExcludedTrack struct {
	Index      int
	Reason     string
	Similarity float64
}

// detectCommentary runs the commentary detection pipeline on ripped files.
func (s *Stage) detectCommentary(ctx context.Context, item *queue.Item, env *ripspec.Envelope, targets []string) (*CommentaryResult, error) {
	if len(targets) == 0 {
		return nil, fmt.Errorf("no targets for commentary detection")
	}

	logger := logging.WithContext(ctx, s.logger)

	// Use first target for analysis (consistent audio tracks across episodes)
	targetPath := targets[0]

	// Probe to get audio track info
	ffprobeBinary := deps.ResolveFFprobePath(s.cfg.FFprobeBinary())
	probe, err := ffprobe.Inspect(ctx, ffprobeBinary, targetPath)
	if err != nil {
		return nil, fmt.Errorf("probe for commentary detection: %w", err)
	}

	// Identify primary audio track (already selected)
	primaryIndex := -1
	for _, stream := range probe.Streams {
		if stream.CodecType == "audio" {
			primaryIndex = stream.Index
			break
		}
	}
	if primaryIndex < 0 {
		return nil, fmt.Errorf("no audio tracks found")
	}

	// Find commentary candidates: English 2-channel stereo tracks
	candidates := findCommentaryCandidates(probe.Streams, primaryIndex)
	if len(candidates) == 0 {
		logger.Debug("no commentary candidates found",
			logging.Int("primary_index", primaryIndex),
		)
		return &CommentaryResult{
			PrimaryTrack: TrackInfo{Index: primaryIndex},
		}, nil
	}

	logger.Debug("found commentary candidates",
		logging.Int("candidate_count", len(candidates)),
	)

	// Set up working directory for transcription
	stagingRoot := item.StagingRoot(s.cfg.Paths.StagingDir)
	workDir := filepath.Join(stagingRoot, "commentary")
	if err := os.MkdirAll(workDir, 0o755); err != nil {
		return nil, fmt.Errorf("create commentary work dir: %w", err)
	}

	// Initialize WhisperX service
	whisperCfg := whisperx.Config{
		Model:       s.cfg.Commentary.WhisperXModel,
		CUDAEnabled: s.cfg.Subtitles.WhisperXCUDAEnabled,
		VADMethod:   s.cfg.Subtitles.WhisperXVADMethod,
		HFToken:     s.cfg.Subtitles.WhisperXHuggingFace,
	}
	if whisperCfg.Model == "" {
		whisperCfg.Model = s.cfg.Subtitles.WhisperXModel
	}
	whisperSvc := whisperx.NewService(whisperCfg, deps.ResolveFFmpegPath())

	// Get transcription of primary audio for comparison
	primaryTranscript, err := s.transcribeSegment(ctx, whisperSvc, targetPath, primaryIndex, workDir, "primary")
	if err != nil {
		return nil, fmt.Errorf("transcribe primary audio: %w", err)
	}

	primaryFingerprint := textutil.NewFingerprint(primaryTranscript)

	// Process each candidate
	var commentaryTracks []CommentaryTrack
	var excludedTracks []ExcludedTrack

	llmClient := s.createLLMClient()

	for _, candidate := range candidates {
		// Transcribe candidate
		candidateTranscript, err := s.transcribeSegment(ctx, whisperSvc, targetPath, candidate.Index, workDir, fmt.Sprintf("candidate-%d", candidate.Index))
		if err != nil {
			logger.Warn("failed to transcribe candidate; skipping",
				logging.Int("track_index", candidate.Index),
				logging.Error(err),
			)
			continue
		}

		candidateFingerprint := textutil.NewFingerprint(candidateTranscript)

		// Check similarity to primary audio
		similarity := textutil.CosineSimilarity(primaryFingerprint, candidateFingerprint)
		if similarity >= s.cfg.Commentary.SimilarityThreshold {
			// This is likely a stereo downmix, not commentary
			logger.Debug("candidate excluded as stereo downmix",
				logging.Int("track_index", candidate.Index),
				logging.Float64("similarity", similarity),
			)
			excludedTracks = append(excludedTracks, ExcludedTrack{
				Index:      candidate.Index,
				Reason:     "stereo_downmix",
				Similarity: similarity,
			})
			continue
		}

		// Use LLM to classify
		if llmClient != nil {
			decision, err := s.classifyWithLLM(ctx, llmClient, candidateTranscript, item, env)
			if err != nil {
				logger.Warn("LLM classification failed; skipping candidate",
					logging.Int("track_index", candidate.Index),
					logging.Error(err),
				)
				continue
			}

			if decision.IsCommentary(s.cfg.Commentary.ConfidenceThreshold) {
				commentaryTracks = append(commentaryTracks, CommentaryTrack{
					Index:      candidate.Index,
					Confidence: decision.Confidence,
					Reason:     decision.Reason,
				})
				logger.Info("commentary track detected",
					logging.Int("track_index", candidate.Index),
					logging.Float64("confidence", decision.Confidence),
					logging.String("reason", decision.Reason),
				)
			} else {
				excludedTracks = append(excludedTracks, ExcludedTrack{
					Index:  candidate.Index,
					Reason: fmt.Sprintf("llm_classification: %s (confidence: %.2f)", decision.Decision, decision.Confidence),
				})
				logger.Info("commentary track rejected",
					logging.Int("track_index", candidate.Index),
					logging.String("decision", decision.Decision),
					logging.Float64("confidence", decision.Confidence),
					logging.String("reason", decision.Reason),
				)
			}
		} else {
			// No LLM configured - skip LLM classification
			logger.Debug("LLM not configured; skipping classification",
				logging.Int("track_index", candidate.Index),
			)
		}
	}

	return &CommentaryResult{
		PrimaryTrack:     TrackInfo{Index: primaryIndex},
		CommentaryTracks: commentaryTracks,
		ExcludedTracks:   excludedTracks,
	}, nil
}

// findCommentaryCandidates finds English 2-channel stereo tracks that could be commentary.
func findCommentaryCandidates(streams []ffprobe.Stream, primaryIndex int) []ffprobe.Stream {
	var candidates []ffprobe.Stream
	for _, stream := range streams {
		if !isCommentaryCandidate(stream, primaryIndex) {
			continue
		}
		candidates = append(candidates, stream)
	}
	return candidates
}

// isCommentaryCandidate returns true if the stream could be a commentary track.
// Candidates are non-primary 2-channel audio tracks in English or unknown language.
func isCommentaryCandidate(stream ffprobe.Stream, primaryIndex int) bool {
	if stream.CodecType != "audio" || stream.Index == primaryIndex || stream.Channels != 2 {
		return false
	}
	lang := audio.NormalizeLanguage(stream.Tags)
	return strings.HasPrefix(lang, "en") || lang == "" || lang == "und"
}

// transcribeSegment transcribes a segment of an audio track.
func (s *Stage) transcribeSegment(ctx context.Context, whisperSvc *whisperx.Service, sourcePath string, audioIndex int, workDir, label string) (string, error) {
	segmentDir := filepath.Join(workDir, label)
	if err := os.MkdirAll(segmentDir, 0o755); err != nil {
		return "", fmt.Errorf("create segment dir: %w", err)
	}

	// Extract and transcribe first 10 minutes
	const segmentDurationSec = 600
	result, err := whisperSvc.TranscribeSegment(ctx, sourcePath, audioIndex, 0, segmentDurationSec, segmentDir, "en")
	if err != nil {
		return "", err
	}

	return result.Text, nil
}

// createLLMClient creates an LLM client for commentary classification.
func (s *Stage) createLLMClient() *llm.Client {
	llmCfg := s.cfg.CommentaryLLM()
	if llmCfg.APIKey == "" {
		return nil
	}

	return llm.NewClient(llm.Config{
		APIKey:  llmCfg.APIKey,
		BaseURL: llmCfg.BaseURL,
		Model:   llmCfg.Model,
	})
}

// classifyWithLLM uses an LLM to determine if a transcript is commentary.
func (s *Stage) classifyWithLLM(ctx context.Context, client *llm.Client, transcript string, item *queue.Item, env *ripspec.Envelope) (CommentaryDecision, error) {
	// Build context about the content - use DiscTitle from item
	title := strings.TrimSpace(item.DiscTitle)

	// Try to get year from metadata
	year := ""
	if env != nil && env.Metadata != nil {
		if y, ok := env.Metadata["year"]; ok {
			if yearInt, ok := y.(float64); ok && yearInt > 0 {
				year = fmt.Sprintf("%.0f", yearInt)
			} else if yearStr, ok := y.(string); ok {
				year = yearStr
			}
		}
	}

	// Build user message with transcript
	userMessage := fmt.Sprintf("Title: %s", title)
	if year != "" {
		userMessage += fmt.Sprintf(" (%s)", year)
	}
	userMessage += fmt.Sprintf("\n\nTranscript sample:\n%s", truncateTranscript(transcript, 4000))

	// Call LLM using CompleteJSON for JSON response
	response, err := client.CompleteJSON(ctx, CommentaryClassificationPrompt, userMessage)
	if err != nil {
		return CommentaryDecision{}, fmt.Errorf("llm completion: %w", err)
	}

	// Parse JSON response
	var decision CommentaryDecision
	if err := json.Unmarshal([]byte(response), &decision); err != nil {
		// Try to extract JSON from the response using LLM helper
		if decodeErr := llm.DecodeLLMJSON(response, &decision); decodeErr != nil {
			return CommentaryDecision{}, fmt.Errorf("parse llm response: %w (raw: %s)", err, response)
		}
	}

	return decision, nil
}

// truncateTranscript limits transcript length for LLM input.
func truncateTranscript(transcript string, maxLen int) string {
	if len(transcript) <= maxLen {
		return transcript
	}
	return transcript[:maxLen] + "\n[truncated]"
}

// ApplyCommentaryDisposition remuxes files to set the "comment" disposition on detected commentary tracks.
// This ensures Drapto preserves the disposition and Jellyfin recognizes the tracks as commentary.
func ApplyCommentaryDisposition(ctx context.Context, cfg *config.Config, logger *slog.Logger, targets []string, result *CommentaryResult) error {
	if result == nil || len(result.CommentaryTracks) == 0 {
		return nil // No commentary tracks to mark
	}

	ffmpegBinary := deps.ResolveFFmpegPath()

	// Build a set of commentary track indices for quick lookup
	commentaryIndices := make(map[int]bool)
	for _, track := range result.CommentaryTracks {
		commentaryIndices[track.Index] = true
	}

	for _, path := range targets {
		if err := applyDispositionToFile(ctx, ffmpegBinary, path, commentaryIndices, logger); err != nil {
			return fmt.Errorf("apply commentary disposition to %s: %w", filepath.Base(path), err)
		}
	}

	return nil
}

// audioStreamMapping tracks input-to-output index mapping for audio streams.
type audioStreamMapping struct {
	inputIndex   int
	outputIndex  int
	isCommentary bool
}

// applyDispositionToFile remuxes a single file to set commentary disposition.
func applyDispositionToFile(ctx context.Context, ffmpegBinary, path string, commentaryIndices map[int]bool, logger *slog.Logger) error {
	if len(commentaryIndices) == 0 {
		return nil
	}

	ffprobeBinary := deps.ResolveFFprobePath("")
	probe, err := ffprobe.Inspect(ctx, ffprobeBinary, path)
	if err != nil {
		return fmt.Errorf("probe file: %w", err)
	}

	audioStreams := buildAudioStreamMappings(probe.Streams, commentaryIndices)
	if !hasAnyCommentary(audioStreams) {
		return nil
	}

	// Build ffmpeg command to remux with disposition flags
	tmpPath := path + ".disposition-tmp.mkv"
	args := []string{"-y", "-hide_banner", "-loglevel", "error", "-i", path}

	// Map all streams
	args = append(args, "-map", "0")
	args = append(args, "-c", "copy")

	// Set dispositions for audio streams
	for _, s := range audioStreams {
		if s.isCommentary {
			// Set comment disposition for commentary tracks
			args = append(args, fmt.Sprintf("-disposition:a:%d", s.outputIndex), "comment")
		} else if s.outputIndex == 0 {
			// Ensure first audio track is default
			args = append(args, fmt.Sprintf("-disposition:a:%d", s.outputIndex), "default")
		}
	}

	args = append(args, tmpPath)

	cmd := exec.CommandContext(ctx, ffmpegBinary, args...)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("ffmpeg disposition remux: %w: %s", err, strings.TrimSpace(stderr.String()))
	}

	// Replace original with remuxed file
	if err := os.Remove(path); err != nil {
		_ = os.Remove(tmpPath)
		return fmt.Errorf("remove original: %w", err)
	}
	if err := os.Rename(tmpPath, path); err != nil {
		return fmt.Errorf("rename temp file: %w", err)
	}

	if logger != nil {
		logger.Info("set commentary disposition",
			logging.String("file", filepath.Base(path)),
			logging.Int("commentary_tracks", countCommentaryTracks(audioStreams)),
		)
	}

	return nil
}

// buildAudioStreamMappings creates a mapping of audio streams to their output indices.
func buildAudioStreamMappings(streams []ffprobe.Stream, commentaryIndices map[int]bool) []audioStreamMapping {
	var mappings []audioStreamMapping
	outputIdx := 0
	for _, stream := range streams {
		if stream.CodecType != "audio" {
			continue
		}
		mappings = append(mappings, audioStreamMapping{
			inputIndex:   stream.Index,
			outputIndex:  outputIdx,
			isCommentary: commentaryIndices[stream.Index],
		})
		outputIdx++
	}
	return mappings
}

// hasAnyCommentary returns true if any stream in the mappings is commentary.
func hasAnyCommentary(mappings []audioStreamMapping) bool {
	for _, m := range mappings {
		if m.isCommentary {
			return true
		}
	}
	return false
}

// countCommentaryTracks returns the number of commentary tracks in the mappings.
func countCommentaryTracks(mappings []audioStreamMapping) int {
	count := 0
	for _, m := range mappings {
		if m.isCommentary {
			count++
		}
	}
	return count
}
