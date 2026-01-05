package commentary

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
	"spindle/internal/logging"
)

// speakerEmbedScript is the embedded Python script for speaker embedding.
// It uses pyannote for speaker diarization and embedding extraction.
// Audio is pre-loaded via torchaudio to avoid pyannote's torchcodec issues.
const speakerEmbedScript = `#!/usr/bin/env python3
import argparse
import json
import sys
import warnings

# Suppress the torchcodec warning since we pre-load audio ourselves
warnings.filterwarnings("ignore", message=".*torchcodec.*")

import numpy as np
import torch
import torchaudio
from pyannote.audio import Inference, Model, Pipeline
from pyannote.core import Segment


def load_audio(audio_path, sample_rate=16000):
    """Load audio using torchaudio and return pyannote-compatible dict."""
    waveform, sr = torchaudio.load(audio_path)
    # Resample if needed
    if sr != sample_rate:
        resampler = torchaudio.transforms.Resample(sr, sample_rate)
        waveform = resampler(waveform)
    # Convert to mono if stereo
    if waveform.shape[0] > 1:
        waveform = waveform.mean(dim=0, keepdim=True)
    return {"waveform": waveform, "sample_rate": sample_rate}


def extract_embeddings(audio_path, hf_token):
    """Run diarization and extract per-speaker embeddings."""
    # Pre-load audio to avoid torchcodec issues
    audio_dict = load_audio(audio_path)

    # Use GPU if available
    device = torch.device("cuda" if torch.cuda.is_available() else "cpu")

    pipeline = Pipeline.from_pretrained(
        "pyannote/speaker-diarization-3.1",
        token=hf_token,
    ).to(device)
    # Load embedding model explicitly, then wrap with Inference
    emb_model = Model.from_pretrained("pyannote/embedding", token=hf_token).to(device)
    embedding = Inference(emb_model, window="whole")

    # Run diarization on pre-loaded audio
    result = pipeline(audio_dict)
    # pyannote 3.x returns DiarizeOutput; extract the annotation
    diarization = result.speaker_diarization if hasattr(result, 'speaker_diarization') else result

    # Find longest segment per speaker
    speakers = {}
    for turn, _, speaker in diarization.itertracks(yield_label=True):
        dur = turn.end - turn.start
        if speaker not in speakers or dur > speakers[speaker]["duration"]:
            speakers[speaker] = {"start": turn.start, "end": turn.end, "duration": dur}

    # Extract embeddings for each speaker's best segment
    embeddings = {}
    for speaker, seg in speakers.items():
        # Crop waveform manually for embedding extraction
        sr = audio_dict["sample_rate"]
        start_sample = int(seg["start"] * sr)
        end_sample = int(seg["end"] * sr)
        segment_waveform = audio_dict["waveform"][:, start_sample:end_sample]
        segment_dict = {"waveform": segment_waveform, "sample_rate": sr}
        emb = embedding(segment_dict)
        embeddings[speaker] = emb.flatten().tolist()

    return {"speaker_count": len(embeddings), "embeddings": embeddings}


def cosine_similarity(a, b):
    """Compute cosine similarity between two embedding vectors."""
    a_arr, b_arr = np.array(a), np.array(b)
    norm_a = np.linalg.norm(a_arr)
    norm_b = np.linalg.norm(b_arr)
    if norm_a == 0 or norm_b == 0:
        return 0.0
    return float(np.dot(a_arr, b_arr) / (norm_a * norm_b))


def compare_embeddings(primary, candidate):
    """Compare speaker embeddings between primary and candidate."""
    max_sim = 0.0
    if not primary["embeddings"] or not candidate["embeddings"]:
        return {
            "primary_speaker_count": primary["speaker_count"],
            "candidate_speaker_count": candidate["speaker_count"],
            "max_similarity": 0.0,
            "same_speakers": False,
        }
    for p_emb in primary["embeddings"].values():
        for c_emb in candidate["embeddings"].values():
            sim = cosine_similarity(p_emb, c_emb)
            max_sim = max(max_sim, sim)
    return {
        "primary_speaker_count": primary["speaker_count"],
        "candidate_speaker_count": candidate["speaker_count"],
        "max_similarity": max_sim,
        "same_speakers": max_sim >= 0.7,
    }


def main():
    parser = argparse.ArgumentParser()
    parser.add_argument("--primary", required=True)
    parser.add_argument("--candidate", required=True)
    parser.add_argument("--hf-token", required=True)
    args = parser.parse_args()
    try:
        primary = extract_embeddings(args.primary, args.hf_token)
        candidate = extract_embeddings(args.candidate, args.hf_token)
        result = compare_embeddings(primary, candidate)
        print(json.dumps(result))
    except Exception as e:
        print(json.dumps({"error": str(e)}), file=sys.stderr)
        sys.exit(1)


if __name__ == "__main__":
    main()
`

