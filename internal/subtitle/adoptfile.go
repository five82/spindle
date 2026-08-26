package subtitle

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strings"

	"github.com/five82/spindle/internal/logs"
	"github.com/five82/spindle/internal/transcription"
)

// AdoptFileRequest describes one manual adoption run for an arbitrary file
// (the `spindle subtitle` command). TMDBID is required; Season/Episode are
// zero for movies.
type AdoptFileRequest struct {
	VideoPath string
	WorkDir   string
	TMDBID    int
	Season    int
	Episode   int
	// Transcript, when non-nil, is a pre-computed WhisperX result (batched
	// multi-file runs) reused as the sync reference instead of transcribing.
	Transcript *transcription.TranscribeResult
	Logger     *slog.Logger
	// OnTranscribeStart/OnTranscribeComplete report sync-reference
	// transcription for CLI progress; not called when Transcript is reused.
	OnTranscribeStart    func()
	OnTranscribeComplete func(*transcription.TranscribeResult)
}

// AdoptFileResult describes the adopted subtitle written into WorkDir; the
// caller owns final placement (sidecar copy or mux).
type AdoptFileResult struct {
	SubtitlePath       string
	Language           string
	Candidate          string
	Segments           int
	Validation         string
	GateMetrics        string
	RejectedCandidates []string
}

// AdoptForFile runs the adoption process — search, clean, sync, verify — for
// one file with an explicit TMDB identity. It returns an error when no
// candidate passes verification; generating subtitles from scratch is the
// whisperx-subtitles agent skill's job.
func (h *Handler) AdoptForFile(ctx context.Context, req AdoptFileRequest) (*AdoptFileResult, error) {
	if h.osClient == nil {
		return nil, errors.New("OpenSubtitles is not configured (subtitles.opensubtitles_api_key)")
	}
	if req.TMDBID <= 0 {
		return nil, errors.New("TMDB identity required: pass --tmdb-id or use a path containing [tmdbid-ID]")
	}
	if strings.TrimSpace(req.VideoPath) == "" || strings.TrimSpace(req.WorkDir) == "" {
		return nil, errors.New("adopt subtitle: missing video path or work dir")
	}
	if err := os.MkdirAll(req.WorkDir, 0o755); err != nil {
		return nil, fmt.Errorf("create work dir: %w", err)
	}
	logger := logs.Default(req.Logger)

	results, err := h.osClient.Search(ctx, req.TMDBID, req.Season, req.Episode, h.searchLanguages())
	if err != nil {
		return nil, fmt.Errorf("opensubtitles search: %w", err)
	}
	profile := subtitleSourceProfile(ctx, logger, req.VideoPath, "")
	candidates := rankSearchCandidatesForSource(results, req.Season, req.Episode, profile)
	if len(candidates) == 0 {
		return nil, errors.New("OpenSubtitles returned no usable candidates")
	}
	if len(candidates) > maxSubtitleCandidates {
		candidates = candidates[:maxSubtitleCandidates]
	}
	fileIDs := make([]int, len(candidates))
	for i, candidate := range candidates {
		fileIDs[i] = candidate.FileID
	}
	logger.Info("subtitle candidate attempt set selected",
		"decision_type", "subtitle_candidate_ranking",
		"decision_result", "selected",
		"decision_reason", "source affinity orders release/file matches before generic or conflicting candidates",
		"source_profile", profile.class,
		"input_resolution", profile.resolution(),
		"candidate_file_ids", fmt.Sprint(fileIDs),
		"attempt_count", len(candidates),
	)

	transcript := req.Transcript
	if transcript == nil {
		if h.transcriber == nil {
			return nil, errors.New("transcriber not configured")
		}
		selected, err := h.transcriber.SelectPrimaryAudioTrack(ctx, req.VideoPath, "en")
		if err != nil {
			return nil, fmt.Errorf("select primary audio: %w", err)
		}
		if req.OnTranscribeStart != nil {
			req.OnTranscribeStart()
		}
		transcript, err = h.transcriber.Transcribe(ctx, transcription.TranscribeRequest{
			InputPath:  req.VideoPath,
			AudioIndex: selected.Index,
			Language:   selected.Language,
			OutputDir:  req.WorkDir,
			Purpose:    "subtitle_sync_reference",
		})
		if err != nil {
			return nil, fmt.Errorf("transcribe sync reference: %w", err)
		}
		if req.OnTranscribeComplete != nil {
			req.OnTranscribeComplete(transcript)
		}
	}
	adopt, err := buildAdoptContext(ctx, logger, transcript, req.VideoPath, req.WorkDir)
	if err != nil {
		return nil, err
	}

	adopted, rejected, err := h.adoptFirstCandidate(ctx, logger, candidates, adopt, filepath.Join(req.WorkDir, filepath.Base(req.VideoPath)))
	if err != nil {
		return nil, err
	}
	if adopted == nil {
		return nil, fmt.Errorf("no downloaded subtitle passed verification:\n  %s", strings.Join(rejected, "\n  "))
	}
	return &AdoptFileResult{
		SubtitlePath:       adopted.DisplayPath,
		Language:           adopted.Language,
		Candidate:          adopted.Candidate.label(),
		Segments:           adopted.Segments,
		Validation:         subtitleValidationResult(adopted.Validation),
		GateMetrics:        adopted.Check.Metrics(),
		RejectedCandidates: rejected,
	}, nil
}
