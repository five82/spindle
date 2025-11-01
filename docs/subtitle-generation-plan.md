# WhisperX Subtitle Generation Plan

## Summary
Enable optional, fully local subtitle generation by invoking WhisperX with CUDA acceleration. After transcription/alignment, reshape the output to satisfy Netflix best practices (42 characters per line, ≤2 lines, 1–7 second cues, ≤17 CPS) before handing the `.srt` file to Plex/organizer.

## Goals
- Keep the feature opt-in (`subtitles_enabled` in `config.toml`).
- Produce high-quality, Netflix-compliant SRT files without blocking the primary workflow.
- Avoid external APIs or recurring costs; depend only on installed CUDA drivers, `uv`, and WhisperX.

## Configuration
- Retain `SubtitlesEnabled bool` (default `false`) in `internal/config.Config` and add `WhisperXCUDAEnabled bool` (default `false`) to opt into GPU acceleration when CUDA/cuDNN are present.
- Document prerequisites: CUDA 12.8+, cuDNN 9.1 (matching PyTorch wheels), `uv` CLI providing `uvx` when GPU mode is desired; CPU mode works without CUDA.
- Update README/sample config to highlight WhisperX requirements (GPU, disk, temp space).

## Workflow Placement
- Subtitle stage still runs immediately after encoding and before organizer.
- Queue item supplies encoded file path and language metadata (when available).
- Failures log a warning and let the pipeline continue; no hard failure unless future config demands it.

## Standalone CLI Command
- `spindle gensubtitle` now shells out to WhisperX instead of Voxtral.
- Options:
  - Source path (required).
  - Optional `--output` and `--work-dir`.
- Command writes Netflix-shaped SRT alongside the source (or requested directory).

## WhisperX Invocation
- Use `uvx` to run WhisperX in an isolated environment:
  ```
  uvx --index-url https://download.pytorch.org/whl/cu128 \
      --extra-index-url https://pypi.org/simple \
      whisperx <source> \
      --model large-v3 \
      --align_model WAV2VEC2_ASR_LARGE_LV60K_960H \
      --segment_resolution chunk \
      --chunk_size 15 \
      --vad_method pyannote --vad_onset 0.08 --vad_offset 0.07 \
      --beam_size 10 --best_of 10 --temperature 0.0 --patience 1.0 \
      --batch_size 4 --output_format all --language <iso639-1 when known>
  ```
- When CPU mode is requested, drop the CUDA wheel indices and append `--device cpu --compute_type float32`.
- Output directory is a staging subfolder; we parse the resulting JSON for word timings.

## Netflix Formatting
- Flatten WhisperX word-level timings and group them into cues respecting:
  - ≤2 lines, ≤42 characters per line.
  - Duration between 1s and 7s.
  - Reading speed ≤17 characters/second.
  - ≥120 ms gap between consecutive cues.
- Split/merge cues greedily while evaluating those constraints.
- Write `basename.<lang>.srt` using normalized whitespace and newline-wrapped lines.

## Error Handling & Observability
- Update progress messages to “Running WhisperX transcription”.
- Log WhisperX command parameters, segment counts, and duration.
- Keep intermediate WhisperX outputs in a temp directory; remove after SRT is generated.

## Testing
- Unit tests cover cue grouping, line wrapping, CPS checks, and duration adjustments.
- Integration test stubs WhisperX execution (providing JSON fixtures) to exercise `Service.Generate`.
- Manual smoke test script (documented in `docs/development.md`) runs WhisperX on a short clip to validate CUDA setup.

## Documentation Updates
- Refresh `docs/workflow.md` and README to describe WhisperX-based subtitles.
- Provide troubleshooting tips for CUDA/cuDNN mismatches and WhisperX installation.
- Remove references to Voxtral/Mistral API keys.

## Future Enhancements
- Allow user overrides for max CPS/line length per language.
- Support diarization for multi-speaker captioning when WhisperX adds stable APIs.
- Cache WhisperX downloads between runs (shared `uv` cache) and expose metrics (GPU time, VRAM usage).