// SpeakerResult holds the Python script output.
type SpeakerResult struct {
	PrimarySpeakerCount   int     `json:"primary_speaker_count"`
	CandidateSpeakerCount int     `json:"candidate_speaker_count"`
	MaxSimilarity         float64 `json:"max_similarity"`
	SameSpeakers          bool    `json:"same_speakers"`
	Error                 string  `json:"error,omitempty"`
}

// speakerEmbeddingAvailable checks if pyannote speaker embedding can be used.
func speakerEmbeddingAvailable(cfg *config.Config) bool {
	if cfg == nil || !cfg.CommentaryDetection.SpeakerEmbeddingEnabled {
		return false
	}
	// Require HuggingFace token (same as WhisperX pyannote VAD)
	if strings.TrimSpace(cfg.Subtitles.WhisperXHuggingFace) == "" {
		return false
	}
	// Check uvx is available
	if _, err := exec.LookPath("uvx"); err != nil {
		return false
	}
	return true
}

// applySpeakerEmbeddingClassification runs speaker embedding analysis
// for ambiguous candidates to determine if they share speakers with primary.
func applySpeakerEmbeddingClassification(
	ctx context.Context,
	cfg *config.Config,
	ffmpegBinary, path string,
	windows []window,
	workDir string,
	primaryIndex int,
	decisions []Decision,
	logger *slog.Logger,
) []Decision {
	// Find candidates that need speaker analysis
	candidates := []int{}
	for idx, d := range decisions {
		if d.Include || d.Metadata.Negative != "" {
			continue
		}
		// Only run on ambiguous or audio_description candidates
		switch d.Reason {
		case "ambiguous", "audio_description", "audio_description_outlier":
			candidates = append(candidates, idx)
		}
	}
	if len(candidates) == 0 {
		return decisions
	}

	// Extract primary audio sample once
	primarySample := filepath.Join(workDir, "speaker-primary.wav")
	if err := extractSpeakerSample(ctx, ffmpegBinary, path, primaryIndex, windows, primarySample); err != nil {
		logger.Warn("speaker embedding: failed to extract primary sample",
			logging.Error(err),
			logging.String(logging.FieldEventType, "speaker_embedding_extract_failed"),
		)
		return decisions
	}

	for _, idx := range candidates {
		d := decisions[idx]
		candidateSample := filepath.Join(workDir, fmt.Sprintf("speaker-%d.wav", d.Index))
		if err := extractSpeakerSample(ctx, ffmpegBinary, path, d.Index, windows, candidateSample); err != nil {
			logger.Debug("speaker embedding: failed to extract candidate sample",
				logging.Int("stream_index", d.Index),
				logging.Error(err),
			)
			continue
		}

		result, err := runSpeakerEmbedding(ctx, cfg, primarySample, candidateSample, workDir)
		if err != nil {
			logger.Warn("speaker embedding analysis failed",
				logging.Int("stream_index", d.Index),
				logging.Error(err),
				logging.String(logging.FieldEventType, "speaker_embedding_failed"),
			)
			continue
		}

		logger.Debug("speaker embedding result",
			logging.Int("stream_index", d.Index),
			logging.Int("primary_speakers", result.PrimarySpeakerCount),
			logging.Int("candidate_speakers", result.CandidateSpeakerCount),
			logging.Float64("max_similarity", result.MaxSimilarity),
			logging.Bool("same_speakers", result.SameSpeakers),
		)

		// Update decision based on speaker analysis
		decisions[idx] = classifySpeakerResult(decisions[idx], result, cfg)
	}

	return decisions
}

// extractSpeakerSample extracts audio from a stream to a WAV file for speaker analysis.
func extractSpeakerSample(ctx context.Context, ffmpegBinary, path string, streamIndex int, windows []window, outputPath string) error {
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
		"-ar", "16000",
		"-c:a", "pcm_s16le",
		outputPath,
	)
	cmd := exec.CommandContext(ctx, ffmpegBinary, args...) //nolint:gosec
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("ffmpeg speaker extract: %w: %s", err, strings.TrimSpace(stderr.String()))
	}
	return nil
}

