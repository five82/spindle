---
name: itemaudit
description: Comprehensive audit of Spindle queue items through multi-layer artifact analysis. Use /itemaudit <item_id> to audit a specific queue item or /itemaudit for daemon-level issues.
user-invocable: true
argument-hint: [item_id]
---

# Spindle Item Audit Skill

Comprehensive audit of Spindle queue items through multi-layer artifact analysis.

## Usage

`/itemaudit <item_id>` - Audit a specific queue item
`/itemaudit` - Audit daemon-level issues

## Philosophy

The goal is to **uncover problems that automated code does not detect**. Quick log scans saying "no warnings, no errors" are insufficient. This skill performs deep, manual analysis of all available artifacts to find anomalies.

## Audit Procedure

### Phase 1: Gather Context

1. **Check daemon status**: `spindle status`
2. **Get item info** (if item specified): `spindle queue show <id>`
3. **Determine processing stage**: The item's status determines which analyses are applicable
4. **Locate log files**:
   - Debug logs (preferred): `log_dir/debug/items/<item>.log`
   - Normal logs (fallback): `log_dir/items/<item>.log`
   - Debug logs persist from previous audit runs even if daemon restarted in normal mode

### Stage Gating

After gathering context, determine two things:
1. The **furthest completed stage** — only run phases that have artifacts to analyze
2. The **disc source type** — determines whether external validation is applicable

#### Determining Disc Source Type

Disc type is not stored in the queue database. Infer it from the debug logs:
- `is_blu_ray: true` in `bd_info details` entry → **Blu-ray** (or 4K Blu-ray)
- DVD fingerprinting uses `VIDEO_TS/*.ifo` files → **DVD**
- If neither is clear, check `disc_type` fields in early log entries

#### Stage-Phase Matrix

| Item Status | Furthest Stage | Applicable Phases |
|-------------|----------------|-------------------|
| `PENDING` / `IDENTIFYING` | None / Scanning | Phase 2 (logs only) |
| `IDENTIFIED` / Failed during `RIPPING` | Identification complete | Phase 2 + Phase 6 (edition detection from logs only, no external validation) |
| `RIPPED` / Failed during `EPISODE_IDENTIFYING` (TV) or `ENCODING` (movie) | Rip complete | Phase 2, 3, 6 |
| `EPISODE_IDENTIFYING` (TV only) | Episode ID in progress | Phase 2, 3, 6 |
| `EPISODE_IDENTIFIED` (TV only) | Episode ID complete | Phase 2, 3, 3b, 6 |
| `ENCODED` / Failed during `AUDIO_ANALYZING` | Encode complete | Phase 2, 3, [3b TV], 4, 5, 6 + external validation (Blu-ray only) |
| `AUDIO_ANALYZED` / Failed during `SUBTITLING` | Audio analysis complete | Phase 2, 3, [3b TV], 4, 5, 6, 8 + external validation (Blu-ray only) |
| `SUBTITLED` / `ORGANIZING` / `COMPLETED` | All stages | All phases + external validation (Blu-ray only) |

**Key principles:**
- External validation (blu-ray.com lookups) is only useful when (a) there are encoded files to cross-reference AND (b) the source is Blu-ray or 4K Blu-ray. **Skip external validation entirely for DVDs** — blu-ray.com reviews don't cover DVD releases, and DVD discs lack the detailed audio/video specs that make cross-referencing valuable.
- For rip failures, the audit focuses on the failure itself — what went wrong, timing patterns, and whether identification was correct.

**For failed items:** Focus the report on diagnosing the failure. Analyze the error, the events leading up to it, and any retry patterns. Do not pad the report with sections that say "N/A - not reached".

### Phase 2: Log Analysis (All Items)

**Go beyond simple error counts.** Analyze logs for:

1. **Decision anomalies**:
   - Low confidence scores on decisions that were accepted anyway
   - Unexpected fallbacks (encoding retries)
   - Decisions that contradict expected behavior for the content type

