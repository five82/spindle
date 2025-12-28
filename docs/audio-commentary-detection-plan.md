# Audio Commentary Detection Plan

## Summary
Add high-precision commentary track detection that preserves the primary audio plus qualifying commentary tracks, while excluding duplicates, audio description, and music-only tracks. Detection must handle both commentary-only and mixed-with-program-audio tracks. Commentary candidates are limited to English, stereo streams. Ambiguous candidates are dropped (no REVIEW state). The rip cache must remain raw and unmodified; commentary selection is applied only to a working copy outside the cache.

## Goals
- Detect English stereo commentary tracks (commentary-only and mixed commentary).
- Exclude false positives:
  - Duplicates/downmixes of the primary track.
  - Audio description (AD/DVS/visually impaired).
  - Music-only or isolated score tracks.
- Keep the primary audio track plus detected commentary tracks through encoding.
- Preserve rip cache as an immutable raw rip; no post-processing alters it.

## Non-Goals
- Human review flow for ambiguous tracks (explicitly disabled).
- Commentary detection for non-English or non-stereo tracks.

## Rip Cache Strategy (Immutable Raw + Working Copy)
Current behavior refines audio in-place before caching; this discards commentary tracks. The new flow must:

1. Rip raw MKV into the rip cache directory (all audio tracks preserved).
2. Create a working copy in the staging area (outside the cache) for analysis and remux.
3. Perform commentary detection and audio selection on the working copy only.
4. Never modify files inside the rip cache (no remux, no sidecar metadata).

Cache hit flow:
- Copy raw MKV(s) from cache to staging and continue analysis there.

Cache miss flow:
- Rip to cache, then copy to staging and analyze there.

Rationale: The cache becomes a reusable raw source, and the working copy can be safely altered for selection.

## Detection Pipeline

### 1) Candidate Gating (Strict)
Only consider streams that meet all of the following:
- English language tag present (e.g., "eng" / "en").
- Stereo (2 channels).
- Duration close to the program (reject extreme mismatches if duration is present).

Tracks lacking language tags are excluded.

### 2) Metadata Signals
Positive keywords (raise confidence):
- commentary, director commentary, cast commentary, commentary by, screenwriter commentary

Negative keywords (immediate exclude):
- audio description, descriptive, dvs, visually impaired, narration for the blind
- isolated score, music only, score, soundtrack
- karaoke, sing-along

### 3) Audio Analysis Signals
Use short sampled windows (e.g., 3 windows x 60-90 seconds at 10%, 50%, 90%).

Required tools:
- ffmpeg: extract PCM audio per stream for analysis windows.
- fpcalc (Chromaprint): fingerprint similarity vs primary.
- WebRTC VAD (cgo): speech activity per window.

Computed metrics per candidate:
- speech_ratio: fraction of frames flagged as speech.
- speech_overlap_with_primary: speech frames that overlap primary speech.
- speech_in_primary_silence_ratio: speech frames that overlap primary silence.
- fingerprint_similarity: similarity to the primary track fingerprints.

### 4) Classification Rules (Conservative)
- Duplicate/downmix (exclude):
  - fingerprint_similarity is extremely high AND
  - speech_ratio is near the primary track AND
  - loudness profile (optional) is similar.

- Audio description (exclude):
  - speech_in_primary_silence_ratio is high, AND
  - fingerprint_similarity is not low enough to be commentary-only.

- Music-only (exclude):
  - speech_ratio below low threshold (e.g., < 0.10), OR
  - title indicates isolated score/music.

- Commentary-only (include):
  - fingerprint_similarity is low, AND
  - speech_ratio is high, AND
  - metadata does not indicate AD/music.

- Mixed commentary (include):
  - fingerprint_similarity is not extreme-high AND
  - speech_overlap_with_primary is high (talking over program audio), AND/OR
  - speech_ratio is meaningfully higher than primary.

Ambiguous outcomes are excluded by default (no REVIEW).

## Primary + Commentary Selection
Audio selection output must include:
- Primary track (same rules as today).
- All commentary tracks that pass the classifier.

Ordering:
- Primary track first.
- Commentary tracks next (keep relative order).

Disposition:
- Primary set as default.
- Commentary tracks set to "none".

## Encoding Flow Implications
Goal: keep primary + commentary tracks in the final encoded file.

Plan:
1. Use Drapto as-is; it already maps and transcodes every audio stream in the input MKV.
2. Ensure the working copy handed to Drapto includes primary + detected commentary tracks.
3. Keep primary as default; commentary tracks as non-default dispositions.

## Dependency Management (Repo + Runtime)
All required dependencies must be documented in `README.md`, surfaced by `spindle status`, and enforced at startup.

Requirements:
- Add new dependencies (fpcalc/Chromaprint, WebRTC VAD) to `README.md` alongside existing FFmpeg/MakeMKV/Drapto requirements.
- `spindle status` must report dependency presence/absence for all external tools (not just commentary detection).
- `spindle start` must run the same dependency check and refuse to begin processing if any required dependency is missing.
- The dependency check logic should be shared so status and startup stay consistent.

Checklist: README dependency list
- ffmpeg (with libsvtav1 + libopus)
- ffprobe
- makemkvcon
- drapto
- mediainfo (if already required by Drapto; confirm current README expectations)
- fpcalc (Chromaprint CLI)
- WebRTC VAD runtime/build deps (document the exact system packages required for the chosen cgo wrapper)

Target OS
- Ubuntu 24.04 is the supported environment; README install steps and dependency checks should assume Ubuntu 24.04 package names and paths.
- Chosen WebRTC VAD wrapper: `github.com/visvasity/webrtcvad` (fork of `maxhawkins/go-webrtcvad` with go.mod; embeds WebRTC VAD C code, no external webrtc install).
- Ubuntu 24.04 package list (mirror this in `README.md` and dependency checks):
  - `build-essential` (gcc toolchain required for cgo builds)
  - `libchromaprint-tools` (provides `fpcalc`)