// runSpeakerEmbedding calls the Python script via uvx to compare speaker embeddings.
func runSpeakerEmbedding(ctx context.Context, cfg *config.Config, primaryPath, candidatePath, workDir string) (SpeakerResult, error) {
	// Write the Python script to a temp file
	scriptPath := filepath.Join(workDir, "speaker_embed.py")
	if err := os.WriteFile(scriptPath, []byte(speakerEmbedScript), 0o644); err != nil {
		return SpeakerResult{}, fmt.Errorf("write speaker script: %w", err)
	}

	hfToken := strings.TrimSpace(cfg.Subtitles.WhisperXHuggingFace)
	cudaEnabled := cfg.Subtitles.WhisperXCUDAEnabled

	// Build uvx command with pyannote dependencies
	// torchaudio + soundfile required as audio decoder fallback (torchcodec often fails)
	// --refresh ensures we get the correct dependencies (uvx caches aggressively)
	args := []string{
		"--refresh",
		"--quiet",
		"--with", "pyannote.audio",
		"--with", "numpy",
		"--with", "torchaudio",
		"--with", "soundfile",
		"--with", "omegaconf",
	}
	if cudaEnabled {
		args = append(args,
			"--index-url", "https://download.pytorch.org/whl/cu128",
			"--extra-index-url", "https://pypi.org/simple",
		)
	}
	args = append(args, "python", scriptPath,
		"--primary", primaryPath,
		"--candidate", candidatePath,
		"--hf-token", hfToken,
	)

	cmd := exec.CommandContext(ctx, "uvx", args...) //nolint:gosec
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	// Set environment for PyTorch compatibility and HuggingFace auth
	cmd.Env = os.Environ()
	cmd.Env = append(cmd.Env, "HF_TOKEN="+hfToken)
	if os.Getenv("TORCH_FORCE_NO_WEIGHTS_ONLY_LOAD") == "" {
		cmd.Env = append(cmd.Env, "TORCH_FORCE_NO_WEIGHTS_ONLY_LOAD=1")
	}

	if err := cmd.Run(); err != nil {
		stderrStr := stderr.String()
		// Try to parse error from stderr
		var result SpeakerResult
		if json.Unmarshal(stderr.Bytes(), &result) == nil && result.Error != "" {
			return SpeakerResult{}, fmt.Errorf("speaker embedding: %s", result.Error)
		}
		// Check for common HuggingFace gated model errors
		if strings.Contains(stderrStr, "GatedRepoError") || strings.Contains(stderrStr, "401") {
			return SpeakerResult{}, fmt.Errorf("speaker embedding: HuggingFace model access denied. Visit https://hf.co/pyannote/speaker-diarization-3.1 and https://hf.co/pyannote/embedding to accept the model terms, then retry")
		}
		// Extract the actual error by finding the last Python exception
		errMsg := strings.TrimSpace(stderrStr)
		if idx := strings.LastIndex(errMsg, "Error:"); idx != -1 {
			errMsg = strings.TrimSpace(errMsg[idx:])
		} else if idx := strings.LastIndex(errMsg, "Exception:"); idx != -1 {
			errMsg = strings.TrimSpace(errMsg[idx:])
		} else if lines := strings.Split(errMsg, "\n"); len(lines) > 0 {
			// Get last non-empty line as error summary
			for i := len(lines) - 1; i >= 0; i-- {
				if line := strings.TrimSpace(lines[i]); line != "" {
					errMsg = line
					break
				}
			}
		}
		return SpeakerResult{}, fmt.Errorf("speaker embedding: %w: %s", err, errMsg)
	}

	var result SpeakerResult
	if err := json.Unmarshal(stdout.Bytes(), &result); err != nil {
		return SpeakerResult{}, fmt.Errorf("parse speaker result: %w", err)
	}
	return result, nil
}

// classifySpeakerResult updates a decision based on speaker embedding analysis.
// It combines speaker similarity with the existing speech-in-silence metric
// to distinguish between downmix, audio description, and commentary.
//
// Key insight: Audio description tracks contain movie audio mixed with narrator.
// So AD tracks show HIGH speaker similarity (movie audio matches) but also
// HIGH speech-in-silence (narrator speaks during quiet moments).
func classifySpeakerResult(d Decision, r SpeakerResult, cfg *config.Config) Decision {
	silenceThreshold := cfg.CommentaryDetection.SpeechInSilenceMax
	if silenceThreshold <= 0 {
		silenceThreshold = 0.40
	}

	// Very high speaker similarity (>0.95) + high fingerprint (>0.85) = definite downmix
	// This takes priority over speech-in-silence which can have measurement variance
	if r.MaxSimilarity >= 0.95 && d.Metrics.FingerprintSimilarity >= 0.85 {
		d.Include = false
		d.Reason = "speaker_same_voices"
		return d
	}

	// High speech-in-silence indicates AD pattern (narrator speaks during silences)
	// AD tracks have movie audio bleed-through, so moderate-high similarity is expected
	if d.Metrics.SpeechInPrimarySilence > silenceThreshold {
		d.Include = false
		d.Reason = "speaker_audio_description"
		return d
	}

	// Low speech-in-silence: check if same speakers (downmix) or different (commentary)
	if r.SameSpeakers || r.MaxSimilarity >= 0.7 {
		// Same voices + low speech-in-silence -> downmix/duplicate
		d.Include = false
		d.Reason = "speaker_same_voices"
		return d
	}

	// Different speakers + low speech-in-silence -> commentary
	if r.MaxSimilarity < 0.5 {
		d.Include = true
		d.Reason = "speaker_commentary"
		return d
	}

	// Ambiguous similarity range (0.5-0.7) - fall through to WhisperX
	return d
}