2. **Timing anomalies**:
   - Stages taking unusually long or short
   - Large gaps between log entries suggesting hangs
   - Repeated retry attempts

3. **Data flow anomalies**:
   - Track counts changing unexpectedly between stages
   - Episode counts not matching expectations
   - File sizes that seem wrong for the content

4. **LLM decision review**:
   - Search for `decision_type=commentary` entries
   - Search for `decision_type=edition_detection` entries (movies only)
   - Evaluate if confidence levels and reasons make sense for the content

5. **TV episode pipeline checks** (TV only):
   - Search for `decision_type=episode_identification` entries — check if stage was skipped and why (valid reasons: `movie_content`, `opensubtitles_disabled`, `content_matcher_unavailable`)
   - Search for `event_type=episode_match_low_confidence` — indicates episodes with `MatchConfidence` below 0.70 (accepted but flagged for review)
   - Check `episode_count` in `event_type=stage_complete` entries for consistency with expected episode count
   - Look for per-episode encoding or subtitle failures: `Asset.Status = "failed"` entries in logs, and whether the stage continued past them (partial success is allowed)
   - Search for `decision_type=contentid_matches` to see final episode-to-reference matching results

### Phase 3: Rip Cache Analysis (Post-Ripping Items)

If item has passed ripping stage, analyze the rip cache:

1. **Locate cache entry**: `{ripcache.dir}/{sanitized-fingerprint}/spindle.cache.json`
2. **Read and parse** the cache metadata file
3. **Verify**:
   - `disc_title` matches expected content
   - `rip_spec_data` contains expected episodes/titles
   - `needs_review` flag status and reason
   - Track counts and durations look reasonable
   - No orphaned or missing assets

4. **Per-episode asset validation** (TV only):
   - Parse `Envelope.Episodes[]` — verify each episode has a corresponding entry in `Assets.Ripped[]` with a matching `EpisodeKey`
   - Check for any ripped assets with `status: "failed"` or missing `Path`
   - Verify `len(Assets.Ripped)` matches `len(Episodes)` — mismatches indicate lost or extra rips
   - Cross-reference `Episode.TitleID` with `Asset.TitleID` — each episode should map to a specific disc title (t00X.mkv)

### Phase 3b: Episode Identification Validation (TV Only, Post-Episode-Identification)

**Skip entirely if item is a movie or has not reached `EPISODE_IDENTIFIED`.** Episode identification uses WhisperX transcription + OpenSubtitles text similarity to confirm or correct initial episode ordering.

1. **Content ID method**: Check `content_id_method` attribute in `Envelope.Attributes`
   - `whisperx_opensubtitles` = full pipeline (WhisperX transcription matched against OpenSubtitles references via cosine similarity)
   - If absent, episode identification was skipped — check logs for skip reason (`decision_type=episode_identification`, `decision_result=skipped`)

2. **Episode manifest review**: Parse `Envelope.Episodes[]` and check `MatchConfidence` per episode:
   - **CRITICAL** (< 0.70): Episode ordering likely wrong. The stage flags `NeedsReview` at this threshold. Check if item has `NeedsReview=true` with a review reason mentioning "low episode match confidence"
   - **WARNING** (0.70-0.80): Marginal confidence — verify by spot-checking episode titles against durations
   - **OK** (> 0.80): High confidence match
   - **Zero** (0.0): Episode was not re-identified (kept initial heuristic assignment). This is normal when episode identification was skipped

3. **`content_id_matches` attribute validation**: Check `Envelope.Attributes["content_id_matches"]` for the match details:
   - Each entry has `episode_key`, `matched_episode`, `score`, `subtitle_file_id`, `subtitle_language`
   - Verify all episodes have a match entry (missing entries = unmatched episodes)
   - Check that `matched_episode` numbers form a reasonable sequence (e.g., consecutive episodes from one disc)
   - The minimum accepted similarity score is 0.58 — scores near this floor warrant scrutiny

4. **Episode sequence continuity**: Check that episode numbers in `Episodes[]` are sequential or at least form a plausible disc block (e.g., episodes 5-8 of a season). Gaps or duplicates indicate matching errors