Checklist: `spindle status` output (format + content)
- Section header: “Dependencies”
- For each dependency: name, resolved path/version when available, status (OK/MISSING)
- Include all external tools above, not just commentary-specific tools
- Summarize missing items at the end and point to README for install steps

README phrasing (Ubuntu 24.04)
```
## Dependencies (Ubuntu 24.04)

# System packages
sudo apt update
sudo apt install -y build-essential ffmpeg mediainfo libchromaprint-tools

# External binaries (installed separately)
# - MakeMKV (provides makemkvcon)
# - Drapto (spindle's encoder)

Spindle requires the following tools to be present in PATH:
- ffmpeg, ffprobe
- makemkvcon
- drapto
- mediainfo
- fpcalc (Chromaprint)
```

README dependency table (example)
```
| Dependency | Purpose | Ubuntu 24.04 package / install | Binary | Required for |
| --- | --- | --- | --- | --- |
| FFmpeg | Transcoding + audio analysis | `apt install ffmpeg` | `ffmpeg` | ripping/encoding |
| FFprobe | Media inspection | `apt install ffmpeg` | `ffprobe` | ripping/encoding |
| MakeMKV | Disc ripping | MakeMKV installer | `makemkvcon` | ripping |
| Drapto | AV1 encoding | Drapto binary | `drapto` | encoding |
| MediaInfo | HDR/metadata analysis | `apt install mediainfo` | `mediainfo` | encoding |
| Chromaprint | Audio fingerprinting | `apt install libchromaprint-tools` | `fpcalc` | commentary detection |
| WebRTC VAD | Speech activity | Go module + `build-essential` | (linked via cgo) | commentary detection |
```

Proposed `spindle status` output snippet
```
Dependencies
  ffmpeg           OK  /usr/bin/ffmpeg (6.x)
  ffprobe          OK  /usr/bin/ffprobe (6.x)
  makemkvcon       OK  /usr/bin/makemkvcon (1.17.x)
  drapto           OK  /usr/local/bin/drapto (0.x)
  mediainfo        OK  /usr/bin/mediainfo (24.x)
  fpcalc           OK  /usr/bin/fpcalc (1.x)
  webrtcvad (cgo)  OK  build toolchain available

Missing dependencies: none
```

## Configuration Additions
Add a dedicated config section to control the detector and thresholds. Defaults should be conservative.

Proposed config:
- commentary_detection.enabled = true
- commentary_detection.languages = ["en"] (strict)
- commentary_detection.channels = 2
- commentary_detection.sample_windows = 3
- commentary_detection.window_seconds = 90
- commentary_detection.fingerprint_similarity_duplicate = 0.98
- commentary_detection.speech_ratio_min_commentary = 0.25
- commentary_detection.speech_ratio_max_music = 0.10
- commentary_detection.speech_overlap_primary_min = 0.60
- commentary_detection.speech_in_silence_max = 0.40

(Thresholds to be tuned on real discs; retain strict defaults to avoid false positives.)

## Logging & Observability
- Log per-track classification decisions with reasons and key metrics.
- Add a summary log: primary label + commentary count + removed indices.
- Consider a debug flag to keep analysis artifacts in staging for inspection.

## Test Plan

### Unit Tests
- `internal/media/audio` selection tests:
  - primary selection unchanged.
  - commentary tracks included/excluded based on synthetic features.
- classifier tests for keyword rules and boundary thresholds.

### Integration Tests
- Use a small set of fixture feature JSONs (no raw audio in repo):
  - commentary-only
  - mixed commentary
  - audio description
  - duplicate/downmix
  - music-only

### Calibration Dataset
- Re-rip Sound of Music 4K with the new cache/working-copy flow so the raw rip retains commentary tracks.
- Extract track metadata from MakeMKV/ffprobe for ground truth labels.
- Use this disc to tune thresholds (no REVIEW allowed).

## Implementation Steps (Proposed Order)
1. Change ripping flow to keep rip cache immutable and introduce a working copy in staging.
2. Introduce commentary detection module with:
   - metadata parsing
   - VAD analysis
   - chromaprint fingerprint comparison
3. Update audio selection to keep primary + commentary tracks, with stable ordering.
4. Integrate selection into remux flow (working copy only).
5. Ensure encoding preserves commentary tracks (Drapto already encodes all audio streams).
6. Add config entries, logging, and docs updates.
7. Add tests and minimal fixtures; tune thresholds on the Sound of Music disc.

## Dependencies
- Required: `fpcalc` (Chromaprint CLI) available in PATH (`libchromaprint-tools` on Ubuntu 24.04).
- Required: WebRTC VAD via cgo Go wrapper (`github.com/visvasity/webrtcvad`); requires gcc toolchain (`build-essential` on Ubuntu 24.04).
- Existing: ffmpeg/ffprobe.

## Open Validation Items
- Confirm ffprobe stream duration availability; handle missing durations gracefully.

## Gaps to Address / Clarifications
- Fingerprint comparison must normalize audio first (e.g., downmix + resample to mono PCM) before running `fpcalc`, so thresholds are stable across 7.1/5.1/stereo sources.
- Speech-overlap metrics need to account for stream `start_time` / delay offsets; align by container time and tolerate drift.
- Define explicit duration tolerance (e.g., within ±2% or ±120s) and the fallback behavior when duration is missing.
- Missing dependency behavior must be fail-closed: if `fpcalc` or VAD is unavailable, drop commentary candidates and log why (no ambiguous acceptance).
- Remux must preserve stream metadata (language/title) and dispositions: primary default, commentary non-default.
