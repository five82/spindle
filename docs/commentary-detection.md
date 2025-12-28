# Commentary Detection (WhisperX + LLM)

Disc metadata and stream titles are not reliable for commentary tracks. When
`commentary_detection_enabled = true`, Spindle runs a lightweight audio
classifier before encoding so it can keep commentary tracks even when labels are
missing or wrong.

## What it does

During the encoding stage (before Drapto starts), Spindle:

1. Picks the primary audio stream using the normal audio selection rules.
2. Considers every *English stereo* (`2ch`) audio stream as a candidate.
3. Transcribes short snippets from the primary track and each candidate using
   WhisperX (cached).
4. Drops obvious non-commentary candidates (duplicates, music-only).
5. Optionally calls an OpenRouter-hosted LLM to classify the remaining tracks as
   `commentary` vs `audio_description` vs `unknown`.
6. Remuxes a working copy of the source MKV to keep only the primary track +
   detected commentary tracks, then proceeds with encoding (the rip cache stays
   untouched).

When commentary detection is disabled, Spindle keeps only the primary audio
track (no heuristics).

## Configuration

```toml
commentary_detection_enabled = true

# OpenRouter (defaults to the preset-decider settings when empty)
commentary_detection_model = ""
commentary_detection_base_url = ""
commentary_detection_api_key = ""
commentary_detection_referer = ""
commentary_detection_title = "Spindle Commentary Detector"
commentary_detection_timeout_seconds = 45
```

Notes:

- When a `commentary_detection_*` value is empty (or `commentary_detection_timeout_seconds`
  is 0), Spindle falls back to the corresponding `preset_decider_*` value. This
  keeps configuration simple when you want one OpenRouter model for both
  features, but still allows a dedicated model if you prefer.
- If OpenRouter is not configured (no API key), Spindle still filters duplicates
  and music-only tracks, then keeps the remaining English stereo candidates (it
  errs on the side of retaining).
- Commentary detection uses WhisperX, so it needs the same local dependency path
  as subtitles (Spindle shells out via `uvx`).

Set `SPD_DEBUG_COMMENTARY_KEEP=1` before launching the daemon to retain
detector artifacts under each queue item’s staging folder.

## Stage 3 (detailed): "same as primary" detection

Before involving an LLM, Spindle tries to drop tracks that are essentially
duplicates of the primary program audio (for example, a second stereo mix).

Implementation details (see `internal/commentaryid/similarity.go`):

- **Window sampling**: Spindle transcribes ~3 windows (roughly around 1 minute
  in, 33% in, and 66% in). Each window is ~75 seconds (or shorter for short
  runtimes).
- **Tokenization**: Transcripts are lowercased and split on non-alphanumeric
  boundaries; tokens shorter than 3 characters are ignored.
- **Similarity metrics (per window)**:
  - cosine similarity of token-count vectors
  - *purity*: overlap tokens / candidate tokens
  - *coverage*: overlap tokens / primary tokens
- **Decision rule**: a candidate is tagged `same_as_primary` only if a majority
  of valid windows exceed the thresholds (currently cosine ≥ 0.93, purity ≥
  0.92, coverage ≥ 0.92, with a minimum token floor per window).

Requiring multiple windows avoids dropping tracks that are partially similar
(for example, a commentary track that references dialogue but is mostly unique).

## What the LLM sees

Spindle sends the LLM a JSON payload containing:

- the ripped file basename
- the primary audio stream index + a short transcript sample
- each candidate track’s stream index, channels, language, ffprobe title, and a
  short transcript sample (samples are truncated to keep prompts bounded)

No file paths, host identifiers, or queue IDs are included.
