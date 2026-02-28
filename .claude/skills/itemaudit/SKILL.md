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

The goal is to **uncover problems that automated code does not detect**. Quick log scans saying "no warnings, no errors" are insufficient. This skill performs deep analysis of all available artifacts to find anomalies.

## Audit Procedure

### Phase 1: Gather Artifacts

Run the `audit-gather` subcommand to collect all artifacts in a single pass:

```bash
spindle audit-gather <item_id>
```

This returns a JSON report containing:
- **`item`**: Queue item summary (status, flags, paths, timestamps)
- **`stage_gate`**: Pre-computed phase applicability (which analyses apply, media type, disc source, edition)
- **`logs`**: Parsed log entries — decisions, warnings, errors, and stage timing events with raw JSON
- **`rip_cache`**: Cache metadata (disc title, rip spec, needs_review flag)
- **`envelope`**: Parsed ripspec Envelope (titles, episodes, assets at each stage, attributes)
- **`encoding`**: Encoding details snapshot (crop, validation, config, result)
- **`media`**: ffprobe output for each encoded file (streams, format, duration, size)
- **`errors`**: Any gathering errors (missing logs, parse failures, etc.)
- **`analysis`**: Pre-computed summaries — decision groups, episode consistency, crop analysis, episode stats, media stats, asset health, anomaly flags (see Analysis Reference below)

**The `stage_gate` object tells you exactly which phases to run.** Each `phase_*` boolean is pre-computed from the item's status, media type, and disc source. Do not re-derive these — trust the gate.

If `/itemaudit` is invoked without an item ID, run `spindle status` and `spindle queue list` to diagnose daemon-level issues instead.

### Analysis Reference

The `analysis` object (nil if no data) contains pre-computed summaries:

| Field | Present When | Contents |
|-------|-------------|----------|
| `decision_groups` | Decisions exist | Groups by (type, result, reason) with count. `entries` included when count=1 or messages vary; nil when all identical. |
| `episode_consistency` | 2+ TV probes | `majority_profile` (video_codec, width, height, audio_streams, subtitle_count), `majority_count`, `total_episodes`, `deviations[]` with human-readable differences. |
| `crop_analysis` | Crop data exists | `filter`, `output_width/height`, `aspect_ratio`, `standard_ratio`, `required`, `disabled`. |
| `episode_stats` | Episodes exist | `count`, `matched`, `unresolved`, `confidence_min/max/mean`, `below_070/080/090` (cumulative), `sequence_contiguous`, `episode_range`. |
| `media_stats` | Valid probes exist | `file_count`, `duration_min_sec/max_sec`, `size_min_bytes/max_bytes`. |
| `asset_health` | Assets exist | Per-stage (ripped/encoded/subtitled/final) `total/ok/failed/muxed` counts. |
| `anomalies` | Issues detected | Pre-flagged issues with `severity` (critical/warning/info), `category`, `message`. |

**Use `analysis.anomalies` as a starting checklist for Issues Found.** Each anomaly is a machine-detected flag -- the LLM's job is to investigate context, assess impact, and add judgment-based findings the code cannot detect.

### Extraction Strategy

After running `spindle audit-gather`, process the full JSON output through a **single comprehensive extraction script** rather than making many narrow extraction passes. The script should produce one compact output that enables all subsequent analysis phases without further extraction calls.

The extraction script should:

1. **Summarize metadata**: item fields, stage_gate, gathering errors
2. **Format pre-computed analysis**: Read `analysis.decision_groups` for deduplicated decisions (groups with `count > 1` and nil `entries` are identical repeats; groups with `entries` have varying messages). Read `analysis.anomalies` for pre-flagged issues. Read `analysis.episode_stats`, `analysis.media_stats`, `analysis.crop_analysis`, `analysis.episode_consistency`, `analysis.asset_health` for pre-computed summaries.
3. **List all warnings and errors** with full context (these are always few enough to show individually)
4. **Show stage timing** with computed durations
5. **Summarize episode manifest** with confidence scores and episode numbers

Steps 2/5/6/8 from the old extraction strategy are now pre-computed in `analysis` -- the script reads and formats them rather than computing them from raw data. This approach replaces 10+ sequential extraction calls with 1-2, keeping the analysis equally thorough while significantly reducing gathering overhead.

