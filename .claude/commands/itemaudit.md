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

### Phase 6: Subtitle Analysis (Post-Subtitling Items)

If item has passed subtitling stage:

1. **Locate SRT files**: Look in staging directory and final destination
2. **Read and analyze SRT content**:
   ```bash
   head -100 "<srt_file>"  # Check beginning
   tail -50 "<srt_file>"   # Check ending
   wc -l "<srt_file>"      # Total lines (rough cue estimate)
   ```

3. **Content quality checks**:
   - First cue timestamp reasonable (typically within first few minutes)
   - Last cue timestamp near video duration
   - Cue density: minimum ~2 cues per minute expected
   - No obvious encoding issues (mojibake, wrong language)
   - Dialogue makes sense for the content (spot check a few cues)

4. **Duration alignment**:
   - Subtitle end time should be within 10 minutes of video duration
   - Subtitles significantly shorter = missing content
   - Subtitles significantly longer = wrong subtitle file

### Phase 7: Commentary Track Validation

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

### Phase 8: Preset Selection Validation

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
| Preset fallback | Encoding | `alert=preset_decider_fallback` | Suboptimal encoding |
| Wrong crop detection | Encoding | Aspect ratio mismatch vs blu-ray.com | Black bars or cut content |
| Missing commentary | Audio Analysis | Count mismatch vs blu-ray.com review | Commentary tracks not preserved |
| Unlabeled commentary | Audio Analysis | Missing title/disposition in ffprobe | Jellyfin won't recognize tracks |
| Stereo downmix kept | Audio Analysis | Extra 2ch track that matches primary | Unnecessary audio bloat |
| SRT validation issues | Subtitles | `event_type=srt_validation_issues` | Malformed subtitles |
| Subtitle duration mismatch | Subtitles | Duration delta > 10 minutes | Wrong subtitle file |
| Sparse subtitles | Subtitles | < 2 cues/minute | Possibly wrong language/incomplete |

### DEBUG-Only Patterns

| Pattern | Stage | Evidence |
|---------|-------|----------|
| TMDB candidate scoring | Identification | `decision_type=tmdb_search` with all candidates |
| Episode runtime matching | Identification | `decision_type=episode_runtime_match` |
| Track selection | Ripping | `decision_type=track_select` per-track |
| Preset LLM response | Encoding | `decision_type=preset_llm` full prompt/response |
| Subtitle ranking | Subtitles | `decision_type=subtitle_rank` candidate scores |

## Audit Report Format

```
## Audit Report for Item #<id>

**Title:** <identified_title>
**Status:** <status> | **NeedsReview:** <bool> | **ReviewReason:** <reason>
**Media Type:** <movie/tv> | **Source:** <DVD/Blu-ray/4K Blu-ray>
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

#### Subtitles (if applicable)
- SRT files: <count>
- Duration coverage: <percentage>
- Cue density: <cues/minute>
- Content spot-check: <pass/concerns>

### External Validation (Blu-ray/4K)

#### blu-ray.com Review
- URL: <review URL if found>
- Listed aspect ratio: <ratio>
- Listed audio tracks: <description>
- Listed commentary: <count and description>

#### Validation Results
- Aspect ratio match: <yes/no/concern>
- Commentary count match: <yes/no/concern>
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
- [ ] If Blu-ray: looked up blu-ray.com review
- [ ] If Blu-ray: validated crop detection against review
- [ ] If Blu-ray: validated commentary count against review
- [ ] If post-subtitling: analyzed SRT content quality
- [ ] Reviewed LLM decisions (preset, commentary) for reasonableness
- [ ] Generated comprehensive report with specific findings
