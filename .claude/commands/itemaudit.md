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

### Phase 2: Log Analysis (All Items)

**Go beyond simple error counts.** Analyze logs for:

1. **Decision anomalies**:
   - Low confidence scores on decisions that were accepted anyway
   - Unexpected fallbacks (preset fallback, subtitle source changes)
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
   - Search for `decision_type=preset_llm` entries
   - Search for `decision_type=commentary` entries
   - Search for `decision_type=edition_detection` entries (movies only)
   - Evaluate if confidence levels and reasons make sense for the content

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

### Phase 4: Encoded File Analysis (Post-Encoding Items)

If item has passed encoding stage:

1. **Run ffprobe** on the encoded file:
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

### Phase 5: Crop Detection Validation (Blu-ray/4K Blu-ray)

For Blu-ray and 4K Blu-ray content:

1. **Extract crop info** from EncodingDetailsJSON:
   - `Crop.required` - was cropping needed?
   - `Crop.crop` - the applied crop filter (e.g., "crop=1920:800:0:140")
   - `Crop.message` - detection summary

2. **Look up disc review on blu-ray.com**:
   - Search: `site:blu-ray.com "<title>" review` (use identified title from metadata)
   - Find the official review page
   - Look for "Video" section mentioning aspect ratio
   - Common ratios: 2.39:1/2.40:1 (scope), 1.85:1, 1.78:1 (16:9), IMAX variable

3. **Cross-reference**:
   - Does our detected crop produce the expected aspect ratio?
   - Calculate: `(video_width - left_crop - right_crop) / (video_height - top_crop - bottom_crop)`
   - 2.39:1 scope = ~2.39, 1.85:1 = ~1.85, 2.00:1 = IMAX
   - Flag if our crop differs significantly from the review's stated ratio

4. **Check for IMAX/variable aspect ratio issues**:
   - If crop detection shows "multiple ratios" or low top-candidate percentage
   - Some films have IMAX sequences with different ratios
   - This may be acceptable or may indicate detection issues

### Phase 6: Edition Detection Validation (Movies Only)

If item is a movie, validate edition detection:

