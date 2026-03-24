# System Design: Pipeline Stages

All seven pipeline stages: identification, ripping, episode identification,
encoding, audio analysis, subtitle generation, and organization.

See [DESIGN_INDEX.md](DESIGN_INDEX.md) for the complete document map.

---

## 1. Stage: Identification

### 1.1 MakeMKV Scan

1. Run `makemkvcon info` on configured optical drive.
2. Parse robot-format output for title list and stream details.
3. Run `bd_info` (optional) for enhanced metadata: disc name, volume identifier,
   disc ID, year, studio.
4. Apply `disc_settle_delay` between disc access commands.

#### 1.1.1 MakeMKV Robot Format

**TINFO lines** (title attributes): `TINFO:titleID,attrID,reserved,"value"`

| Attr ID | Purpose |
|---------|---------|
| 2 | Title name |
| 8 | Chapter count |
| 9 | Duration (`HH:MM:SS` -> total seconds) |
| 16 | Playlist (e.g., `00800.mpls`) |
| 25 | Segment count |
| 26 | Segment map (comma-separated) |

**SINFO lines** (stream attributes): `SINFO:titleID,streamID,attrID,reserved,"value"`

| Attr ID | Purpose |
|---------|---------|
| 1 | Type: contains "video"/"audio"/"sub"/"text"/"data" |
| 2 | Track name (overridden by 30) |
| 3, 28 | Language code (alt field for different MakeMKV versions) |
| 4, 29 | Language name (alt field) |
| 5 | Codec ID |
| 6 | Codec short name |
| 7 | Codec long description |
| 13 | Bitrate |
| 14 | Channel count (numeric) |
| 30 | Track name override |
| 40 | Channel layout |

**Track type classification** from attribute 1: `video`, `audio`, `subtitle`
(matches "sub" or "text"), `data`, `unknown`.

#### 1.1.2 Track Structure

```go
Track {
    StreamID      int
    Order         int
    Type          TrackType       // video/audio/subtitle/data/unknown
    CodecID       string
    CodecShort    string
    CodecLong     string
    Language      string          // ISO-639-2, lowercase
    LanguageName  string
    Name          string
    ChannelCount  int
    ChannelLayout string
    BitRate       string
    Attributes    map[int]string  // raw MakeMKV attr ID -> value
}
```

Methods: `IsAudio()` (Type == audio), `IsForced()` (subtitle with "(forced only)"
in Name, case-insensitive).

#### 1.1.3 BDInfo Output Parsing

`bd_info` produces key-value output with colon separators.

| Field Name | Parsed To |
|------------|-----------|
| "Disc ID" | DiscID (uppercased) |
| "Volume Identifier" | VolumeIdentifier |
| "Disc Title" | DiscName |
| "BluRay detected" | IsBluRay (yes/no) |
| "AACS detected" | HasAACS (yes/no) |
| "provider data" | Provider -> Studio (via mapping) |

**Year extraction**: regex `\b(19|20)\d{2}\b` applied to DiscName first, then
VolumeIdentifier.

**Studio mapping** from provider string (case-insensitive prefix match):
sony -> Sony Pictures, warner -> Warner Bros, universal -> Universal Pictures,
disney -> Walt Disney Pictures, paramount -> Paramount Pictures, mgm -> MGM,
fox -> 20th Century Fox, lionsgate -> Lionsgate. Fallback: use provider if > 3
chars.

**Fallback chain**: DiscName <- VolumeIdentifier; Year <- DiscName/VolumeIdentifier.

### 1.2 Media Kind Inference

Heuristics to determine if disc is movie or TV:

- **TV indicators**: Season/episode patterns in disc title or label (e.g.,
  "S01", "Season 1", "DISC_1"), multiple titles of similar duration.
- **Movie indicators**: Single long title, movie-like naming.
- Returns: `movie`, `tv`, or `unknown` with a reason string.

### 1.3 Season/Disc Extraction

- Extract season number from disc title patterns: "S01", "Season 1", "SEASON_1".
- Extract disc number from disc title, label, or volume identifier.
- Sources checked in priority order: disc title, disc label, bd_info name.

### 1.4 KeyDB / Disc ID Cache

**Disc ID Cache** (fast path):
1. If disc ID cache enabled and disc has a BD info disc ID:
2. Look up disc ID in JSON cache file.
3. If found: skip KeyDB and TMDB search, use cached TMDB ID, title, media type.
4. After successful TMDB identification: write entry to disc ID cache.

**Disc ID cache storage**:
- Single JSON file at `disc_id_cache_path` (configured). Non-functional when
  path is empty (all operations become no-ops).
- `Entry` fields: `disc_id`, `tmdb_id`, `media_type` ("movie"/"tv"), `title`,
  `edition`, `season_number`, `year`, `cached_at`,
  `has_forced_subtitle_track`.
- Thread-safe via `sync.RWMutex`.
- Atomic persistence: write to `.tmp` file, then `os.Rename()`.
- JSON serialized as an array sorted by `cached_at` descending (newest first)
  for deterministic output.
- File created lazily on first `Store()` call.
- Load errors are non-fatal: cache starts empty, logs warning.
- `List()`: Return all entries sorted by `cached_at` descending.
- `Remove(fingerprint)`: Delete a single entry by disc fingerprint.
- `Clear()`: Remove all entries.

**KeyDB Lookup**:
1. If KeyDB catalog available and disc ID present:
2. Parse KeyDB.cfg for disc ID entry.
3. If found: use authoritative title from KeyDB (overrides disc label).
4. Download KeyDB if not present locally (with timeout).

### 1.5 Title Determination Priority