5. **`episodes_synchronized` flag**: Check `Envelope.Attributes["episodes_synchronized"]` — should be `true` after successful episode identification. If `false` or absent after the stage completed, the match results were not applied

### Phase 4: Encoded File Analysis (Post-Encoding Items)

If item has passed encoding stage:

**For movies** (single encoded file) or **per-episode for TV** (iterate `Assets.Encoded[]`):

1. **Run ffprobe** on each encoded file:
   ```bash
   ffprobe -v error -show_format -show_streams -of json "<encoded_file>"
   ```

2. **Verify video stream**:
   - Resolution matches expected (SD/HD/4K)
   - Codec is AV1 (av1/libaom-av1/libsvtav1)
   - Duration matches source within tolerance (~1-2 seconds)
   - HDR metadata present if expected (color_primaries, transfer_characteristics)

3. **Verify audio streams**:
   - Primary audio is first and has "default" disposition
   - Commentary tracks have "comment" disposition AND title contains "Commentary"
   - Track count matches expected (primary + commentary tracks)
   - No unexpected stereo downmix tracks that should have been excluded

4. **Check commentary labeling** (recent bug area):
   - For each audio stream with `disposition.comment=1`, verify:
     - Stream title exists and contains "Commentary" (case-insensitive)
     - If original title was blank, it should now be exactly "Commentary"
     - If original title existed without "commentary", it should have " (Commentary)" appended
   - Cross-reference with log entries showing commentary detection count

5. **Parse EncodingDetailsJSON** from queue item:
   - Check `Validation.passed` and individual step results
   - Review crop detection: was a crop applied? What were the candidates?
   - Check for warnings or errors in the snapshot

6. **Per-episode asset status** (TV only):
   - Check each entry in `Assets.Encoded[]` for `status: "failed"` — failed episodes have `ErrorMsg` with the failure reason
   - Encoding allows partial success (continues past individual failures), so some episodes may be encoded while others failed
   - Verify `len(Assets.Encoded)` matches `len(Episodes)` — missing entries indicate episodes that were never attempted

7. **Cross-episode consistency** (TV only):
   - All episodes from the same disc should share: same resolution, same codec, same audio track count
   - Flag any episode with different resolution or codec as anomalous
   - Duration spread: episodes from the same season typically have similar runtimes (within ~5 minutes). Outliers may indicate wrong episode assignment

### Phase 5: Crop Detection Validation (Post-Encoding Only)

**Skip entirely if item has not passed encoding.** No encoded file = nothing to validate.

For encoded content, validate crop detection:

1. **Extract crop info** from EncodingDetailsJSON:
   - `Crop.required` - was cropping needed?
   - `Crop.crop` - the applied crop filter (e.g., "crop=1920:800:0:140")
   - `Crop.message` - detection summary

2. **Calculate actual aspect ratio**:
   - `(video_width - left_crop - right_crop) / (video_height - top_crop - bottom_crop)`
   - Common ratios: 2.39:1/2.40:1 (scope), 1.85:1, 1.78:1 (16:9), 2.00:1 (IMAX)
   - Verify the ratio looks reasonable for the content

3. **External cross-reference (Blu-ray/4K Blu-ray only, skip for DVDs)**:
   - Look up disc review on blu-ray.com
   - Search: `site:blu-ray.com "<title>" review` (use identified title from metadata)
   - Find the "Video" section mentioning aspect ratio
   - Flag if our crop differs significantly from the review's stated ratio

4. **Check for IMAX/variable aspect ratio issues** (Blu-ray only):
   - If crop detection shows "multiple ratios" or low top-candidate percentage
   - Some films have IMAX sequences with different ratios
   - This may be acceptable or may indicate detection issues

5. **TV episode crop consistency** (TV only):
   - All episodes from the same disc share source properties — crop should be identical or very similar across episodes
   - Spot-check one or two episodes rather than performing full crop validation on every episode
   - If one episode has a different crop than others, flag as anomalous (likely a detection issue, not intentional)