1. **From logs**: Search for `decision_type=edition_detection` entries
2. **Expected detection paths**:
   - `decision_reason=regex_pattern_match`: Known edition detected via pattern (Director's Cut, Extended, etc.)
   - `decision_reason=llm_confirmed`: Ambiguous edition confirmed by LLM
   - `decision_reason=llm_rejected`: LLM determined not an edition
   - `decision_reason=llm_not_configured`: Ambiguous title but no LLM available

3. **Verify detection correctness**:
   - If disc title contains obvious edition markers (Director's Cut, Extended, Unrated, IMAX, etc.), an edition should be detected
   - If cache was used, check for `edition from cache` log entry
   - Cross-reference with blu-ray.com review to confirm whether disc is actually an alternate edition

4. **Verify edition label**:
   - Check `edition_label` in logs matches the actual edition type
   - Known patterns: Director's Cut, Extended Edition, Unrated, Theatrical, Remastered, Special Edition, Anniversary Edition, Ultimate Edition, Final Cut, Redux, IMAX

5. **Verify filename**:
   - Movie filenames should be: `Title (Year) - Edition.mkv`
   - Check final organized file includes edition suffix when detected
   - Edition should NOT appear in folder name (GetBaseFilename strips it)

### Phase 7: Subtitle Analysis (Post-Subtitling Items)

If item has passed subtitling stage:

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

6. **Edition-aware subtitle selection** (if movie has edition):
   - Check logs for `edition=match` or `edition=mismatch` in subtitle ranking
   - Selected subtitle should match edition when possible (e.g., Director's Cut subtitle for Director's Cut disc)
   - If `edition=mismatch` was accepted, verify no matching edition subtitle was available

### Phase 8: Commentary Track Validation

1. **From logs**: Find `commentary track classification` and `commentary detection complete` entries
2. **Expected behavior**:
   - 2-channel English tracks that aren't stereo downmixes should be candidates
   - High similarity to primary audio = stereo downmix (excluded)
   - LLM should classify based on content (talking about filmmaking = commentary)

3. **Cross-reference with blu-ray.com**:
   - Check "Audio" section of disc review
   - Note how many commentary tracks the disc actually has
   - Compare against our detection count

4. **Verify in encoded file**:
   - Count audio streams with "comment" disposition
   - Verify all are properly labeled (see Phase 4)

### Phase 9: Preset Selection Validation

1. **From logs**: Find `preset_decider` or `preset_llm` decision entries
2. **Review decision**:
   - What profile was selected? (clean/grain/default)
   - What was the confidence?
   - What was the reasoning?

3. **Cross-reference with content**:
   - Films with intentional grain (older films, certain directors) should use "grain"
   - Clean digital productions should use "clean"
   - If unsure, "default" is appropriate

4. **Flag concerns**:
   - Low confidence selections that weren't defaulted
   - Profile that seems wrong for the content type
   - Fallback to default due to errors

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
| Preset fallback | Encoding | `alert=preset_decider_fallback` | Suboptimal encoding |
| Wrong crop detection | Encoding | Aspect ratio mismatch vs blu-ray.com | Black bars or cut content |
| Missing commentary | Audio Analysis | Count mismatch vs blu-ray.com review | Commentary tracks not preserved |
| Unlabeled commentary | Audio Analysis | Missing title/disposition in ffprobe | Jellyfin won't recognize tracks |
| Stereo downmix kept | Audio Analysis | Extra 2ch track that matches primary | Unnecessary audio bloat |
| SRT validation issues | Subtitles | `event_type=srt_validation_issues` | Malformed subtitles |
| Subtitle duration mismatch | Subtitles | Duration delta > 10 minutes | Wrong subtitle file |
| Sparse subtitles | Subtitles | < 2 cues/minute | Possibly wrong language/incomplete |
| Edition subtitle mismatch | Subtitles | `edition=mismatch` when matching subtitle exists | Wrong timing for alternate cut |
| Subtitles not muxed | Subtitles | Sidecar SRT exists but no embedded tracks | Jellyfin may not auto-load |
| Unlabeled subtitles | Subtitles | Missing or incorrect title in embedded track | Jellyfin won't display track name properly |

### DEBUG-Only Patterns

| Pattern | Stage | Evidence |
|---------|-------|----------|
| TMDB candidate scoring | Identification | `decision_type=tmdb_search` with all candidates |
| Episode runtime matching | Identification | `decision_type=episode_runtime_match` |
| Edition marker analysis | Identification | No edition markers detected (DEBUG level) |
| Track selection | Ripping | `decision_type=track_select` per-track |
| Preset LLM response | Encoding | `decision_type=preset_llm` full prompt/response |
| Subtitle ranking | Subtitles | `decision_type=subtitle_rank` candidate scores |
| Edition match scoring | Subtitles | `edition=match` or `edition=mismatch` in ranking reasons |

## Audit Report Format

```
## Audit Report for Item #<id>

**Title:** <identified_title>
**Status:** <status> | **NeedsReview:** <bool> | **ReviewReason:** <reason>
**Media Type:** <movie/tv> | **Source:** <DVD/Blu-ray/4K Blu-ray>
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

#### Encoded File (if applicable)
- Path: <path>
- Duration: <seconds> | Size: <MB>
- Video: <codec> <resolution> <HDR status>
- Audio streams: <count>
  - Primary: <description>
  - Commentary: <count> tracks (labeled: yes/no)
- Crop applied: <crop filter or "none">
- Calculated aspect ratio: <ratio>

#### Edition Detection (movies only)
- Detection method: <regex/llm/cache/none>
- Edition label: <label or "none">
- Filename includes edition: <yes/no>
- blu-ray.com confirms edition: <yes/no/not checked>

#### Subtitles (if applicable)
- Muxed into MKV: <yes/no>
- Subtitle tracks: <count>
- Track labels correct: <yes/no> (regular has language name, forced has "(Forced)")
- SRT sidecar files: <count>
- Duration coverage: <percentage>
- Cue density: <cues/minute>
- Edition match status: <match/mismatch/n/a>
- Content spot-check: <pass/concerns>

### External Validation (Blu-ray/4K)

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

For each audit, complete these steps:

- [ ] Gathered item info and status
- [ ] Located and read log files (debug preferred)
- [ ] Analyzed logs for anomalies beyond simple error counts
- [ ] If post-ripping: analyzed rip cache metadata
- [ ] If post-encoding: ran ffprobe and analyzed streams
- [ ] If post-encoding: verified commentary labeling specifically
- [ ] If movie: validated edition detection logic
- [ ] If movie with edition: verified edition in filename
- [ ] If Blu-ray: looked up blu-ray.com review
- [ ] If Blu-ray: validated crop detection against review
- [ ] If Blu-ray: validated commentary count against review
- [ ] If Blu-ray movie: validated edition detection against review
- [ ] If post-subtitling: verified subtitles are muxed into MKV
- [ ] If post-subtitling: verified subtitle track labels (language name, "(Forced)" marker)
- [ ] If post-subtitling: analyzed subtitle content quality
- [ ] If movie with edition: verified subtitle edition matching
- [ ] Reviewed LLM decisions (preset, commentary, edition) for reasonableness
- [ ] Generated comprehensive report with specific findings