1. KeyDB title (most authoritative)
2. BD info disc name
3. MakeMKV first title name
4. Disc label from lsblk
5. Default: "Unknown Disc"

### 1.6 TMDB Search

1. Extract year from resolved title via trailing year pattern (e.g., "Munich (2005)"
   -> title "Munich", year 2005). Year extraction priority: BDInfo year, then
   resolved title year, then item disc title year. Log year source as
   `decision_type: "year_source"`.
2. Clean title string for search (remove year, disc metadata, normalize).
3. Search TMDB with cleaned title.
4. If movie hint: search `/search/movie` first, fall back to `/search/multi`.
5. If TV hint: search `/search/tv` first, fall back to `/search/multi`.
6. Score results using title similarity, vote count, year proximity.
7. For exact title matches: require minimum 5 votes (hardcoded threshold).
8. Best match above confidence threshold becomes the identification.

### 1.7 TMDB Confidence Scoring

`selectBestResult()` ranks TMDB search results:

**Score formula:** `score = match + (voteAverage / 10.0) + (voteCount / 1000.0)`
where `match = 1.0` if query appears in title (case-insensitive), else `0.0`.

**Acceptance paths:**

- **Exact match** (title equals query after normalization): requires
  `voteAverage >= 2.0` AND `voteCount >= 5`.
- **Non-exact match**: requires `voteAverage >= 3.0` AND
  `score >= 1.3 + (voteCount / 1000.0)`.

**Preference rule:** An exact match meeting its thresholds is preferred over a
higher-scoring non-exact result.

**Year-aware exact matching:** When a year is available from the extraction in
step 1.6, a result is considered an exact match only if the normalized title
matches AND the result's release year matches the extracted year. This
disambiguates same-title films from different years (e.g., Munich 2005 vs
Munich 1972).

### 1.8 Edition Detection