### Stage Gating

The `stage_gate` object in the audit-gather output contains:

| Field | Meaning |
|-------|---------|
| `furthest_stage` | Status the item reached (or failed at) |
| `media_type` | `movie` or `tv` |
| `disc_source` | `bluray`, `4k_bluray`, `dvd`, or `unknown` |
| `edition` | Detected edition label (empty if none) |
| `phase_logs` | Always true |
| `phase_rip_cache` | Post-ripping |
| `phase_episode_id` | TV only, post-episode-identification |
| `phase_encoded` | Post-encoding |
| `phase_crop` | Post-encoding |
| `phase_edition` | Movies only, post-identification |
| `phase_subtitles` | Post-subtitling |
| `phase_commentary` | Post-audio-analysis |
| `phase_external_validation` | Post-encoding AND non-DVD source |

**Key principles:**
- External validation (blu-ray.com lookups) is only useful when (a) there are encoded files to cross-reference AND (b) the source is Blu-ray or 4K Blu-ray. **Skip external validation entirely for DVDs.**
- **For failed items:** Focus the report on diagnosing the failure. Analyze the error, the events leading up to it, and any retry patterns. Do not pad the report with sections that say "N/A - not reached".

### Phase 2: Log Analysis (when `phase_logs` is true)

Analyze `logs.decisions`, `logs.warnings`, `logs.errors`, and `logs.stages` from the audit-gather output. **Go beyond simple error counts.**

1. **Decision anomalies** (from `logs.decisions`):
   - Low confidence scores on decisions that were accepted anyway
   - Unexpected fallbacks (encoding retries)
   - Decisions that contradict expected behavior for the content type
   - Filter by `decision_type` to find specific categories (commentary, edition_detection, tmdb_confidence, etc.)
   - Use `raw_json` fields when you need full context beyond the extracted fields

2. **Timing anomalies** (from `logs.stages`):
   - Stages taking unusually long or short (use `duration_seconds` when available)
   - Large gaps between stage events suggesting hangs
   - Repeated retry attempts

3. **Data flow anomalies**:
   - Track counts changing unexpectedly between stages
   - Episode counts not matching expectations
   - File sizes that seem wrong for the content

4. **LLM decision review** (filter `logs.decisions` by `decision_type`):
   - `decision_type=commentary` entries
   - `decision_type=edition_detection` entries (movies only)
   - Evaluate if confidence levels and reasons make sense for the content

5. **TV episode pipeline checks** (TV only, from `logs.decisions` and `logs.warnings`):
   - `decision_type=episode_identification` entries — check if stage was skipped and why (valid reasons: `movie_content`, `opensubtitles_disabled`, `content_matcher_unavailable`)
   - `decision_type=episode_review` with `decision_result=needs_review` — episodeid flagged unresolved episodes
   - `event_type=contentid_no_references` or `contentid_no_matches` in warnings — soft failures
   - `event_type=episode_match_low_confidence` — episodes with `MatchConfidence` below 0.70
   - `decision_type=contentid_matches` — final episode-to-reference matching results
   - Verify placeholder keys (`s01_001`) were replaced with resolved keys (`s01e03`) after episodeid

### Phase 3: Rip Cache Analysis (when `phase_rip_cache` is true)

Analyze the `rip_cache` section from audit-gather output:

1. **Verify** `rip_cache.found` is true — if false, cache may have been pruned
2. **Check metadata**:
   - `disc_title` matches expected content
   - `needs_review` flag status and reason
3. **Per-episode asset validation** (TV only, from `envelope.assets.ripped`):
   - Verify each episode in `envelope.episodes` has a corresponding `ripped` asset with matching `episode_key`
   - Pre-episodeid, keys are placeholders (`s01_001`, `s01_002`) with `episode=0` — this is expected
   - Check for any ripped assets with `status: "failed"` or missing `path`
   - Verify ripped asset count matches episode count

### Phase 3b: Episode Identification Validation (when `phase_episode_id` is true)

**TV only.** Analyze `envelope.episodes`, `envelope.attributes`, and `item.needs_review`:

1. **Content ID method**: Check `envelope.attributes["content_id_method"]`
   - `whisperx_opensubtitles` = full pipeline
   - If absent, check `logs.decisions` for skip reason

2. **Episode manifest review**: `analysis.episode_stats` provides pre-computed `confidence_min/max/mean`, `below_070/080/090` counts, `unresolved` count, and `sequence_contiguous` for the overview. Use these for the summary, but still review the full `envelope.episodes[]` manifest for per-episode details. Confidence thresholds:
   - **CRITICAL** (< 0.70): Episode ordering likely wrong. Check `item.needs_review`
   - **WARNING** (0.70-0.80): Marginal confidence
   - **OK** (> 0.80): High confidence match
   - **Zero** (0.0): Unresolved episode

3. **`content_id_matches` attribute**: Check `envelope.attributes["content_id_matches"]`:
   - Verify all episodes have a match entry
   - Check `matched_episode` numbers form a reasonable sequence
   - Minimum accepted similarity score is 0.58 — scores near this floor warrant scrutiny

4. **Episode sequence continuity**: `analysis.episode_stats.sequence_contiguous` and `episode_range` are pre-computed. If not contiguous, inspect `envelope.episodes[]` for gaps or duplicates indicating matching errors

5. **`episodes_synchronized` flag**: Check `envelope.attributes["episodes_synchronized"]` — should be `true` after successful identification

### Phase 4: Encoded File Analysis (when `phase_encoded` is true)

Analyze the `media` array from audit-gather output. Each entry contains full ffprobe results.

**TV note:** The encoding snapshot only contains data for the last episode encoded (the snapshot is overwritten per-episode during encoding). Use `media[]` probes for per-episode stream validation. The snapshot is still useful for crop detection, encoding config, and validation results (which are consistent across episodes from the same disc).

**For movies** (single entry) or **per-episode for TV** (entries with `episode_key`):

1. **Verify video stream** (from `media[].probe.streams` where `codec_type=video`):
   - Resolution matches expected (SD/HD/4K)
   - Codec is AV1 (av1/libaom-av1/libsvtav1)
   - Duration matches source within tolerance (~1-2 seconds)
   - HDR metadata present if expected (color_primaries, transfer_characteristics in tags)

2. **Verify audio streams** (from `media[].probe.streams` where `codec_type=audio`):
   - Primary audio is first and has `disposition.default=1`
   - Commentary tracks have `disposition.comment=1` AND title contains "Commentary"
   - Track count matches expected (primary + commentary tracks)
   - No unexpected stereo downmix tracks

3. **Check commentary labeling** (recent bug area):
   - For each audio stream with `disposition.comment=1`:
     - Stream `tags.title` exists and contains "Commentary" (case-insensitive)
     - If original title was blank, it should now be exactly "Commentary"
     - If original title existed without "commentary", it should have " (Commentary)" appended
   - Cross-reference with commentary decisions in `logs.decisions`

4. **Check subtitle streams** (from `media[].probe.streams` where `codec_type=subtitle`):
   - Verify subtitle track exists with correct language
   - Regular subtitles should have title containing language name (e.g., "English")
   - Forced subtitles should have `disposition.forced=1` and title containing "(Forced)"

5. **Parse encoding details** from `encoding.snapshot`:
   - Check `validation.passed` and individual step results
   - Review crop detection from `crop` fields
   - Check for `warning` or `error` in snapshot

6. **Per-episode asset status** (TV only, from `envelope.assets.encoded`):
   - Check for `status: "failed"` entries with `error_msg`
   - Encoding allows partial success
   - Verify encoded asset count matches episode count

7. **Cross-episode consistency** (TV only):
   - Use `analysis.episode_consistency` for the overview: `majority_profile` gives the common (video_codec, width, height, audio_streams, subtitle_count), `majority_count`/`total_episodes` show how many match, and `deviations[]` lists episodes with human-readable differences
   - Use `analysis.media_stats` for duration range (`duration_min_sec/max_sec`) and size range (`size_min_bytes/max_bytes`)
   - Still inspect individual `media[]` probes for stream-level checks (items 1-6) but use the pre-computed consistency summary for the overview

