package audioanalysis

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"

	"spindle/internal/config"
	"spindle/internal/deps"
	langpkg "spindle/internal/language"
	"spindle/internal/media/audio"
	"spindle/internal/media/ffprobe"
	"spindle/internal/services/llm"
	"spindle/internal/services/whisperx"
	"spindle/internal/textutil"
)

// DiagnosticResult captures full results of commentary detection for CLI diagnostics.
type DiagnosticResult struct {
	PrimaryIndex        int
	PrimaryLabel        string
	PrimaryTranscript   string
	Candidates          []DiagnosticCandidate
	CommentaryIndices   []int
	SimilarityThreshold float64
	ConfidenceThreshold float64
}

// DiagnosticCandidate captures detection details for a single candidate track.
type DiagnosticCandidate struct {
	Index        int
	Language     string
	Title        string
	Channels     int
	Transcript   string
	Similarity   float64
	IsDownmix    bool
	LLMDecision  *CommentaryDecision
	IsCommentary bool
	Reason       string
}

// RunDiagnostic executes commentary detection against a single target file for troubleshooting.
func RunDiagnostic(ctx context.Context, cfg *config.Config, target string, out io.Writer) (*DiagnosticResult, error) {
	ffprobeBinary := deps.ResolveFFprobePath(cfg.FFprobeBinary())
	probe, err := ffprobe.Inspect(ctx, ffprobeBinary, target)
	if err != nil {
		return nil, fmt.Errorf("ffprobe inspect: %w", err)
	}

	selection := audio.Select(probe.Streams)
	if selection.PrimaryIndex < 0 {
		return nil, errors.New("no audio streams found")
	}

	result := &DiagnosticResult{
		PrimaryIndex:        selection.PrimaryIndex,
		PrimaryLabel:        selection.PrimaryLabel(),
		SimilarityThreshold: cfg.Commentary.SimilarityThreshold,
		ConfidenceThreshold: cfg.Commentary.ConfidenceThreshold,
	}

	candidates := FindCommentaryCandidates(probe.Streams, selection.PrimaryIndex)
	if len(candidates) == 0 {
		if out != nil {
			fmt.Fprintln(out, "No commentary candidates found (no 2-channel English/unknown tracks)")
		}
		return result, nil
	}

	if out != nil {
		fmt.Fprintf(out, "Found %d commentary candidate(s)\n", len(candidates))
	}

	workDir, err := os.MkdirTemp("", "spindle-commentary-*")
	if err != nil {
		return nil, fmt.Errorf("create temp directory: %w", err)
	}
	defer os.RemoveAll(workDir)

	whisperSvc := whisperx.NewService(whisperx.Config{
		Model:       cfg.CommentaryWhisperXModel(),
		CUDAEnabled: cfg.Subtitles.WhisperXCUDAEnabled,
		VADMethod:   cfg.Subtitles.WhisperXVADMethod,
		HFToken:     cfg.Subtitles.WhisperXHuggingFace,
	}, deps.ResolveFFmpegPath())

	if out != nil {
		fmt.Fprintf(out, "Transcribing primary audio (track #%d)...\n", selection.PrimaryIndex)
	}
	primaryDir := filepath.Join(workDir, "primary")
	primaryTranscript, err := TranscribeSegment(ctx, whisperSvc, target, selection.PrimaryIndex, primaryDir)
	if err != nil {
		return nil, fmt.Errorf("transcribe primary audio: %w", err)
	}
	result.PrimaryTranscript = primaryTranscript
	primaryFingerprint := textutil.NewFingerprint(primaryTranscript)

	var llmClient *llm.Client
	llmCfg := cfg.CommentaryLLM()
	if llmCfg.APIKey != "" {
		llmClient = llm.NewClientFrom(llmCfg)
	}

	for _, stream := range candidates {
		if out != nil {
			fmt.Fprintf(out, "Processing candidate track #%d...\n", stream.Index)
		}

		candResult := DiagnosticCandidate{
			Index:    stream.Index,
			Language: langpkg.ExtractFromTags(stream.Tags),
			Title:    AudioTitle(stream.Tags),
			Channels: stream.Channels,
		}

		candDir := filepath.Join(workDir, fmt.Sprintf("candidate-%d", stream.Index))
		candidateTranscript, err := TranscribeSegment(ctx, whisperSvc, target, stream.Index, candDir)
		if err != nil {
			candResult.Reason = fmt.Sprintf("transcription failed: %v", err)
			result.Candidates = append(result.Candidates, candResult)
			continue
		}
		candResult.Transcript = candidateTranscript

		candidateFingerprint := textutil.NewFingerprint(candidateTranscript)
		similarity := textutil.CosineSimilarity(primaryFingerprint, candidateFingerprint)
		candResult.Similarity = similarity

		if similarity >= cfg.Commentary.SimilarityThreshold {
			candResult.IsDownmix = true
			candResult.Reason = "stereo_downmix"
			result.Candidates = append(result.Candidates, candResult)
			continue
		}

		if llmClient != nil {
			decision, err := ClassifyCommentary(ctx, llmClient, filepath.Base(target), "", candidateTranscript)
			if err != nil {
				candResult.Reason = fmt.Sprintf("LLM classification failed: %v", err)
				result.Candidates = append(result.Candidates, candResult)
				continue
			}

			candResult.LLMDecision = &decision
			candResult.IsCommentary = decision.IsCommentary(cfg.Commentary.ConfidenceThreshold)
			if candResult.IsCommentary {
				candResult.Reason = "llm_accepted"
				result.CommentaryIndices = append(result.CommentaryIndices, stream.Index)
			} else {
				candResult.Reason = "llm_rejected"
			}
		} else {
			candResult.Reason = "llm_not_configured"
		}

		result.Candidates = append(result.Candidates, candResult)
	}

	return result, nil
}

// WriteDiagnosticTranscripts persists collected transcripts into the current directory.
func WriteDiagnosticTranscripts(result *DiagnosticResult, out io.Writer) error {
	cwd, err := os.Getwd()
	if err != nil {
		return fmt.Errorf("resolve working directory: %w", err)
	}

	if out != nil {
		fmt.Fprintln(out, "\nTranscripts (first 10 minutes):")
	}

	if result.PrimaryTranscript != "" {
		filename := fmt.Sprintf("commentary_primary_%d.txt", result.PrimaryIndex)
		path := filepath.Join(cwd, filename)
		if err := os.WriteFile(path, []byte(result.PrimaryTranscript), 0o644); err != nil {
			return fmt.Errorf("write primary transcript: %w", err)
		}
		if out != nil {
			fmt.Fprintf(out, "  primary #%d -> %s\n", result.PrimaryIndex, filename)
		}
	}

	for _, cand := range result.Candidates {
		if cand.Transcript == "" {
			continue
		}

		reason := cand.Reason
		if reason == "" {
			reason = "unknown"
		}

		filename := fmt.Sprintf("commentary_candidate_%d_%s.txt", cand.Index, textutil.SanitizeToken(reason))
		path := filepath.Join(cwd, filename)
		if err := os.WriteFile(path, []byte(cand.Transcript), 0o644); err != nil {
			return fmt.Errorf("write candidate transcript: %w", err)
		}
		if out != nil {
			fmt.Fprintf(out, "  candidate #%d -> %s\n", cand.Index, filename)
		}
	}

	return nil
}