For movies, detect alternate editions (Director's Cut, Extended Edition, etc.):

1. **Regex patterns**: Check disc title for known edition keywords.
2. **Ambiguous markers**: If extra content in title beyond TMDB title, check with LLM.
3. **LLM classification**: Ask LLM if disc title represents a special edition
   (confidence threshold: 0.8). See DESIGN_LLM_PROMPTS.md Section 1 for the
   exact prompt and response schema.
4. **Label extraction**: Extract edition label from difference between disc title
   and TMDB title.

### 1.9 RipSpec Assembly

After identification:
1. Build title specs from MakeMKV scan results (filtered by `min_title_length`).
2. For TV: create episode specs with placeholder keys (e.g., `s01_001`).
3. Set metadata fields from TMDB response.
4. Set `metadata.disc_source` (`bluray`, `dvd`, `unknown` --
   determined from disc detection via lsblk probe).
5. Set attributes: forced subtitle track detection.
6. Store serialized envelope in `rip_spec_data`.

### 1.10 Additional Behaviors

- **Disc ID cache fast-path**: On cache hit, bypasses both TMDB search and KeyDB
  lookup entirely. Logged with `decision_type: "disc_id_cache"`.
- **Stale staging cleanup**: At stage start, removes staging directories older than
  **48 hours** via `staging.CleanStale()`.
- **Year enrichment**: Year extracted from multiple sources in priority order:
  (1) BDInfo.Year, (2) resolved title trailing year, (3) item disc title trailing
  year. Used for TMDB search refinement and year-aware exact matching. Logged with
  `decision_type: "year_source"`. Runtime from `Titles[0].Duration / 60` (seconds
  to minutes) for search refinement.
- **Title determination decision logging**: All title source changes logged with
  `decision_type: "title_source"`, `decision_result: "updated"`, and source-specific
  `decision_reason` (e.g., `"keydb_contains_authoritative_title_for_disc_id"`).

### 1.11 Failure Modes

- **No disc detected**: Skip (disc may have been removed).
- **Fingerprint computation fails**: Notify error, do not queue.
- **Duplicate fingerprint in workflow**: Return existing item, do not create new.
- **TMDB search fails or returns no results**: Build fallback metadata with
  fallback title, generate placeholder episodes, route to review. Does not fail
  the item.
- **MakeMKV scan timeout**: Fail item.

---

## 2. Stage: Ripping

### 2.1 Title Selection

- Parse RipSpec envelope from item.
- For movies: select primary title (longest duration, filtering out titles shorter
  than `min_title_length`).
- For TV: rip each episode's mapped title ID.

### 2.2 MakeMKV Execution

- Run `makemkvcon mkv` with:
  - Source: configured optical drive
  - Title: selected title index
  - Output: staging directory
  - Timeout: `rip_timeout` seconds
- Parse progress output: `PRGV:current,total,max` lines for percentage tracking.
- Parse message output: `MSG:code,flags,count,message,...` for status updates.

### 2.3 Progress Streaming

- MakeMKV outputs progress lines to stdout.
- Parser extracts percentage from `PRGV` lines.
- Progress updates saved to queue item at regular intervals.

### 2.4 Rip Cache

When `rip_cache.enabled`:
1. **Before ripping**: Check cache for matching fingerprint + title hash.
   - **Cache hit**: Validate cached files exist and are readable. Record cached
     paths as ripped assets in the RipSpec envelope without re-ripping.
   - **Cache miss/invalid**: Proceed with normal ripping.
2. **After ripping**: Copy ripped files to cache directory. Prune cache to stay
   within constraints.
3. **Cache decisions**: `hit`, `miss`, `invalid`, `error`, `incomplete`.

**Cache path resolution** (in order):
1. Disc fingerprint (if non-empty)
2. `queue-{ID}` (if item has an ID)
3. Sanitized disc title
4. `queue-temp` (ultimate fallback)

Path is sanitized via `SanitizePathSegment()`.

**Method contracts:**

| Method | Purpose |
|--------|---------|
| `Store()` | Copy a rip directory into the cache. Used after ripping to a staging area. Triggers pruning. |
| `Register()` | Mark an already-in-place directory as a cache entry (no copy). Used after `cache rip` where output is written directly to the cache. Triggers pruning. |
| `List()` | Return all cache entries sorted newest first (by modification time). |
| `Remove(fingerprint)` | Delete a single cache entry by fingerprint. |
| `Clear()` | Remove all cache entries. |

**Pruning algorithm** (`prune()`):
- Triggered after every `Store()` or `Register()` call.
- While total cache size > `max_gib`: remove the oldest entry (by modification
  time). The just-stored entry is never removed.

**Restore flow** (`Restore()`):
- Used when re-encoding after a failure: if the target directory is empty/missing
  and the cache has a matching entry, copies the cached rip back.
- Touches modification time on both source and destination after restore.
- Returns `(true, nil)` when a restore occurred, `(false, nil)` when no cache
  entry available or target already exists.

**Metadata persistence**:
- `WriteMetadata()` stores a `spindle.cache.json` sidecar alongside the cached
  rip directory via atomic temp-file + rename.
- `EntryMetadata` captures: version (currently 1), disc title, fingerprint,
  rip spec data, metadata JSON, review flags.
- `LoadMetadata()` reads and validates the sidecar; rejects mismatched versions.
- Metadata write failure is non-fatal (logged as warning).

### 2.5 Additional Behaviors

- **Drive readiness check**: For `/dev/` device paths, calls `WaitForReady()`
  before ripping to ensure the drive is ready.
- **MakeMKV settings**: `ensureMakeMKVSettings()` configures audio track selection
  before each rip.
- **Progress sampling**: Updates saved to queue at **5-second intervals** to avoid
  SQLite churn.
- **Rip cache incomplete detection** (TV): Checks all expected title IDs have
  ripped files; if any are missing, invalidates the cache entry and re-rips.
- **Episode asset mapping** post-rip: `assignEpisodeAssets()` maps ripped files to
  episodes. On zero matches: fails with validation error. On partial match: warns
  and routes to review.
- **Rip cache registration** post-rip: `Register()` + `WriteMetadata()`. Metadata
  write failure is non-fatal (logs warning).
- **Rip timeout distinction**: Timeout errors produce `ErrTimeout` (with hint
  "consider increasing rip_timeout"), distinct from `ErrExternalTool` for other
  MakeMKV failures.
- **Per-rip-target validation**: Visited map deduplicates targets to prevent
  double-ripping the same title.

### 2.6 Disc Monitor Pause

The ripping handler pauses disc monitoring before ripping and resumes it
after completion (or failure). This prevents the disc monitor from polling
the drive during MakeMKV operation. The handler calls `PauseDisc()` /
`ResumeDisc()` directly rather than relying on lifecycle hooks.

---

## 3. Stage: Episode Identification

**Applies to**: TV content only (skipped for movies).

This stage resolves placeholder episode keys (e.g., `s01_001`) to actual episode
numbers (e.g., `s01e03`) using WhisperX transcription compared against
OpenSubtitles references.

See `CONTENT_ID_DESIGN.md` for the complete algorithm specification.

**Inputs**: Ripped files with placeholder episode keys, TMDB ID, season number.

**Outputs**: Updated episode keys in RipSpec envelope with resolved episode
numbers, confidence scores, and match metadata.

### 3.1 Skip Decisions

The stage uses **content-gated** skipping for movies and **config-gated**
skipping for TV:

1. Movie content: skip silently (`reason: "movie_content"`). Movies have no
   episode numbers to resolve.
2. No rip spec data: skip.
3. Invalid rip spec (parse error): skip.
4. No episodes in envelope: skip.
5. TV content with matcher unavailable, configuration unavailable, or
   OpenSubtitles disabled: **flag for review** with reason
   `"episode numbers unresolved; content matching unavailable"` and skip.
   This is a configuration gap, not a content decision -- the user should
   enable OpenSubtitles or manually resolve episode numbers.

**Placeholder key retention**: When the stage skips, episode keys remain as
placeholders (e.g., `s01_001`, `s01_002`). Downstream stages use these
placeholder keys as-is for file naming and asset tracking.

### 3.2 Review Triggers

Four conditions flag an item for review after matching:
1. **Content ID flagged**: `ContentIDNeedsReview` set by matching algorithm.
2. **Low confidence**: Any episode below the low-confidence review threshold (0.70).
3. **Partial resolution**: Some episodes still unresolved after matching.
4. **Non-contiguous sequence**: Resolved episode numbers have gaps (e.g.,
   1, 2, 5, 6 instead of 1, 2, 3, 4).

### 3.3 Progress Phases

| Phase | Label | Percent Range |
|-------|-------|---------------|
| 1/3 | Transcribe | 10-50% |
| 2/3 | References | 50-80% |
| 3/3 | Apply | 80-95% |

Active episode key tracked during progress callback via
`item.ActiveEpisodeKey`. RipSpec persisted to database during Apply phase
callback.

---

## 4. Stage: Encoding

### 4.1 Job Planning

The job planner builds an ordered list of encode jobs from the RipSpec:

1. Parse RipSpec envelope.
2. For movies: single encoding job (ripped file -> encoded file).
3. For TV: one encoding job per episode. Each job maps:
   - **Source**: the episode's ripped asset path (looked up from `AssetRipped`).
   - **Output**: derived from `Episode.OutputBasename` placed in the encoded
     directory.
4. Skip episodes whose encoded asset is already completed (`IsCompleted()`:
   non-empty path and status != "failed"). Failed assets naturally fall
   through because they have an empty path and failed status, so they are
   re-attempted without explicit clearing. Per-episode retry uses
   `ClearFailedAsset()` at the API layer (see API_INTERFACES.md).

### 4.2 Per-Episode Execution Loop

The job runner iterates encode jobs with per-episode failure isolation:

1. **Reset snapshot**: zero the encoding snapshot on the queue item, set
   `ActiveEpisodeKey` to the current episode key, and force-persist via
   `store.UpdateProgress(item)`. This gives API consumers (Flyer) a clean
   transition signal: `encoding: null` + new episode key = "encoding starting."
2. Invoke Drapto to encode `Source` -> `Output`. Progress callbacks update
   the snapshot and persist it on the 2-second throttle (see Section 4.4).
3. **On success**: force-persist the final snapshot (bypasses throttle) so
   completion fields (`encode_duration_seconds`, `size_reduction_percent`,
   etc.) are never lost. Record `AssetStatusDone` on the episode's encoded
   asset, persist RipSpec to the database immediately (not batched at stage
   end). This enables real-time progress visibility via the API.
4. **On failure**: force-persist the snapshot with the `error` field populated.
   Record `AssetStatusFailed` with the error message on the episode's encoded
   asset, persist RipSpec, then **continue** to the next episode. A single
   episode failure does not abort the stage.
5. **Stage outcome**: the stage fails only if ALL episodes fail
   (`len(encodedPaths) == 0`). Partial success proceeds to later stages with
   the successfully encoded episodes.

### 4.3 Drapto/SVT-AV1 Integration

- Drapto is a Go library (not a separate binary).
- Configured with `svt_av1_preset` (0-13, lower = slower + better quality).
- Drapto handles: crop detection, HDR passthrough, audio stream mapping,
  subtitle stream passthrough, chapter preservation.
- Drapto outputs a validation report (codec check, duration check, HDR check,
  audio check, A/V sync check).

### 4.4 Progress Streaming with Encoding Snapshot

**Snapshot lifecycle per encode job:**

```
1. Reset:   item.EncodingSnapshot = zero value
2. Persist: store.UpdateProgress(item)       // force (Flyer sees encoding: null)
3. Encode:  drapto.Encode(...)               // callbacks update snapshot
4. Final:   store.UpdateProgress(item)       // force (completion or error state)
```

The reset at step 1-2 eliminates stale-data windows between episodes. Without
it, Flyer would see episode N's completed snapshot while episode N+1 is
starting up. The force-persist at step 4 ensures completion/error fields are
never lost to throttle timing.

**Progress callback chain** (steps within the Drapto encode):

1. **Apply snapshot**: update the encoding snapshot with current Drapto
   event data. Live fields: `percent`, `fps`, `eta_seconds`, `current_frame`,
   `total_frames`, `substage`. One-time fields set at encoding start:
   `input_file`, `resolution`, `dynamic_range`, `preset`, `quality`, `tune`,
   `encoder`, `audio_codec`, `drapto_preset`, `crop_filter`, `crop_required`,
   `crop_message`. End fields set on completion: `original_size`,
   `encoded_size`, `size_reduction_percent`, `average_speed`,
   `encode_duration_seconds`.
2. **Update estimated size**: if progress >= 10%, read the output file's
   current size and extrapolate:
   `estimatedTotal = currentBytes / (percent / 100)`. Updates
   `current_output_bytes` and `estimated_total_bytes`.
   Below 10% the estimate is too unstable and is skipped.
3. **Throttle DB writes**: persist the snapshot to `encoding_details_json` at
   most once per **2 seconds** (`progressPersistInterval`). This prevents
   database write storms during fast-moving encodes while keeping API consumers
   reasonably current.
4. **Log sampling**: a `progressSampler` with bucket size 5 suppresses
   repetitive progress log lines.

Drapto emits 14 event types. Each is routed to an appropriate log level
(DEBUG for routine progress, INFO for decisions like crop/HDR detection,
WARN/ERROR for failures) and selectively persisted to the snapshot.
See DESIGN_INFRASTRUCTURE.md Section 4.6 for the full snapshot schema.

### 4.5 Output Organization

- Encoded files placed in `{staging_dir}/{fingerprint}/encoded/`.
- For TV: one file per episode named by episode key.
- For movies: single encoded file.
- **Stale output cleanup**: Before encoding each job, the expected output file
  is removed if it already exists. The staging directory is keyed by disc
  fingerprint, so re-inserting a disc reuses the same directory. Without
  cleanup, Drapto refuses to overwrite existing files from a prior run.

### 4.6 Validation

1. **Missing ripped episodes**: Detected at start via `MissingEpisodes(ripped)`.
   Warns and flags for review but does not fail.
2. **Drapto validation enforcement**: Always enforced. Validation failures
   cause the stage to fail with a detailed step-by-step failure report.
3. **Crop ratio validation**: For movies only, log warnings if crop detection
   produced unusual aspect ratios.
4. **Encoding snapshot audio refresh**: Re-probes encoded files after encoding to
   update audio details in the encoding snapshot.

---

## 5. Stage: Audio Analysis

This stage performs commentary detection and audio track refinement, both
operating on encoded files. Commentary detection runs first so that
commentary track indices can be preserved when refinement strips unwanted
tracks.

### 5.1 Commentary Detection Pipeline (Phase 1)

**Operates on encoded files** -- smaller files mean faster WhisperX
transcription. Runs before refinement so commentary tracks are identified
before any tracks are removed.

When `commentary.enabled`:

1. **Candidate filtering**: Examine encoded file's audio tracks using ffprobe.
   - Primary track: first audio track (assumed main content).
   - Candidates: additional audio tracks that are NOT the primary.

2. **Stereo downmix cosine similarity**: For each candidate:
   - Extract audio from primary track and candidate track.
   - Downmix both to stereo WAV.
   - Compute cosine similarity of audio spectral features.
   - If similarity >= `similarity_threshold` (default 0.92): mark as
     "stereo downmix of primary" -- NOT commentary. Exclude from further analysis.

3. **LLM classification**: For remaining candidates:
   - Transcribe candidate audio track with WhisperX (model: `commentary.whisperx_model`).
   - Send transcript to LLM with classification prompt (see
     DESIGN_LLM_PROMPTS.md Section 2 for the exact prompt and response schema).
   - LLM returns: `decision` string, `confidence` float, `reason` string.
   - If confidence >= `confidence_threshold` (default 0.80) and is_commentary:
     mark track as commentary.

**Commentary detection is non-fatal**: If detection fails, a warning is logged
with `event_type: "commentary_detection_failed"` and processing continues.

**Transcription**: Commentary detection uses the shared transcription service
(see DESIGN_INFRASTRUCTURE.md Section 9) to invoke WhisperX. The `whisperxSem`
semaphore is held by the audio analysis stage for the duration of any
transcription work.

### 5.2 Audio Refinement Algorithm (Phase 2)

`RefineAudioTargets()` selects and remuxes audio tracks on encoded files:

1. Deduplicate input paths. For each unique path:
2. FFprobe to count audio streams. If <= 1: skip (return single index).
3. If > 1: call `audio.Select()` for primary track selection.
4. **Primary selection priority** (scored): English language filter (fall back
   to first stream if no English) -> channel count (8ch=1000, 6ch=800,
   4ch=600, 2ch=400) -> lossless codec bonus (+100) -> default flag (+5) ->
   stream order tiebreaker (-0.1 per position).
5. Merge `additionalKeep` indices (e.g., commentary tracks from Phase 1)
   into `KeepIndices`, rebuild `RemovedIndices` excluding them.
6. If stream set changed OR disposition fix needed (`needsDispositionFix`):
   remux via FFmpeg with `-map 0:v` + `-map 0:{idx}` for each kept audio
   index, set first audio as default disposition. Replace original file with
   remuxed output.
7. Validate remuxed output via ffprobe (stream count matches expectations).

Returns `AudioRefinementResult` with `PrimaryAudioDescription` and
`KeptIndices` from the first processed path.

### 5.3 Post-Refinement (Phase 3)

1. **Primary audio selection**: Re-probe encoded file post-refinement, select
   primary via `audio.Select()`, set `PrimaryAudioDescription`.

2. **Commentary disposition**: Identified commentary tracks are marked with
   `"comment"` disposition via `ApplyCommentaryDisposition()`, not removed
   from the file. Track indices are remapped via `RemapCommentaryIndices()`
   to reflect post-refinement stream positions.
   `ValidateCommentaryLabeling()` verifies the disposition was applied
   correctly.

**Episode consistency check**: For TV content (> 1 episode), validates audio
stream counts after commentary handling.

---

## 6. Stage: Subtitle Generation

### 6.1 WhisperX Transcription

1. **HF token lazy validation**: Token checked on first `Generate()` call via
   `sync.Once`; fallback logging deduplicated.
2. **Language merging**: Config default languages (`["en"]`) merged with
   per-request languages via `NormalizeList()`.
3. Extract primary audio track from encoded MKV to WAV.
4. Run WhisperX via `uvx` with configured model, CUDA settings, VAD method.
   Line length controlled by `--max_line_width 42 --max_line_count 2`.
5. WhisperX produces SRT output.
6. **Hallucination filtering**: `filterTranscriptionOutput()` removes WhisperX
   artifacts and repetitive segments.
7. **Zero-segment SRT is fatal**: If filtering produces 0 cues, the stage fails.
8. **Duration fallback**: If `totalSeconds <= 0`, extracts duration from last SRT
   timestamp.

### 6.2 WhisperX Hallucination Filtering

`filterWhisperXOutput()` removes WhisperX artifacts in two passes:

**Pass 1 -- Isolated/repeated removal** (`removeIsolatedHallucinations`):

- **Repeated hallucinations**: 3+ consecutive cues with identical normalized
  text where each inter-cue gap > 10 seconds. All cues in the run removed.
- **Isolated hallucination**: gaps >= 30s before AND after the cue, and
  normalized text matches a known phrase.
- **Music-only**: isolated cues containing only music symbols
  (`\u00B6`, `\u266A`, `\u266B`, `*`) and whitespace.

**Pass 2 -- Trailing sweep** (`sweepTrailingHallucinations`):

- Only runs when `videoSeconds >= 600` (2x the 300s window).
- In the last 300 seconds of the video: removes hallucination phrases and
  music-only cues without requiring isolation (credits section cleanup).

**Known hallucination phrases** (normalized): "thank you", "thank you for
watching", "thanks for watching", "please subscribe", "like and subscribe",
"well be right back", "bye", "bye bye", "see you next time", "see you later".

Cue indices are renumbered sequentially after filtering. Zero surviving cues
causes the stage to fail.

### 6.3 SRT Validation

`ValidateSRTContent()` checks for subtitle quality issues. Returns a list of
issue strings (empty = passed):

| Check | Condition | Issue Key |
|-------|-----------|-----------|
| Empty file | 0 cues | `empty_subtitle_file` |
| Duration mismatch | last cue > video duration + 8s | `duration_mismatch` |
| Sparse subtitles | < 2 cues/minute (videos > 60s) | `sparse_subtitles` |
| Late first cue | first cue > 900s (15 min) | `late_first_cue` |

Duration check is asymmetric: subtitles shorter than video are allowed up to
600s (credits), but subtitles longer than video only tolerate 8s drift.

SRT validation issues flag items for review but do not fail the stage.

### 6.4 OpenSubtitles Forced Subs

When `opensubtitles_enabled` and the disc has a forced subtitle track indicator:
1. Search OpenSubtitles for forced/foreign-parts-only subtitles matching TMDB ID.
2. **SRT cleaning**: Downloaded subtitles are cleaned of ad patterns before use
   (see Section 6.4.1).
3. Store forced subtitle as additional SRT sidecar.

When any gate condition is not met, log a `forced_subtitle_search` skip decision
at INFO with the specific reason: "opensubtitles client unavailable",
"opensubtitles_enabled is false", or "no forced subtitle track on disc".

**OpenSubtitles disabled diagnostics** (`openSubtitlesDisabledReason()`):
Returns granular reason strings: "configuration unavailable",
"opensubtitles_enabled is false", "opensubtitles_api_key not set".

#### 6.4.1 SRT Cleaning

`CleanSRT()` removes advertisement cues and normalizes spacing in downloaded
SRT subtitles. Applied to all OpenSubtitles downloads before use.

**Ad pattern detection** (`blockIsAdvertisement()`): Extracts text lines from
each SRT block (skipping index and timing lines), joins them, and tests against
9 regex patterns:

| Pattern | Matches |
|---------|---------|
| `(?i)opensubtitles` | OpenSubtitles watermarks |
| `(?i)subtitles? by` | Attribution lines |
| `(?i)synced? and corrected` | Sync attribution |
| `(?i)advertise (your\|yours?) product` | OpenSubtitles ad CTA |
| `(?i)http(s)?://` | URLs |
| `(?i)\bwww\.` | Website references |
| `(?i)\bsubscene\b` | Subscene site name |
| `(?i)\byts\b` | YTS site name |
| `(?i)\byify\b` | YIFY site name |

Matching cues are removed entirely. Surviving blocks have trailing whitespace
trimmed per line. Returns `CleanStats` with `RemovedCues` count.

**Plain text extraction**: `PlainTextFromSRT()` strips SRT formatting (index
numbers, timestamps) and returns only dialogue text, one line per cue. Used
by content ID and commentary detection for text analysis.

#### 6.4.2 Forced Subtitle Timing

No alignment is performed. Downloaded forced subtitles are used as-is.
Most OpenSubtitles forced subs are already correctly timed for the source
content. If timing drift becomes a problem in practice, add constant-offset
correction first before considering a full linear transform.

### 6.5 Forced Subtitle Candidate Ranking

`rankForcedSubtitleCandidates()` selects the best forced subtitle file from
OpenSubtitles search results. Content ID reference selection uses its own
logic; see CONTENT_ID_DESIGN.md §5.2.

Since forced subtitle searches are filtered by TMDB ID, the TMDB match is
sufficient for content validation. Title comparison is unnecessary here.

**Filtering**: Exclude garbage sources (CAM, Telesync, Telecine, Screener
in release name).

**Ranking**: Prefer configured language over fallback languages. Within
same language preference, sort by download count descending (most downloaded
= most vetted). Tiebreaker: lowest file ID (deterministic ordering).

### 6.6 SRT Generation

- **Location**: SRT files are written beside the encoded MKV in the staging
  `encoded/` directory. `{base}` is the encoded filename without extension.
- **Naming**: `{base}.en.srt` for primary, `{base}.en.forced.srt` for forced.

Examples:

```
{staging_dir}/{fingerprint}/encoded/
  Movie Title (2024).mkv
  Movie Title (2024).en.srt
  Movie Title (2024).en.forced.srt      # only when forced subs found

{staging_dir}/{fingerprint}/encoded/
  Show Name - S01E01.mkv
  Show Name - S01E01.en.srt
  Show Name - S01E02.mkv
  Show Name - S01E02.en.srt
```

The organizing stage discovers these via base-name prefix matching (Section 7.3)
and moves them alongside the MKV to the library.

### 6.7 MKV Muxing

When `mux_into_mkv` is true:
- Use `mkvmerge` to embed SRT subtitle tracks into the MKV container.
- Primary subtitle marked as non-default track (prevents auto-play in clients).
- Forced subtitle marked as forced track.
- Original encoded file is replaced with muxed version.

### 6.8 Subtitle Context

The subtitle stage reads metadata directly from the envelope's `metadata`
section (title, media type, TMDB ID, year, season, edition). No separate
context struct or cross-stage attribute needed.

### 6.9 Transcript Cache

The shared transcription service (DESIGN_INFRASTRUCTURE.md S9) uses
content-stable cache keys (`disc_fingerprint:episode_key:audio_index`).
Because episode ID (Stage 3) and subtitling (Stage 6) supply the same
content key for the same episode, the subtitle stage gets automatic cache
hits even though the input file changed from the ripped file to the encoded
file. No explicit envelope attribute or cross-stage plumbing is needed.

### 6.10 Resume and Failure Isolation

- **Resume support**: Episodes whose subtitle asset is already completed
  (`IsCompleted()`) are skipped. Failed assets are re-attempted without
  explicit clearing (same predicate as encoding; see Section 4.1).
- **Per-episode failure isolation**: Individual episode subtitle failures are recorded
  with `AssetStatusFailed` + error message. Processing continues for remaining
  episodes. Stage only fails if ALL episodes fail.
- **SRT validation review**: SRT validation issues (e.g., suspicious segment patterns)
  flag the item for review but do not fail the stage.

**Transcription**: Subtitle generation uses the shared transcription service
(see DESIGN_INFRASTRUCTURE.md Section 9) to invoke WhisperX and manage
caching. The `whisperxSem` semaphore is held by the subtitling stage for
the duration of transcription work.

---

## 7. Stage: Organization

### 7.1 Validation

1. Verify encoded file exists and is readable.
2. Check file size >= 5 MB (minimum for valid media file).
3. Run ffprobe validation on encoded file.
4. Cross-validate: check for missing encoded episodes.
5. **Partial file cleanup**: Before copying, check the target library path for
   files from a previous interrupted attempt. If a target file exists but its
   size is less than the source file's size, remove it (likely a partial copy
   from a crash). This ensures idempotent retry of the organization stage.