### Phase 5: Crop Detection Validation (when `phase_crop` is true)

Analyze `encoding.snapshot.crop` from the audit-gather output:

1. **Read pre-computed crop data**: `analysis.crop_analysis` provides `output_width`, `output_height`, `aspect_ratio`, `standard_ratio`, `required`, and `disabled`. Also read `encoding.snapshot.crop.message` for the detection summary.

2. **Verify aspect ratio**: Common ratios: 2.39:1/2.40:1 (scope), 1.85:1, 1.78:1 (16:9), 2.00:1 (IMAX). Compare `analysis.crop_analysis.standard_ratio` against expected for the content.

3. **External cross-reference** (only when `phase_external_validation` is true):
   - Search: `site:blu-ray.com "<title>" review`
   - Flag if our crop differs significantly from the review's stated ratio

4. **IMAX/variable aspect ratio issues**:
   - If crop detection shows "multiple ratios" or low top-candidate percentage

5. **TV episode crop consistency** (TV only):
   - All episodes from the same disc should have identical or very similar crop
   - Spot-check one or two episodes rather than performing full validation on every episode

### Phase 6: Edition Detection Validation (when `phase_edition` is true)

Movies only. Two tiers:
- **Log review** (always): Check decisions from `logs.decisions`
- **External validation** (only when `phase_external_validation` is true)

#### Log Review

1. **Find `decision_type=edition_detection`** entries in `logs.decisions`
2. **Expected detection paths**:
   - `decision_reason=regex_pattern_match`: Known edition detected via pattern
   - `decision_reason=llm_confirmed`: Ambiguous edition confirmed by LLM
   - `decision_reason=llm_rejected`: LLM determined not an edition
   - `decision_reason=llm_not_configured`: Ambiguous title but no LLM available