### Phase 6: Edition Detection Validation (Movies Only)

If item is a movie, validate edition detection. This phase has two tiers:
- **Log review** (always, if identification completed): Check decisions from logs
- **External validation** (only if post-encoding): Cross-reference with blu-ray.com

#### Log Review (post-identification)

1. **From logs**: Search for `decision_type=edition_detection` entries
2. **Expected detection paths**:
   - `decision_reason=regex_pattern_match`: Known edition detected via pattern (Director's Cut, Extended, etc.)
   - `decision_reason=llm_confirmed`: Ambiguous edition confirmed by LLM
   - `decision_reason=llm_rejected`: LLM determined not an edition
   - `decision_reason=llm_not_configured`: Ambiguous title but no LLM available

3. **Verify detection correctness from available data**:
   - If disc title contains obvious edition markers (Director's Cut, Extended, Unrated, IMAX, etc.), an edition should be detected
   - If cache was used, check for `edition from cache` log entry
   - Check if multiple feature-length titles with different durations suggest alternate cuts

4. **Verify edition label**:
   - Check `edition_label` in logs matches the actual edition type
   - Known patterns: Director's Cut, Extended Edition, Unrated, Theatrical, Remastered, Special Edition, Anniversary Edition, Ultimate Edition, Final Cut, Redux, IMAX

#### External Validation (post-encoding only)

5. **Cross-reference with blu-ray.com** to confirm whether disc is actually an alternate edition
6. **Verify filename**:
   - Movie filenames should be: `Title (Year) - Edition.mkv`
   - Check final organized file includes edition suffix when detected
   - Edition should NOT appear in folder name (GetBaseFilename strips it)

### Phase 7: Subtitle Analysis (Post-Subtitling Items)

If item has passed subtitling stage:

Regular subtitles always come from WhisperX transcription (generated from the actual audio).
Forced (foreign-parts-only) subtitles are fetched from OpenSubtitles when enabled, then aligned
against the WhisperX output via text-based matching.

**For movies** (single file) or **per-episode for TV** (iterate `Assets.Subtitled[]`):

1. **Locate subtitles**: Check encoded MKV for embedded tracks (preferred) or sidecar SRT files
2. **For embedded subtitles** (muxed into MKV):
   ```bash
   ffprobe -v error -show_streams -select_streams s -of json "<mkv_file>"
   ```
   - Verify subtitle track exists with correct language
   - Check track has "default" disposition if it's the main subtitle
   - Forced subtitles should have "forced" disposition
   - **Check subtitle labeling** (similar to commentary labeling):
     - Regular subtitles should have title containing language name (e.g., "English")
     - Forced subtitles should have title containing "(Forced)" (e.g., "English (Forced)")

3. **For sidecar SRT files** (legacy):
   ```bash
   head -100 "<srt_file>"  # Check beginning
   tail -50 "<srt_file>"   # Check ending
   wc -l "<srt_file>"      # Total lines (rough cue estimate)
   ```

4. **Content quality checks**:
   - First cue timestamp reasonable (typically within first few minutes)
   - Last cue timestamp near video duration
   - Cue density: minimum ~2 cues per minute expected
   - No obvious encoding issues (mojibake, wrong language)
   - Dialogue makes sense for the content (spot check a few cues)

5. **Duration alignment**:
   - Subtitle end time should be within 10 minutes of video duration
   - Subtitles significantly shorter = missing content
   - Subtitles significantly longer = wrong subtitle file

6. **Forced subtitle search outcome** (when disc has forced subtitle flag):
   - Many discs set the forced subtitle flag even when the content has no foreign language segments. Zero candidates from OpenSubtitles is **common and expected** — classify as **INFO**, not WARNING.
   - Only classify as **WARNING** if candidates were returned but all rejected during ranking (suggests a filtering or scoring problem).
   - **Use your knowledge of the title**: If you know the film/show has significant foreign language dialogue (e.g., Inglourious Basterds, Kill Bill, Narcos), a missing forced subtitle is a real gap — escalate to **WARNING**. If the content is predominantly single-language (e.g., South Park, The Office), INFO is correct.
   - Check `decision_type=forced_subtitle_download` with `decision_result=not_found` — this is the normal "searched and found nothing" outcome.

7. **Edition-aware forced subtitle selection** (movies only, if movie has edition and forced subs fetched):
   - Check logs for `edition=match` or `edition=mismatch` in forced subtitle ranking
   - Selected forced subtitle should match edition when possible (e.g., Director's Cut subtitle for Director's Cut disc)
   - If `edition=mismatch` was accepted, verify no matching edition subtitle was available
   - Note: regular subtitles are always WhisperX-generated, so edition matching only applies to forced subtitles from OpenSubtitles

8. **Per-episode subtitle asset status** (TV only):
   - Check each entry in `Assets.Subtitled[]` for `status: "failed"` — failed episodes have `ErrorMsg`
   - Subtitling allows partial success (continues past individual failures), so some episodes may have subtitles while others failed
   - Verify `SubtitlesMuxed` flag per episode — all completed episodes should have `subtitles_muxed: true`
   - Check `subtitle_generation_results` in `Envelope.Attributes` for per-episode result details

9. **Cross-episode subtitle consistency** (TV only):
   - Cue density should be roughly similar across episodes from the same show (within ~50% of each other)
   - All episodes should have the same subtitle language
   - All episodes should have consistent forced subtitle presence (either all have forced subs or none do)
   - Flag any episode with dramatically different cue density as potential transcription issue

### Phase 8: Commentary Track Validation (Post-Audio-Analysis Only)

**Skip entirely if item has not passed audio analysis.** No audio analysis = no commentary decisions to validate.

1. **From logs**: Find `commentary track classification` and `commentary detection complete` entries
2. **Expected behavior**:
   - 2-channel English tracks that aren't stereo downmixes should be candidates
   - High similarity to primary audio = stereo downmix (excluded)
   - LLM should classify based on content (talking about filmmaking = commentary)

3. **Cross-reference with blu-ray.com** (only if external validation is applicable per stage gating):
   - Check "Audio" section of disc review
   - Note how many commentary tracks the disc actually has
   - Compare against our detection count

4. **Verify in encoded file**:
   - Count audio streams with "comment" disposition
   - Verify all are properly labeled (see Phase 4)

5. **Cross-episode commentary consistency** (TV only):
   - Commentary is a per-disc property, not per-episode — all episodes from the same disc should have the same number of audio streams (primary + commentary)
   - If one episode has different audio stream counts than others, flag as anomalous
   - TV commentary is less common than movie commentary but does exist (e.g., showrunner commentaries on season premieres/finales)

## Log Access Methods

**Via log files** (daemon may be stopped):
```bash
# Debug logs (check first - have richer context)
cat log_dir/debug/items/YYYYMMDDTHHMMSS-<id>-<slug>.log | jq .

# Normal logs (fallback)
cat log_dir/items/YYYYMMDDTHHMMSS-<id>-<slug>.log | jq .
```

**Via API** (daemon running):
```bash
# All logs for item
curl -H "Authorization: Bearer $SPINDLE_API_TOKEN" \
  "http://127.0.0.1:7487/api/logs?item=<ID>&lane=*"

# Decision events only
curl -H "Authorization: Bearer $SPINDLE_API_TOKEN" \
  "http://127.0.0.1:7487/api/logs?item=<ID>&decision_type=*&lane=*"
```

## Problem Pattern Catalog

### Known Patterns to Check

| Pattern | Stage | Evidence | Impact |
|---------|-------|----------|--------|
| Duplicate fingerprint | Identification | `decision_type=duplicate_fingerprint` | Item silently rejected |
| Low TMDB confidence | Identification | Low score in `decision_type=tmdb_confidence` | Wrong title match |
| Episode misidentification | Identification | Low score in `decision_type=episode_match` | Wrong S##E## labels |
| Missed edition detection | Identification | No `edition_detection` decision for disc with edition markers | Edition not in filename |
| Wrong edition label | Identification | `edition_label` doesn't match actual edition type | Incorrect filename/subtitle selection |
| Edition detection LLM failure | Identification | `event_type=edition_llm_failed` | Ambiguous edition not detected |
| Wrong crop detection | Encoding | Aspect ratio mismatch vs blu-ray.com | Black bars or cut content |
| Missing commentary | Audio Analysis | Count mismatch vs blu-ray.com review | Commentary tracks not preserved |
| Unlabeled commentary | Audio Analysis | Missing title/disposition in ffprobe | Jellyfin won't recognize tracks |
| Stereo downmix kept | Audio Analysis | Extra 2ch track that matches primary | Unnecessary audio bloat |
| SRT validation issues | Subtitles | `event_type=srt_validation_issues` | Malformed subtitles |
| Subtitle duration mismatch | Subtitles | Duration delta > 10 minutes | WhisperX timing issue or truncated audio |
| Sparse subtitles | Subtitles | < 2 cues/minute | WhisperX transcription issue or wrong language |
| Forced subtitle not found (INFO) | Subtitles | `decision_result=not_found` with zero OpenSubtitles candidates | Expected — disc flag doesn't guarantee foreign content exists |
| Forced subtitle candidates rejected | Subtitles | OpenSubtitles returned candidates but all rejected during ranking | Filtering or scoring problem |
| Forced subtitle edition mismatch | Subtitles | `edition=mismatch` on forced sub when matching exists | Wrong forced subtitle for alternate cut |
| Subtitles not muxed | Subtitles | Sidecar SRT exists but no embedded tracks | Jellyfin may not auto-load |
| Unlabeled subtitles | Subtitles | Missing or incorrect title in embedded track | Jellyfin won't display track name properly |
| Low episode match confidence | Episode ID | `event_type=episode_match_low_confidence`, scores < 0.70 | Episodes may be mislabeled (wrong S##E##) |
| Heuristic-only episode assignment | Episode ID | `content_id_method` absent, `MatchConfidence` = 0.0 | Episode order based on runtime heuristic only, not verified |
| Episode sequence gaps | Episode ID | Non-sequential episode numbers in `Episodes[]` | Missing episodes or matching error |
| Per-episode rip failure | Ripping | `Assets.Ripped[]` entry with `status: "failed"` | Episode missing from downstream pipeline |
| Per-episode encode failure | Encoding | `Assets.Encoded[]` entry with `status: "failed"` | Episode will not appear in Jellyfin |
| Per-episode subtitle failure | Subtitles | `Assets.Subtitled[]` entry with `status: "failed"` | Episode missing subtitles |
| Cross-episode resolution mismatch | Encoding | Different resolutions in ffprobe across episodes | Inconsistent quality in Jellyfin |
| Cross-episode audio mismatch | Encoding | Different audio stream counts across episodes | Inconsistent audio tracks in Jellyfin |

### DEBUG-Only Patterns

| Pattern | Stage | Evidence |
|---------|-------|----------|
| TMDB candidate scoring | Identification | `decision_type=tmdb_search` with all candidates |
| Episode runtime matching | Identification | `decision_type=episode_runtime_match` |
| Edition marker analysis | Identification | No edition markers detected (DEBUG level) |
| Track selection | Ripping | `decision_type=track_select` per-track |
| Forced subtitle ranking | Subtitles | `decision_type=subtitle_rank` candidate scores (forced subs only) |
| Forced subtitle edition scoring | Subtitles | `edition=match` or `edition=mismatch` in forced subtitle ranking reasons |
| Content ID candidate selection | Episode ID | `decision_type=contentid_candidates` with candidate sources |
| Content ID match scores | Episode ID | `decision_type=contentid_matches` with per-episode similarity scores |
| OpenSubtitles reference search | Episode ID | `decision_type=opensubtitles_reference_search` per-episode results |

## Audit Report Format

**Only include sections applicable to the item's furthest completed stage.** Omit sections for stages the item never reached. For failed items, the report should focus on diagnosing the failure rather than listing empty sections.

```
## Audit Report for Item #<id>

**Title:** <identified_title>
**Status:** <status> | **NeedsReview:** <bool> | **ReviewReason:** <reason>
**Media Type:** <movie/tv> | **Source:** <DVD/Blu-ray/4K Blu-ray> (inferred from logs)
**Edition:** <edition_label or "none detected">
**Debug Mode:** active/inactive (debug logs available: yes/no)

### Executive Summary
<1-2 sentence overview of findings>

### Issues Found

**[CRITICAL] <Issue Name>**
- Evidence: <specific log excerpt or measurement>
- Expected: <what should have happened>
- Actual: <what did happen>
- Impact: <user-facing consequence>
- Recommendation: <specific action>

**[WARNING] <Issue Name>**
...

**[INFO] <Observation>**
...

### Artifact Analysis

#### Log Analysis
- Total log entries: <count>
- WARN events: <count> (list if > 0)
- ERROR events: <count> (list if > 0)
- Decision events: <summary of key decisions>
- Timing anomalies: <any detected>

#### Rip Cache (if applicable)
- Cache path: <path>
- Title count: <N>
- Episode count: <N> (for TV)
- Anomalies: <any detected>

#### Episode Identification (TV only, if applicable)
- Content ID method: <whisperx_opensubtitles / skipped (reason)>
- Episodes synchronized: <yes/no>
- Episode manifest:

| Episode | Key | TitleID | Confidence | Status |
|---------|-----|---------|------------|--------|
| S01E01 | s01e01 | 3 | 0.87 | OK |
| S01E02 | s01e02 | 5 | 0.72 | WARNING |

- Sequence continuity: <sequential/gaps detected>
- Low confidence episodes: <count> (list any < 0.80)

#### Encoded File (if applicable)

**Movie:**
- Path: <path>
- Duration: <seconds> | Size: <MB>
- Video: <codec> <resolution> <HDR status>
- Audio streams: <count>
  - Primary: <description>
  - Commentary: <count> tracks (labeled: yes/no)
- Crop applied: <crop filter or "none">
- Calculated aspect ratio: <ratio>

**TV (per-episode summary):**

| Episode | Duration | Size | Resolution | Codec | Audio Streams | Crop | Status |
|---------|----------|------|------------|-------|---------------|------|--------|
| S01E01 | 2580s | 1.2GB | 1920x1080 | AV1 | 1 | none | completed |
| S01E02 | 2640s | 1.3GB | 1920x1080 | AV1 | 1 | none | completed |

- Cross-episode consistency: <pass/concerns> (resolution, codec, audio count)
- Failed episodes: <count> (list with error messages if any)

#### Edition Detection (movies only)
- Detection method: <regex/llm/cache/none>
- Edition label: <label or "none">
- Filename includes edition: <yes/no>
- blu-ray.com confirms edition: <yes/no/not checked>

#### Subtitles (if applicable)

**Movie:**
- Source: WhisperX (regular always WhisperX; forced from OpenSubtitles if enabled)
- Muxed into MKV: <yes/no>
- Subtitle tracks: <count>
- Track labels correct: <yes/no> (regular has language name, forced has "(Forced)")
- SRT sidecar files: <count>
- Duration coverage: <percentage>
- Cue density: <cues/minute>
- Forced subtitle edition match: <match/mismatch/n/a> (only for forced subs from OpenSubtitles)
- Content spot-check: <pass/concerns>

**TV (per-episode summary):**

| Episode | Muxed | Tracks | Cue Density | Duration Coverage | Status |
|---------|-------|--------|-------------|-------------------|--------|
| S01E01 | yes | 1 | 4.2/min | 98% | completed |
| S01E02 | yes | 1 | 3.8/min | 97% | completed |

- Cross-episode consistency: <pass/concerns> (cue density, language, forced sub presence)
- Failed episodes: <count> (list with error messages if any)

### External Validation (Blu-ray/4K Only, Post-Encoding Only)

**Omit this entire section if:** (a) item has not passed encoding, OR (b) source is DVD. No encoded artifacts or no reliable external reference = nothing to validate.

#### blu-ray.com Review
- URL: <review URL if found>
- Listed aspect ratio: <ratio>
- Listed audio tracks: <description>
- Listed commentary: <count and description>
- Edition type: <theatrical/director's cut/extended/etc. or "standard release">

#### Validation Results
- Aspect ratio match: <yes/no/concern>
- Commentary count match: <yes/no/concern>
- Edition detection match: <yes/no/concern> (is our detection correct?)
- Other notes: <any discrepancies>

### Decision Trace
<key decisions with decision_type, decision_result, decision_reason>
```

## Execution Checklist

After Phase 1, determine the furthest completed stage per the Stage Gating table and check only applicable items. **Do not check items beyond the reached stage.**

### Always
- [ ] Gathered item info and status
- [ ] Determined furthest completed stage (applied stage gating)
- [ ] Determined disc source type from logs (DVD / Blu-ray / 4K Blu-ray)
- [ ] Determined media type (movie / TV)
- [ ] Located and read log files (debug preferred)
- [ ] Analyzed logs for anomalies beyond simple error counts
- [ ] If TV: checked for episode pipeline log patterns (low confidence, per-episode failures)
- [ ] For failed items: diagnosed failure cause, timing, and retry patterns

### Post-Identification (item reached IDENTIFIED or beyond)
- [ ] If movie: validated edition detection logic from logs

### Post-Ripping (item reached RIPPED or beyond)
- [ ] Analyzed rip cache metadata
- [ ] If TV: validated per-episode ripped assets (count, status, EpisodeKey matching)

### Post-Episode-Identification (TV only, item reached EPISODE_IDENTIFIED or beyond)
- [ ] Checked content ID method (whisperx_opensubtitles or skipped)
- [ ] Reviewed episode manifest with MatchConfidence scores
- [ ] Flagged low confidence episodes (CRITICAL < 0.70, WARNING 0.70-0.80)
- [ ] Verified episode sequence continuity (no gaps or duplicates)
- [ ] Checked `content_id_matches` attribute completeness
- [ ] Verified `episodes_synchronized` flag is true

### Post-Encoding (item reached ENCODED or beyond)
- [ ] Ran ffprobe and analyzed streams (per-episode for TV)
- [ ] Validated crop detection (aspect ratio sanity check)
- [ ] Verified commentary labeling specifically
- [ ] If movie with edition: verified edition in filename
- [ ] If TV: checked per-episode encoded asset status (failed episodes)
- [ ] If TV: verified cross-episode consistency (resolution, codec, audio count)
- [ ] If TV: spot-checked crop consistency across episodes
- [ ] If Blu-ray/4K (not DVD): looked up blu-ray.com review
- [ ] If Blu-ray/4K (not DVD): validated crop detection against review
- [ ] If Blu-ray/4K (not DVD): validated commentary count against review
- [ ] If Blu-ray/4K movie (not DVD): validated edition detection against review

### Post-Audio-Analysis (item reached AUDIO_ANALYZED or beyond)
- [ ] Reviewed LLM decisions (commentary, edition) for reasonableness
- [ ] If TV: verified cross-episode audio stream count consistency

### Post-Subtitling (item reached SUBTITLED or beyond)
- [ ] Verified subtitles are muxed into MKV (per-episode for TV)
- [ ] Verified subtitle track labels (language name, "(Forced)" marker)
- [ ] Analyzed subtitle content quality
- [ ] If movie with edition and forced subs: verified forced subtitle edition matching
- [ ] If TV: checked per-episode subtitle asset status (failed episodes, SubtitlesMuxed flags)
- [ ] If TV: verified cross-episode subtitle consistency (cue density, language, forced sub presence)

### Report
- [ ] Generated report with only applicable sections (omit sections for unreached stages)
- [ ] If TV: used per-episode summary tables for encoded files and subtitles