### 7.2 Library Path Resolution

**Movies**: `{library_dir}/{movies_dir}/{Title} ({Year})/{Title} ({Year}).mkv`
- With edition: `{Title} ({Year}) - Edition Name.mkv`

**Edition format**: Jellyfin treats editions as movie versions using a
` - Label` suffix (space-hyphen-space-label). Examples: `Movie (2024) - Director's Cut.mkv`,
`Movie (2024) - Extended Edition.mkv`. Labels are freeform text. This is distinct
from Plex's `{edition-...}` tag format.

**TV**: `{library_dir}/{tv_dir}/{Show Name}/Season {NN}/{Show Name} - S{NN}E{NN} - {Episode Title}.mkv`

### 7.3 File Operations with Progress

- Copy file from staging to library location.
- Report progress via `progress_bytes_copied` and `progress_total_bytes` fields.
- If `overwrite_existing` is true: remove existing file before copy.
- **Cross-device move fallback**: If `os.Rename()` fails with `EXDEV` (cross-filesystem),
  falls back to `CopyFileVerified()` + source removal.
- **File collision handling**: When not overwriting and target exists, appends
  counter suffix `(1)`, `(2)`, etc.
- **Subtitle sidecar matching**: Finds `.srt` files by base name prefix and moves
  them alongside the main file to the library. Sidecar move is skipped when
  `subtitles.mux_into_mkv` is true (subtitles already embedded in MKV).
