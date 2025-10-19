# AI-Generated Subtitles Implementation Plan

## Summary
Introduce optional AI transcription using the Voxtral Mini Transcribe 2507 model via the Mistral API to generate Plex-compatible external `.srt` subtitles for the primary audio track of encoded outputs.

## Goals
- Keep the feature opt-in with clear configuration toggles and an API key entry.
- Generate high-quality SRT subtitles without blocking the main ripping/encoding pipeline.
- Align subtitle naming and placement with Plex expectations so the organizer stage moves them automatically.

## Configuration
- Add `SubtitlesEnabled bool` (default `false`) and `MistralAPIKey string` to `internal/config.Config` and TOML parsing.
- Document new keys in `README.md` and sample config snippets.
- Ensure the API key never appears in logs; restrict it to the subtitle module via dependency injection.

## Workflow Placement
- Hook a new subtitle stage immediately after encoding completes and before organizer runs in the background lane.
- Pass queue item metadata (encoded file path, primary language) to the subtitle module.
- On failure, log a warning, skip subtitle generation, and proceed unless a future `subtitle_required` flag is enabled.

## Standalone CLI Command
- Add a new CLI entry (working name `spindle gensubtitle`) that accepts a path to an already encoded MKV/MP4 file.
- Reuse the subtitle module to demux the primary audio, submit it to Voxtral, and drop the resulting `basename.<lang>.srt` alongside the source file.
- Allow overriding the output directory via a flag; exit non-zero when the API key is missing or transcription fails.
- Share implementation with the workflow stage to avoid duplicate logic and keep unit tests consistent.

## Audio Extraction & Chunking
- Reuse `ffprobe` results from encoding to confirm the primary audio stream (fall back to stream index 0 if metadata missing).
- Use `ffmpeg` to demux the primary audio into an `.opus` (Ogg) file via `-c copy`; run end-to-end tests to confirm Voxtral accepts it before adding any transcode fallback.
- Slice the demuxed audio into ≤5 minute segments with small (1–2 s) overlaps to satisfy Mistral limits and preserve dialogue continuity.

## Mistral API Integration
- Create `internal/subtitles` with a `Client` interface so tests can stub responses.
- Call `POST /v1/audio/transcriptions` with multipart form data (`file`, `model=voxtral-mini-2507`, `language` when detected, `timestamp_granularities=segment`).
- Serialize requests sequentially to respect rate limits; add exponential backoff for `429` or transient errors.
- Track cumulative offsets per chunk so returned segment timestamps translate to absolute positions.

## Post-Processing & SRT Output
- Build cue list by adjusting each segment’s `start`/`end` with the chunk offset and clamping negatives.
- Deduplicate overlapping cues from chunk overlaps (prefer longer span, merge ellipses when needed).
- Normalize whitespace, wrap at ~42 characters, and NFC-normalize text to avoid mixed forms.
- Emit `basename.<lang>.srt` (e.g., `Movie.en.srt`) alongside the encoded MKV in staging; organizer will move both.
- Optionally store a JSON summary (counts, duration, API metrics) in logs for debugging.

## Error Handling & Observability
- Update queue progress messages (e.g., "Generating AI subtitles") and percentages while transcription runs.
- Capture API latency, chunk count, and total cost estimate in debug logs.
- If the API key is missing and subtitles are enabled, surface a configuration error and mark the queue item `failed` with a clear message.

## Testing Strategy
- Unit tests for chunking, timestamp adjustments, and SRT formatting using fixture JSON.
- Integration test with a fake Mistral client returning deterministic segments; ensure files land in staging and organizer picks them up.
- Manual smoke test script to hit the real API with a short audio clip (documented in `docs/development.md`).

## Documentation & Tooling Updates
- Extend `docs/workflow.md` with a section explaining the subtitle stage.
- Add a configuration snippet and cost warning to `README.md`.
- Provide a CLI how-to in `docs/development.md` describing testing and environment variables for the API key.

## Future Enhancements
- Optional parallel segment uploads once sequential flow proves stable.
- Language overrides per queue item or via CLI flags.
- Forced/SDH subtitle variants if Voxtral metadata exposes the distinction.