3. **Verify detection correctness**:
   - If disc title contains obvious edition markers (Director's Cut, Extended, Unrated, IMAX, etc.), an edition should be detected
   - Check if multiple feature-length titles with different durations suggest alternate cuts
   - Check `stage_gate.edition` for the detected label

4. **Verify edition label** against known patterns: Director's Cut, Extended Edition, Unrated, Theatrical, Remastered, Special Edition, Anniversary Edition, Ultimate Edition, Final Cut, Redux, IMAX

#### External Validation (only when `phase_external_validation` is true)

5. **Cross-reference with blu-ray.com** to confirm whether disc is actually an alternate edition
6. **Verify filename**:
   - Check `item.final_file` or `item.encoded_file` includes edition suffix
   - Edition should NOT appear in folder name

### Phase 7: Subtitle Analysis (when `phase_subtitles` is true)

Analyze subtitle streams from `media[].probe.streams` (codec_type=subtitle) and subtitle assets from `envelope.assets.subtitled`.

**For movies** or **per-episode for TV**:

1. **Verify embedded subtitles** from the ffprobe data in `media[]`:
   - Subtitle track exists with correct language
   - Check `disposition.default` for main subtitle
   - Check `disposition.forced` for forced subtitles
   - **Check labeling**: regular subs have language name in title, forced have "(Forced)"

2. **Forced subtitle search outcome** (from `logs.decisions`):
   - Find `decision_type=forced_subtitle_download` with `decision_result=not_found`
   - Zero candidates from OpenSubtitles is **the norm** — do not report this at all (not even as INFO)
   - Only report as **WARNING** if: (a) candidates were returned but all rejected, OR (b) you know the title has significant foreign language dialogue (e.g., Inglourious Basterds, Kill Bill, Narcos) making the absence a real gap

3. **Edition-aware forced subtitle selection** (movies only):
   - Check `logs.decisions` for `edition=match` or `edition=mismatch` in forced subtitle ranking
   - Selected forced subtitle should match edition when possible

4. **Per-episode subtitle asset status** (TV only, from `envelope.assets.subtitled`):
   - Check for `status: "failed"` entries with `error_msg`
   - Verify `subtitles_muxed` flag per episode
   - Check `envelope.attributes["subtitle_generation_results"]` for per-episode details

5. **Cross-episode subtitle consistency** (TV only):
   - All episodes should have same subtitle language and consistent forced subtitle presence

### Phase 8: Commentary Track Validation (when `phase_commentary` is true)

Analyze commentary decisions from `logs.decisions` and audio streams from `media[]`:

1. **From logs**: Find `decision_type=commentary` entries in `logs.decisions`
2. **Expected behavior**:
   - 2-channel English tracks that aren't stereo downmixes should be candidates
   - High similarity to primary audio = stereo downmix (excluded)
   - LLM should classify based on content

3. **Cross-reference with blu-ray.com** (only when `phase_external_validation` is true):
   - Check "Audio" section of disc review for commentary count
   - Compare against our detection count

4. **Verify in media probes**: Count audio streams with `disposition.comment=1` in `media[].probe.streams`

5. **Cross-episode commentary consistency** (TV only):
   - All episodes from the same disc should have same number of audio streams

## Problem Pattern Catalog

### Known Patterns to Check

| Pattern | Stage | Evidence in Audit-Gather Output | Impact |
|---------|-------|--------------------------------|--------|
| Duplicate fingerprint | Identification | `logs.decisions` with `decision_type=duplicate_fingerprint` | Item silently rejected |
| Low TMDB confidence | Identification | `logs.decisions` with `decision_type=tmdb_confidence`, low score | Wrong title match |
| Unresolved placeholder episodes | Episode ID | `envelope.episodes` with `episode=0` and placeholder keys after episodeid | Episodes land in review_dir |
| Missed edition detection | Identification | No `edition_detection` decision for disc with edition markers | Edition not in filename |
| Wrong edition label | Identification | `stage_gate.edition` doesn't match actual edition type | Incorrect filename/subtitle |
| Edition detection LLM failure | Identification | `logs.errors` or `logs.warnings` with `event_type=edition_llm_failed` | Ambiguous edition not detected |
| Wrong crop detection | Encoding | `encoding.snapshot.crop` aspect ratio mismatch vs blu-ray.com | Black bars or cut content |
| Missing commentary | Audio Analysis | Count mismatch vs blu-ray.com review using `media[].probe.streams` | Commentary tracks not preserved |
| Unlabeled commentary | Audio Analysis | Audio stream with `disposition.comment=1` but no "Commentary" in `tags.title` | Jellyfin won't recognize tracks |
| Stereo downmix kept | Audio Analysis | Extra 2ch audio track in `media[].probe.streams` | Unnecessary audio bloat |
| SRT validation issues | Subtitles | `logs.warnings` with `event_type=srt_validation_issues` | Malformed subtitles |
| Subtitle duration mismatch | Subtitles | Subtitle stream duration vs video duration delta > 10 minutes | WhisperX timing issue |
| Forced subtitle not found | Subtitles | `logs.decisions` with `decision_result=not_found`, zero candidates | Do not report — this is the norm |
| Forced subtitle candidates rejected | Subtitles | Candidates returned but all rejected during ranking | Filtering or scoring problem |
| Forced subtitle edition mismatch | Subtitles | `edition=mismatch` in forced subtitle ranking | Wrong forced subtitle |
| Subtitles not muxed | Subtitles | No subtitle streams in `media[].probe.streams` | Jellyfin may not auto-load |
| Unlabeled subtitles | Subtitles | Missing or incorrect `tags.title` on subtitle stream | Jellyfin display issue |
| Low episode match confidence | Episode ID | `envelope.episodes[].match_confidence` < 0.70 | Episodes may be mislabeled |
| Episodes unresolved | Episode ID | `item.needs_review=true`, episodes with `episode=0` | Placeholder names in review_dir |
| Episode sequence gaps | Episode ID | Non-sequential episode numbers in `envelope.episodes[]` | Missing episodes or matching error |
| Per-episode rip failure | Ripping | `envelope.assets.ripped[]` with `status: "failed"` | Episode missing from pipeline |
| Per-episode encode failure | Encoding | `envelope.assets.encoded[]` with `status: "failed"` | Episode won't appear in Jellyfin |
| Per-episode subtitle failure | Subtitles | `envelope.assets.subtitled[]` with `status: "failed"` | Episode missing subtitles |
| Cross-episode resolution mismatch | Encoding | Different resolutions across `media[]` entries | Inconsistent quality |
| Cross-episode audio mismatch | Encoding | Different audio stream counts across `media[]` entries | Inconsistent audio tracks |

### DEBUG-Only Patterns

These appear in `logs.decisions` only when debug logs are available (`logs.is_debug=true`):

| Pattern | Stage | `decision_type` |
|---------|-------|-----------------|
| TMDB candidate scoring | Identification | `tmdb_search` |
| Placeholder episode creation | Identification | (visible in `envelope.episodes` with `episode=0`) |
| Track selection | Ripping | `track_select` |
| Forced subtitle ranking | Subtitles | `subtitle_rank` |
| Content ID candidate selection | Episode ID | `contentid_candidates` |
| Content ID match scores | Episode ID | `contentid_matches` |
| OpenSubtitles reference search | Episode ID | `opensubtitles_reference_search` |

## Audit Report Format

**Only include sections applicable to the item's stage gate.** Omit sections for stages the item never reached. For failed items, the report should focus on diagnosing the failure rather than listing empty sections.

### Presentation Density Guidelines

The analysis must remain exhaustive, but the *presentation* should be proportional to findings. Use compact formats for clean data and expand only where anomalies exist.

**Cross-episode data (TV):**
- Build the majority profile line directly from `analysis.episode_consistency.majority_profile` and deviation list from `analysis.episode_consistency.deviations`
- When all episodes match (`majority_count == total_episodes`), use a single summary line:
  `"All 12 episodes: AV1 1436x1080, 1x Opus mono eng, 1x subrip eng"`
- Only expand to a per-episode table when `deviations` is non-empty, and only show the differing fields
- Duration and size ranges come directly from `analysis.media_stats`: `"Duration: 1485-1520s | Size: 292-557 MB"`

**Decision traces:**
- `analysis.decision_groups` already provides the deduplication -- groups with nil `entries` are identical repeats (show as `"type x{count}: result (reason)"`), groups with `entries` have varying messages
- Only expand individual entries for decisions with different outcomes, notable parameter variations, or anomalous confidence/scores

**Episode manifest:**
- Always show the full per-episode table with confidence scores, matched episode numbers, and titles. Episode identification is a core pipeline feature and the manifest is the primary evidence of correctness. This table is never compressed.

**External validation:**
- When all checks confirm, use a compact paragraph rather than multi-level section/subsection structure
- Only expand into detailed comparison when a mismatch is found

**Do not report as findings (these are normal):**
- Non-sequential disc title ordering — disc layout varies by manufacturer and is irrelevant once content ID resolves episodes
- Inconsistent source audio track counts across titles on the same disc — different playlists routinely carry different language sets
- Forced subtitle search returning zero candidates (covered in Phase 7)
- Audio refinement stripping non-English tracks — that's its job

**Stage timing:**
- Always show the timing table — it's compact and useful for spotting anomalies

### Report Template

```
## Audit Report for Item #<id>

**Title:** <item.disc_title>
**Status:** <item.status> | **NeedsReview:** <item.needs_review> | **ReviewReason:** <item.review_reason>
**Media Type:** <stage_gate.media_type> | **Source:** <stage_gate.disc_source>
**Edition:** <stage_gate.edition or "none detected">
**Debug Logs:** <logs.is_debug>

### Executive Summary
<1-2 sentence overview of findings>

### Issues Found

**[CRITICAL] <Issue Name>**
- Evidence: <specific data from audit-gather output>
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
- Log path: <logs.path>
- Total log entries: <logs.total_lines>
- WARN events: <count> (list if > 0)
- ERROR events: <count> (list if > 0)
- Key decisions: <from analysis.decision_groups — expand only anomalous decisions>
- Timing: <stage timing table>

#### Rip Cache (if phase_rip_cache)
- Cache path: <rip_cache.path>
- Found: <rip_cache.found>
- Anomalies: <any detected>

#### Episode Identification (if phase_episode_id)
- Content ID method: <envelope.attributes.content_id_method>
- Episodes synchronized: <envelope.attributes.episodes_synchronized>
- Confidence overview: <from analysis.episode_stats: min/max/mean, below thresholds, unresolved count>
- Episode manifest: <full per-episode table with confidence scores>
- Sequence continuity: <analysis.episode_stats.sequence_contiguous, episode_range>

#### Encoded File (if phase_encoded)

**Movie:**
- Video: <codec> <resolution> <HDR status> | Duration: <seconds>s | Size: <bytes>
- Audio: <stream summary>
- Crop: <analysis.crop_analysis.filter> (<analysis.crop_analysis.standard_ratio>)
- Validation: <passed/failed, expand individual steps only if failed>

**TV:**
- Common profile: <from analysis.episode_consistency.majority_profile>
- Duration: <analysis.media_stats.duration_min_sec>-<max>s | Size: <analysis.media_stats.size_min_bytes>-<max>
- Cross-episode consistency: <analysis.episode_consistency — pass if no deviations, else list deviations>
- Failed episodes: <count, with details if > 0>

#### Edition Detection (if phase_edition)
- Detection method: <from logs.decisions>
- Edition label: <stage_gate.edition>
- Filename includes edition: <check item.final_file or item.encoded_file>

#### Subtitles (if phase_subtitles)
- Tracks: <count and config from media probes>
- Labels correct: <yes/no>
- Forced subtitle outcome: <from logs.decisions>

#### Commentary (if phase_commentary)
- Decisions: <from logs.decisions>
- Tracks in output: <count from media probes>

### External Validation (if phase_external_validation)
<Compact paragraph when all checks pass. Expand into detailed comparison only when mismatches found.>

### Decision Trace
<From analysis.decision_groups — type x{count}: result (reason) for identical groups. Expand entries only for groups with varying messages or anomalous results.>
```

## Execution Checklist

After running `spindle audit-gather`, check only the phases flagged as `true` in `stage_gate`. **Do not check phases beyond the reached stage.**

### Always
- [ ] Ran `spindle audit-gather <id>` and loaded the JSON output
- [ ] Checked `errors` array for gathering failures
- [ ] Reviewed `stage_gate` to determine applicable phases
- [ ] Reviewed `analysis.anomalies` for pre-flagged issues
- [ ] Analyzed `logs` for anomalies beyond simple error counts
- [ ] If TV: checked for episode pipeline log patterns
- [ ] For failed items: diagnosed failure cause from `item.error_message` and log events

### Post-Identification (phase_edition)
- [ ] Validated edition detection logic from `logs.decisions`

### Post-Ripping (phase_rip_cache)
- [ ] Analyzed rip cache metadata
- [ ] If TV: validated per-episode ripped assets in `envelope.assets.ripped`

### Post-Episode-Identification (phase_episode_id)
- [ ] Checked content ID method in `envelope.attributes`
- [ ] Reviewed episode manifest with MatchConfidence scores
- [ ] Verified episode sequence continuity
- [ ] Checked `content_id_matches` attribute completeness
- [ ] Verified `episodes_synchronized` flag

### Post-Encoding (phase_encoded, phase_crop)
- [ ] Analyzed streams from `media[]` entries (video, audio, subtitle)
- [ ] Validated crop detection from `encoding.snapshot.crop`
- [ ] Verified commentary labeling
- [ ] If movie with edition: verified edition in filename
- [ ] If TV: checked cross-episode consistency

### Post-Audio-Analysis (phase_commentary)
- [ ] Reviewed commentary decisions from `logs.decisions`
- [ ] If TV: verified cross-episode audio stream count consistency

### Post-Subtitling (phase_subtitles)
- [ ] Verified subtitle tracks in media probes
- [ ] Verified subtitle track labels
- [ ] If movie with edition and forced subs: verified forced subtitle edition matching
- [ ] If TV: checked per-episode subtitle asset status

### External Validation (phase_external_validation)
- [ ] Looked up blu-ray.com review
- [ ] Validated crop, commentary count, and edition against review

### Report
- [ ] Generated report with only applicable sections
- [ ] Applied presentation density guidelines (compact for clean data, expanded for anomalies)
- [ ] Used `analysis.decision_groups` for decision trace