- Per-episode asset recorded to RipSpec after each episode.

### 7.4 Partial Episode Organization

When `needs_review` is true but some episodes ARE resolved:
- **Resolved episodes organized normally** to the library.
- **Unresolved episodes routed to review** simultaneously.
- Per-episode failure isolation: each episode processed independently. Failure of
  one episode does not prevent others. Stage fails only if ALL episodes fail.

### 7.5 Resume Support

Skips episodes whose final asset is already completed (`IsCompleted()`).
Failed assets are re-attempted without explicit clearing (same predicate
as encoding; see Section 4.1).

### 7.6 Review Routing

If `needs_review` is true and no episodes are resolved:
- Move encoded files to `{review_dir}/` instead of library.
- **Review filename**: sanitized review reason prefix + 8-char hex fingerprint
  suffix. Up to **10,000** collision attempts with counter suffix.
- Notify via `EventUnidentifiedMedia`.

**Library unavailable**: Terminal condition. Routes to review with specific
reason. Stops processing remaining episodes.

### 7.7 Jellyfin Refresh

After successful organization (if `jellyfin.enabled`):
- **Batch refresh**: Single `POST /Library/Refresh` call after all episodes
  (not per-episode).
- Best-effort: log warning on failure, do not fail the stage.

### 7.8 Edition Filename Validation

`ValidateEditionFilename()` verifies that the edition suffix appears in the
final filename when edition metadata is present. This catches logic bugs in
the metadata-to-filename path.

- Checks that the filename (without extension) ends with ` - Edition Name`
  (space-hyphen-space-label, matching Jellyfin's version naming convention).
- On mismatch: returns `ErrValidation` with details. Logged with
  `event_type: "edition_validation_failed"`.
- On success: logged with `event_type: "edition_validation_passed"`.
- Skipped when edition is empty.

### 7.9 Additional Behaviors

- **Metadata fallback**: When metadata title is empty, falls back to disc title,
  then to encoded file basename. Persists the fallback title to the queue item
  (non-fatal if persistence fails).
- **Cross-stage missing asset checks**: Warns about missing encoded/subtitled
  assets, flags for review.

### 7.10 Staging Cleanup

After successful organization (library or review routing), the staging
directory (`{staging_dir}/{fingerprint}/`) is removed via `os.RemoveAll`.
Cleanup failures are logged as warnings (non-fatal) — disk space reclamation
is best-effort. The existing `staging.CleanStale()` in the identification
stage remains as a safety net for failed or abandoned items.

---

## 8. Decision Type Catalog

All decision logging uses `decision_type`, `decision_result`, `decision_reason`
attributes (see DESIGN_INFRASTRUCTURE.md SS1.4). This section catalogs which
decision types each stage produces. Constants are defined in
`internal/logs/decision.go`.

### 8.1 Identification Stage

| decision_type | Possible Results | Context |
|---------------|-----------------|---------|
| `bdinfo_availability` | disc source value, `unavailable` | Disc type classification |
| `bdinfo_scan` | `completed` | BDInfo metadata extraction |
| `title_source` | `updated` | Disc title determination |
| `title_resolution` | source used | Title resolution from multiple sources |
| `year_source` | `bdinfo`, `title`, `disc_title` | Year extraction priority |
| `tmdb_match` | display title, `movie` | TMDB search match acceptance |
| `tmdb_match_preference` | result | Multi-result preference selection |
| `tmdb_search` | result | TMDB search execution |
| `edition_detection` | edition label, reason | LLM edition classification |
| `forced_subtitle_detection` | `present`, `absent` | Disc forced subtitle check |
| `episode_placeholders` | episode count | Placeholder episode creation |
| `disc_id_cache` | `hit`, `miss` | Disc ID cache lookup |
| `config_load` | `explicit_path`, `search_path`, `defaults_only` | Config source selection |

### 8.2 Ripping Stage

| decision_type | Possible Results | Context |
|---------------|-----------------|---------|
| `rip_cache` | `restored` | Rip cache restoration |
| `disc_monitor_control` | `paused`, `resumed` | Disc monitor pause during rip |
| `track_select` | `skipped`, `candidate`, `selected` | Title duration filtering |
| `title_selection` | title summary | Movie/TV title selection |
| `title_rip` | `completed` | Individual title rip outcome |
| `file_discovery` | `not_found` | Post-rip file detection |
| `asset_mapping` | `title_file_map`, `directory_scan` | Ripped file-to-episode mapping |

### 8.3 Episode Identification Stage

| decision_type | Possible Results | Context |
|---------------|-----------------|---------|
| `episode_id_skip` | `skipped` | Episode ID stage skip |
| `episode_match` | match result | Episode-to-reference matching |
| `contentid_candidates` | candidate count | Content ID candidate selection |
| `contentid_matches` | match results | Final episode matching |
| `opensubtitles_reference_search` | result | Reference subtitle search |
| `reference_download` | result | Reference subtitle download |
| `reference_search` | result | Reference search execution |

### 8.4 Encoding Stage

| decision_type | Possible Results | Context |
|---------------|-----------------|---------|
| `encoding_plan` | job count | Encoding job plan |
| `encoding_config` | preset info | SVT-AV1 configuration |
| `encode_resume` | `skipped` | Stale encode file cleanup |
| `encode_cleanup` | `removed` | Pre-encode file cleanup |
| `file_probe` | `success` | FFprobe input inspection |
| `crop_detection` | crop result | Crop filter selection |
| `encoding_validation` | result | Post-encode validation |
| `validation_failure_route` | `flagged_for_review` | Validation failure routing |

### 8.5 Audio Analysis Stage

| decision_type | Possible Results | Context |
|---------------|-----------------|---------|
| `audio_selection` | `selected`, `fallback_non_english` | Primary audio track selection |
| `audio_refinement` | `skipped`, `remuxed` | Audio track refinement decision |
| `audio_remux` | `started`, `completed` | FFmpeg remux execution |
| `commentary_classification` | `skipped`, `commentary`, `not_commentary` | LLM commentary detection |
| `commentary_remapping` | `remapped_N` | Post-refinement index remapping |
| `commentary_disposition` | `applied`, `valid` | FFmpeg disposition setting |
| `commentary_stereo_filter` | `excluded` | Stereo downmix removal |

### 8.6 Subtitle Stage

| decision_type | Possible Results | Context |
|---------------|-----------------|---------|
| `subtitle_skip` | `skipped` | Subtitle stage skip (disabled) |
| `subtitle_resume` | `skipped` | Subtitle resume from prior run |
| `transcription_asset` | asset path | Audio extraction target |
| `transcription_cache` | `hit`, `miss` | Transcription cache lookup |
| `hallucination_filter` | `filtered` | WhisperX hallucination removal |
| `srt_validation` | issue summary | SRT quality validation |
| `subtitle_mux` | `skipped` | MKV muxing decision |
| `forced_subtitle_search` | `skipped` | Forced subtitle search gate |
| `forced_subtitle_ranking` | `selected` | Forced subtitle best pick |
| `subtitle_rank` | `selected`, `candidate` | Per-candidate ranking |
| `forced_subtitle` | `none_available`, `downloaded` | Forced subtitle outcome |
| `sidecar_subtitle_copy` | `skipped` | Sidecar SRT copy decision |

### 8.7 Organization Stage

| decision_type | Possible Results | Context |
|---------------|-----------------|---------|
| `organize_route` | `review` | Review routing decision |
| `source_stage_selection` | stage name | Source asset selection |
| `partial_cleanup` | `removed` | Partial file cleanup |
| `organize_skip` | `skipped` | File existence skip |
| `staging_cleanup` | `preserved`, `removed` | Staging directory cleanup |

### 8.8 Infrastructure

| decision_type | Possible Results | Context |
|---------------|-----------------|---------|
| `stage_execution` | `started`, `completed` | Stage lifecycle |
| `disc_enqueue` | `created` | Disc enqueue from monitor |
| `duplicate_detection` | `skipped` | Duplicate disc skip |
| `detect_guard` | `skipped` | Detection guard skip |
| `drive_wait` | `ready` | Drive poll readiness |
| `disc_event_handling` | `skipped` | Paused event skip |
| `disc_monitor_control` | `paused`, `resumed` | Monitor pause/resume |
| `mount_resolution` | `lsblk`, `proc_mounts`, `fallback_path`, `auto_mount` | Mount point resolution |
| `title_refresh` | `updated` | Post-enqueue title refresh |
| `fingerprint_strategy` | `bluray`, `dvd`, `fallback` | Disc fingerprint method |
| `keydb_lookup` | `hit`, `miss` | KeyDB catalog lookup |
| `makemkv_settings` | `current`, `updated` | MakeMKV settings check |
