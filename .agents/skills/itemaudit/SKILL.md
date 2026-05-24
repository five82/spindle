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

Run the `audit-gather` subcommand to collect all artifacts in a single pass, saving to a temp file:

```bash
spindle audit-gather <item_id> 2>/dev/null > /tmp/spindle-audit-<item_id>.json
```

This produces an agent-facing JSON report. The schema may evolve with this skill; treat it as diagnostic input rather than a stable public API. It contains:
- **`item`**: Queue item summary (`stage`, progress, review flags, paths, timestamps)
- **`stage_gate`**: Pre-computed phase applicability (which analyses apply, resolved media type, media hint, disc source)
- **`logs`**: Parsed log entries — decisions (type/result/reason/message), warnings and errors (with `extras` maps of non-standard log fields for diagnostic context), and stage timing events (`ts`, `event_type`, `stage`, `duration_seconds`)
- **`rip_cache`**: Cache metadata (disc title, cached_at, title_count, total_bytes). Serialized `rip_spec_data` and `metadata_json` blobs are omitted (already in parsed `envelope`).
- **`envelope`**: Parsed ripspec Envelope (titles, episodes, assets at each stage, attributes)
- **`encoding`**: Encoding details snapshot (crop, validation, config, result). Preset settings are omitted; validation results capture pass/fail.
- **`media`**: ffprobe output for encoded files. For TV, only the representative probe (matching majority profile, marked `representative: true`), deviation probes, and error probes are included. `media_omitted` indicates how many clean probes were dropped.
- **`errors`**: Any gathering errors (missing logs, parse failures, etc.)
- **`analysis`**: Pre-computed summaries — decision groups, episode consistency, crop analysis, episode stats, media stats, asset health, anomaly flags (see Analysis Reference below)

**The `stage_gate` object tells you exactly which phases to run.** Each `phase_*` boolean is pre-computed from the item's status, media type, and disc source. Do not re-derive these — trust the gate.

If `/itemaudit` is invoked without an item ID, run `spindle status` and `spindle queue list` to diagnose daemon-level issues instead.

### Analysis Reference

The `analysis` object (nil if no data) contains pre-computed summaries:

| Field | Present When | Contents |
|-------|-------------|----------|
| `decision_groups` | Decisions exist | Groups by (type, result, reason) with count. `entries` included when count=1 or messages vary; nil when all identical. |
| `notable_decisions` | Notable decisions exist | Curated subset of decisions most useful for reporting (TMDB/title/crop/validation/audio/subtitle/routing/episode match), avoiding noisy full decision scans. |
| `stage_timings` | Stage events exist | One row per stage with start, completion, duration, start count, and completion count. Prefer this over raw `logs.stages` for the timing table. |
| `source_summary` | Source/output traits known | Disc source, UHD-likely flag, input/output resolution, input codecs, output codec, HDR/dynamic range. |
| `title_selection` | Movie titles exist | Feature-length candidates, selected title, selection decision/reason, and similar-runtime candidate count. Prefer this over hand-parsing `envelope.titles`. |
| `output_media` | Valid probes exist | Compact stream summaries (video/audio/subtitle labels and flags) derived from ffprobe. Prefer this for normal stream checks; use raw `media[]` only for missing details. |
| `audio_summary` | Audio evidence exists | Primary track, output/excluded/commentary counts, commentary decisions, and commentary label status. |
| `subtitle_summary` | Subtitle evidence exists | Subtitle generation validation counts, output subtitle count, and label status. |
| `routing_summary` | Final assets exist | Final output destination classification and expected-vs-actual route per output. |
| `episode_consistency` | 2+ TV probes | `majority_profile` (video_codec, width, height, audio_streams, subtitle_streams with codec/language/is_forced), `majority_count`, `total_episodes`, `deviations[]` with human-readable differences. |
| `crop_analysis` | Crop data exists | `filter`, `output_width/height`, `aspect_ratio`, `standard_ratio`, `required`. |
| `episode_stats` | Episodes exist | `count`, `matched`, `unresolved`, `placeholder_only`, `confidence_min/max/mean`, `below_070/080/090` (cumulative), `sequence_contiguous`, `episode_range`. |
| `media_stats` | Valid probes exist | `file_count`, `duration_min_sec/max_sec`, `size_min_bytes/max_bytes`. |
| `asset_health` | Assets exist | Per-stage (ripped/encoded/subtitled/final) `total/ok/failed/muxed` counts. |
| `anomalies` | Issues/context detected | Pre-flagged signals with `severity` (critical/warning/info), `category`, `message`. |

**Use critical/warning `analysis.anomalies` as a starting checklist for Issues Found.** Info-level anomalies, if present, are context only unless investigation shows real user impact. Each anomaly is a machine-detected flag -- the LLM's job is to investigate context, assess impact, reject false positives, and add judgment-based findings the code cannot detect.

### Extraction Strategy

After running `spindle audit-gather`, process the full JSON output through a **single comprehensive extraction script** rather than making many narrow extraction passes. The script should produce one compact output that enables all subsequent analysis phases without further extraction calls.

**Shell mechanics:**
1. **Save audit-gather output to a temp file first:**
   ```bash
   spindle audit-gather <item_id> 2>/dev/null > /tmp/spindle-audit-<item_id>.json
   ```
2. **Pass the extraction script via a single-quoted heredoc**, never via `python3 -c "..."` (the `-c` form breaks because bash interprets `!`, `\`, and other special characters inside double quotes):
   ```bash
   python3 << 'PYEOF'
   import json
   data = json.load(open('/tmp/spindle-audit-<item_id>.json'))
   # ... extraction logic ...
   PYEOF
   ```
3. **Never pipe JSON into a heredoc script** (`cat file | python3 << 'PYEOF'`). The heredoc and pipe both compete for stdin — the heredoc wins (providing the script), so `json.load(sys.stdin)` gets nothing, or worse the JSON is interpreted as Python code.

The extraction script should:

1. **Summarize metadata**: item fields, stage_gate, gathering errors
2. **Format pre-computed analysis**: Read `analysis.decision_groups` for deduplicated decisions (groups with `count > 1` and nil `entries` are identical repeats; groups with `entries` have varying messages). Read `analysis.notable_decisions`, `analysis.stage_timings`, `analysis.source_summary`, `analysis.title_selection`, `analysis.output_media`, `analysis.audio_summary`, `analysis.subtitle_summary`, and `analysis.routing_summary` before falling back to raw logs/probes. Read `analysis.anomalies` for pre-flagged critical/warning issues and info context. Read `analysis.episode_stats`, `analysis.media_stats`, `analysis.crop_analysis`, `analysis.episode_consistency`, `analysis.asset_health` for pre-computed summaries.
3. **List all warnings and errors** with full context (these are always few enough to show individually)
4. **Show stage timing** with computed durations
5. **Summarize episode manifest** with confidence scores and episode numbers. If `analysis.episode_stats.placeholder_only` is true and `phase_episode_id` is false, label it clearly as a **placeholder episode inventory**, not a resolved episode manifest.
6. **Title selection** (movies): Prefer `analysis.title_selection` for selected title, feature-length candidates, and similar runtimes. Fall back to `envelope.titles` only if the summary is absent.

Steps 2/5/6/8 from the old extraction strategy are now pre-computed in `analysis` -- the script reads and formats them rather than computing them from raw data. This approach replaces 10+ sequential extraction calls with 1-2, keeping the analysis equally thorough while significantly reducing gathering overhead.

### Stage Gating

The `stage_gate` object in the audit-gather output contains:

| Field | Meaning |
|-------|---------|
| `furthest_stage` | Status the item reached (or failed at) |
| `media_type` | Resolved media type: `movie`, `tv`, or `unknown` |
| `media_hint` | Hint inferred before/without full identification (for example `tv` on a failed TMDB lookup) |
| `disc_source` | `bluray`, `dvd`, or `unknown` |
| `phase_logs` | Always true |
| `phase_rip_cache` | Post-ripping |
| `phase_episode_id` | TV only, post-episode-identification |
| `phase_encoded` | Post-encoding |
| `phase_crop` | Post-encoding |
| `phase_subtitles` | Post-subtitling |
| `phase_commentary` | Post-audio-analysis |
| `phase_external_validation` | Post-encoding AND non-DVD source |

**Key principles:**
- External validation (blu-ray.com lookups) is only useful when (a) there are encoded files to cross-reference AND (b) the source is Blu-ray. **Skip external validation entirely for DVDs.**
- UHD status is not encoded in `disc_source`. Infer UHD from contextual signals: disc title containing "UHD", 2160p resolutions in bdinfo, or similar markers in the audit data.
- **For failed items:** Focus the report on diagnosing the failure. Analyze the error, the events leading up to it, and any retry patterns. Do not pad the report with sections that say "N/A - not reached".
- **TV-hinted no-TMDB-match is now fatal at identification.** Expect these items to fail before ripping rather than continue as degraded TV review items.

### Phase 2: Log Analysis (when `phase_logs` is true)

Analyze `logs.decisions`, `logs.warnings`, `logs.errors`, and `logs.stages` from the audit-gather output. **Go beyond simple error counts.**

1. **Decision anomalies** (from `logs.decisions`):
   - Low confidence scores on decisions that were accepted anyway
   - Unexpected fallbacks (encoding retries)
   - Decisions that contradict expected behavior for the content type
   - Filter by `decision_type` to find specific categories (commentary, tmdb_confidence, etc.)
   - Infrastructure decisions to check: `decision_type=tmdb_match` (acceptance/rejection), `decision_type=title_resolution` (source priority), `decision_type=fingerprint_strategy` (disc type detection), `decision_type=disc_id_cache` (cache hit/miss), `decision_type=transcription_cache` (transcription reuse)
   - Warnings/errors include `extras` maps with non-standard log fields for diagnostic context; decisions use structured fields only (full log lines available at `logs.path`)

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
   - `decision_type=tmdb_match` entries — verify acceptance thresholds are reasonable
   - Evaluate if confidence levels and reasons make sense for the content

5. **TV episode pipeline checks** (TV only, from `logs.decisions` and `logs.warnings`):
   - `decision_type=episode_identification` entries — check if stage was skipped and why (valid reasons include `movie_content`)
   - `decision_type=episode_review` with `decision_result=needs_review` — episodeid flagged unresolved episodes
   - `event_type=contentid_no_references` or `contentid_no_matches` in warnings — soft failures
   - `event_type=low_confidence_match` — episodes with `MatchConfidence` below 0.70
   - `decision_type=contentid_matches` — final episode-to-reference matching results; compare `ambiguous_rips`, `decisive_low_similarity_rips`, and `contested_rips`
   - Verify placeholder keys (`s01_001`) were replaced with resolved keys (`s01e03`) after episodeid
   - **Do not stop at episode-ID quality.** If organizer/review routing is implicated, compare per-episode review state against final destinations.

### Phase 3: Rip Cache Analysis (when `phase_rip_cache` is true)

Analyze the `rip_cache` section from audit-gather output:

1. **Verify** `rip_cache.found` is true — if false, cache may have been pruned
2. **Check metadata**:
   - `disc_title` matches expected content
   - `cached_at`, `title_count`, and `total_bytes` look plausible
3. **Title selection analysis** (movies only, from `envelope.titles`):
   - Identify feature-length titles: titles with `chapters > 1` AND `duration > 3600` seconds
   - The pipeline uses multi-stage selection (`ChoosePrimaryTitle`), not simply the longest title:
     - **Disney multi-language detection**: when 2+ feature-length 800-series playlists (00800-00899) exist with runtimes within 30 seconds, the pipeline prefers the lowest playlist number (00800.mpls = English). The selected title may be *shorter* than alternatives — this is correct behavior for Disney/Pixar/Marvel/Star Wars multi-language discs where language variants differ only in localized title cards and credits.
     - **Different cuts**: when 800-series playlists differ by >30 seconds, treated as different cuts (theatrical vs director's) and longest is preferred.
     - Additional tiebreakers: chapter count, MPLS over M2TS, segment count, TitleHash fingerprint frequency.
   - Check `decision_reason` in logs: `"primary_title_selector"` indicates the multi-stage algorithm was used.
   - Report which title was selected with playlist and duration context. Example: "Selected title 0 (00800.mpls, 6151s / 102.5 min, English) over title 1 (00801.mpls, 6181s / 103.0 min) and title 3 (00802.mpls, 6181s / 103.0 min) via Disney multi-language heuristic"
   - **Flag for review**: if a non-800 playlist was selected when 800-series alternatives exist with similar runtimes (possible mis-selection)
   - The ripped asset filename (from `envelope.assets.ripped[].path`) often contains a title index (e.g., `_t02`) that maps to the `envelope.titles[].id`
   - Include this in the Rip Cache section of the report, not as an issue — it is informational context about what was ripped
   - If only one feature-length title exists, note it briefly ("single feature-length title on disc")
4. **Per-episode asset validation** (TV only, from `envelope.assets.ripped`):
   - Verify each episode in `envelope.episodes` has a corresponding `ripped` asset with matching `episode_key`
   - Pre-episodeid, keys are placeholders (`s01_001`, `s01_002`) with `episode=0` — this is expected
   - Check for any ripped assets with `status: "failed"` or missing `path`
   - Verify ripped asset count matches episode count
5. **Asset mapping strategy** (from `logs.decisions`): Check `decision_type=asset_mapping` — `title_file_map` is the normal path for TV, `directory_scan` is the fallback

### Phase 3b: Episode Identification Validation (when `phase_episode_id` is true)

**TV only.** Analyze `envelope.episodes`, `envelope.attributes`, and `item.needs_review`:

1. **Content ID provenance**: Check `envelope.attributes.content_id`
   - `method` should describe the matching path used
   - `reference_source` should explain where references came from
   - `episodes_synchronized` should be `true` after successful identification
   - `completed` distinguishes successful completion from degraded early exit

2. **Episode manifest review**: `analysis.episode_stats` provides pre-computed `confidence_min/max/mean`, `below_070/080/090` counts, `unresolved` count, `placeholder_only`, and `sequence_contiguous` for the overview. Use these for the summary, but still review the full `envelope.episodes[]` manifest for per-episode details. Confidence thresholds:
   - **CRITICAL** (< 0.70): Episode ordering likely wrong. Check `item.needs_review`
   - **WARNING** (0.70-0.80): Marginal confidence
   - **OK** (> 0.80): High confidence match
   - **Zero** (0.0): Unresolved episode
   - For `logs.decisions` with `decision_type=episode_match`, inspect `confidence_quality` before treating an accepted score as risky. `decisive_low_similarity` means text similarity is lower than a clear match but runner-up margins are strong enough for deterministic acceptance; it should not require `decision_reason=llm_verified`. `ambiguous` means margins were not decisive. `contested` is review-worthy.

3. **Canonical match outcomes live in `episodes[]`**:
   - Verify all episodes have sensible resolved/unresolved state
   - Review `match_confidence`, `needs_review`, and `review_reason` per episode
   - Minimum accepted similarity score is 0.58 — scores near this floor warrant scrutiny

4. **Episode sequence continuity**: `analysis.episode_stats.sequence_contiguous` and `episode_range` are pre-computed. If not contiguous, inspect `envelope.episodes[]` for gaps or duplicates indicating matching errors

### Phase 3c: Final Output Routing Validation (post-organizing items, especially TV with review flags)

Analyze the actual final routing outcome, not just item-level review flags:

1. **Read `envelope.assets.final`** and map final paths by `episode_key`.
2. **For TV, compute expected destination per episode**:
   - resolved + no episode review flag -> library
   - unresolved -> review
   - resolved + episode review flag -> review
3. **Compare expected vs actual destination** using final paths and, when needed, direct filesystem inspection of the review/library directories.
4. **Escalate any mismatch as a primary finding**, especially:
   - all episodes routed to review when only a subset required review
   - review-required episodes routed to library
   - missing final outputs for episodes that should have been organized
5. If the structured audit data is incomplete or suspicious, **inspect the actual directories on disk** rather than assuming the envelope tells the whole story.

### Phase 4: Encoded File Analysis (when `phase_encoded` is true)

Analyze the `media` array from audit-gather output. Each entry contains full ffprobe results.

**TV note:** The encoding snapshot only contains data for the last episode encoded (the snapshot is overwritten per-episode during encoding). The `media[]` array is compressed for TV: only the representative probe (matching the majority profile, marked `representative: true`), deviation probes, and error probes are included. Use `media_omitted` to see how many clean probes were dropped. The representative probe is sufficient for stream-level checks (items 1-6 below); `analysis.episode_consistency` confirms all omitted episodes match the same profile. The snapshot is still useful for crop detection, encoding config, and validation results (which are consistent across episodes from the same disc).

**For movies** (single entry) or **the representative probe for TV**:

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
   - Verify exactly one generated display subtitle track exists with correct language
   - Subtitle title should contain the language name (e.g., "English")
   - Generated subtitle tracks should not have `disposition.forced=1`

5. **Parse encoding details** from `encoding.snapshot`:
   - Check `validation.passed` and individual step results
   - Review crop detection from `crop` fields
   - Check for `warning` or `error` in snapshot
   - Check encoding config: `encoder`, `quality` (CRF value), `preset` (SVT-AV1 speed preset), `tune`, `audio_codec`
   - Check `decision_type=file_probe` in `logs.decisions` for pre-encoding resolution and codec detection
   - Check `decision_type=crop_detection` for crop decision visibility
   - Check `decision_type=encoding_validation` for per-episode validation results
   - `decision_type=validation_failure_route` with `decision_result=flagged_for_review` indicates validation-failed items routed to review

6. **Per-episode asset status** (TV only, from `envelope.assets.encoded`):
   - Check for `status: "failed"` entries with `error_msg`
   - Encoding allows partial success
   - Verify encoded asset count matches episode count

7. **Cross-episode consistency** (TV only):
   - Use `analysis.episode_consistency` for the overview: `majority_profile` gives the common (video_codec, width, height, audio_streams, subtitle_streams), `majority_count`/`total_episodes` show how many match, and `deviations[]` lists episodes with human-readable differences
   - Use `analysis.media_stats` for duration range (`duration_min_sec/max_sec`) and size range (`size_min_bytes/max_bytes`)
   - Inspect the representative probe for stream-level checks (items 1-6); omitted probes are confirmed equivalent by the consistency analysis

### Phase 5: Crop Detection Validation (when `phase_crop` is true)

Analyze crop data from the audit-gather output:

1. **Read pre-computed crop data**: `analysis.crop_analysis` provides `output_width`, `output_height`, `aspect_ratio`, `standard_ratio`, and `required`. Also read `encoding.snapshot.crop_message` for the detection summary.

2. **Verify aspect ratio**: Common ratios: 2.39:1/2.40:1 (scope), 1.85:1, 1.78:1 (16:9), 2.00:1 (IMAX). Compare `analysis.crop_analysis.standard_ratio` against expected for the content.

3. **External cross-reference** (only when `phase_external_validation` is true):
   - Search: `site:blu-ray.com "<title>" review`
   - Flag if our crop differs significantly from the review's stated ratio

4. **IMAX/variable aspect ratio issues**:
   - If crop detection shows "multiple ratios" or low top-candidate percentage

5. **TV episode crop consistency** (TV only):
   - All episodes from the same disc should have identical or very similar crop
   - Spot-check one or two episodes rather than performing full validation on every episode

### Phase 6: Subtitle Analysis (when `phase_subtitles` is true)

Analyze subtitle streams from `media[].probe.streams` (codec_type=subtitle) and subtitle assets from `envelope.assets.subtitled`.

**For movies** or **per-episode for TV**:

1. **Verify embedded subtitles** from the ffprobe data in `media[]`:
   - Exactly one generated display subtitle track should exist per output when subtitles are enabled and muxing succeeded
   - Check `disposition.default` is not unexpectedly enabled for the generated subtitle
   - Check `disposition.forced` is not enabled for the generated subtitle
   - **Check labeling**: subtitle title should contain the language name (e.g., "English")

2. **Subtitle generation outcome** (from `analysis.subtitle_summary`, `envelope.attributes.subtitle_generation_results`, and `logs.decisions`):
   - Spindle now generates one English display SRT from WhisperX. It does not generate forced/foreign subtitle tracks and does not fetch OpenSubtitles output subtitles.
   - `decision_type=subtitle_mux` with `decision_result=skipped` indicates muxing was disabled in config.
   - `decision_type=transcription_cache` shows whether WhisperX reused a cached transcription.
   - Treat additional generated subtitle tracks, forced dispositions, or "Forced" subtitle labels as defects or stale outputs unless there is clear evidence they came from outside the current Spindle subtitle stage.

3. **Per-episode subtitle asset status** (TV only, from `envelope.assets.subtitled`):
   - Check for `status: "failed"` entries with `error_msg`
   - Verify `subtitles_muxed` flag per episode
   - Check `envelope.attributes["subtitle_generation_results"]` for per-episode details
   - Treat `validation_result` as the actionable summary: `passed` is clean, `needs_review` is actionable, `failed` means subtitle generation failed
   - Treat `qc_observations` as telemetry only. Do not list below-threshold observations (for example `high_reading_speed`, `short_cue_duration`, `long_cue_duration`) as Issues Found unless they also appear in `review_issues`/`severe_issues`, caused review routing, or created a visible subtitle problem.

4. **Cross-episode subtitle consistency** (TV only):
   - All episodes should have the same subtitle language and the same single-display-subtitle layout

### Phase 7: Commentary Track Validation (when `phase_commentary` is true)

Analyze commentary decisions from `logs.decisions` and audio streams from `media[]`:

1. **From logs**: Find `decision_type=commentary` entries in `logs.decisions`
2. **Expected behavior**:
   - 2-channel English tracks that aren't stereo downmixes should be candidates
   - High similarity to primary audio = stereo downmix (excluded)
   - LLM should classify based on content

3. **Refinement impact** (from `logs.decisions`): Check `decision_type=commentary_remapping` — shows how many commentary tracks survived audio refinement. `remapped_count=0` means all commentary tracks were lost during refinement.

4. **Cross-reference with blu-ray.com** (only when `phase_external_validation` is true):
   - Check "Audio" section of disc review for commentary count
   - Compare against our detection count

5. **Verify in media probes**: Count audio streams with `disposition.comment=1` in `media[].probe.streams`

6. **Cross-episode commentary consistency** (TV only):
   - All episodes from the same disc should have same number of audio streams

## Problem Pattern Catalog

### Known Patterns to Check

| Pattern | Stage | Evidence in Audit-Gather Output | Impact |
|---------|-------|--------------------------------|--------|
| Duplicate fingerprint | Identification | `logs.decisions` with `decision_type=duplicate_fingerprint` | Item silently rejected |
| Low TMDB confidence | Identification | `logs.decisions` with `decision_type=tmdb_confidence`, low score | Wrong title match |
| Unresolved placeholder episodes | Episode ID | `envelope.episodes` with `episode=0` and placeholder keys after episodeid | Episodes land in review_dir |
| Wrong crop detection | Encoding | `encoding.snapshot.crop_filter` aspect ratio mismatch vs blu-ray.com | Black bars or cut content |
| Missing commentary | Audio Analysis | Count mismatch vs blu-ray.com review using `media[].probe.streams` | Commentary tracks not preserved |
| Unlabeled commentary | Audio Analysis | Audio stream with `disposition.comment=1` but no "Commentary" in `tags.title` | Jellyfin won't recognize tracks |
| Stereo downmix kept | Audio Analysis | Extra 2ch audio track in `media[].probe.streams` | Unnecessary audio bloat |
| SRT validation review/failure | Subtitles | `subtitle_generation_results[].validation_result` is `needs_review` or `failed`; `review_issues`/`severe_issues` populated; review routing present | Malformed or low-quality subtitles requiring action |
| Subtitle duration mismatch | Subtitles | Subtitle stream duration vs video duration delta > 10 minutes | WhisperX timing issue |
| Extra/forced subtitle generated | Subtitles | More than one generated subtitle stream, `disposition.forced=1`, or "Forced" subtitle labels in current outputs | Stale or incorrect subtitle output; current pipeline should produce one non-forced display SRT |
| Subtitles not muxed | Subtitles | No subtitle streams in `media[].probe.streams` | Jellyfin may not auto-load |
| Unlabeled subtitles | Subtitles | Missing or incorrect `tags.title` on subtitle stream | Jellyfin display issue |
| Low episode match confidence | Episode ID | `envelope.episodes[].match_confidence` < 0.70 | Episodes may be mislabeled |
| Decisive low-similarity episode match | Episode ID | `decision_type=episode_match` with `confidence_quality=decisive_low_similarity` and strong margins | Usually not a defect; explain as lower transcript/reference overlap rather than confusion with another episode |
| Episodes unresolved | Episode ID | `item.needs_review=true`, episodes with `episode=0` | Placeholder names in review_dir |
| Episode sequence gaps | Episode ID | Non-sequential episode numbers in `envelope.episodes[]` | Missing episodes or matching error |
| Per-episode rip failure | Ripping | `envelope.assets.ripped[]` with `status: "failed"` | Episode missing from pipeline |
| Per-episode encode failure | Encoding | `envelope.assets.encoded[]` with `status: "failed"` | Episode won't appear in Jellyfin |
| Per-episode subtitle failure | Subtitles | `envelope.assets.subtitled[]` with `status: "failed"` | Episode missing subtitles |
| Cross-episode resolution mismatch | Encoding | Different resolutions across `media[]` entries | Inconsistent quality |
| Cross-episode audio mismatch | Encoding | Different audio stream counts across `media[]` entries | Inconsistent audio tracks |
| Transcription cache miss on retry | Subtitles/EpisodeID | `decision_type=transcription_cache` with `decision_result=miss` on re-processed item | Re-transcription wasted GPU time |
| Fingerprint fallback used | Identification | `decision_type=fingerprint_strategy` with `decision_result=fallback` | Disc type detection degraded |
| TMDB match rejected | Identification | `decision_type=tmdb_match` with `decision_result=rejected` | No content match found |
| Validation failed but continued | Encoding | `decision_type=validation_failure_route` with `decision_result=flagged_for_review` | Item routed to review |
| Commentary tracks lost in refinement | Audio Analysis | `decision_type=commentary_remapping` with remapped count 0 | Commentary detection effort wasted |
| Source stage fallback to encoded | Organization | `decision_type=source_stage_selection` with `decision_result=encoded` when subtitles enabled | Subtitles may be missing from output |
| Audio selection non-english fallback | Audio Analysis | `decision_type=audio_selection` with `decision_result=fallback_non_english` | Primary audio track is not English |
| Commentary disposition applied | Audio Analysis | `decision_type=commentary_disposition` with `decision_result=applied` | Commentary tracks marked in output |
| KeyDB lookup miss | Identification | `decision_type=keydb_lookup` with `decision_result=miss` | Disc ID not in KeyDB, fallback to title parsing |

### DEBUG-Only Patterns

These appear in `logs.decisions` only when debug logs are available (`logs.is_debug=true`):

| Pattern | Stage | `decision_type` |
|---------|-------|-----------------|
| TMDB candidate scoring | Identification | `tmdb_search` (final selection now visible at INFO as `tmdb_match`) |
| Placeholder episode creation | Identification | (visible in `envelope.episodes` with `episode=0`) |
| Audio candidate scoring | Audio Analysis | Individual candidate scores visible only at DEBUG (selection result is INFO) |
| OpenSubtitles request details | Episode ID | reference search query params and result counts |
| LLM retry details | Various | individual retry attempt timing |
| Content ID candidate selection | Episode ID | `contentid_candidates` |
| Content ID match scores | Episode ID | `contentid_matches` |
| OpenSubtitles reference search | Episode ID | `opensubtitles_reference_search` |

## Audit Report Format

**Only include sections applicable to the item's stage gate.** Omit sections for stages the item never reached. For failed items, the report should focus on diagnosing the failure rather than listing empty sections.

### Presentation Density Guidelines

The analysis must remain exhaustive, but the *presentation* should be proportional to findings. Use compact formats for clean data and expand only where anomalies exist.

**Issues Found actionability:**
- Only put items in **Issues Found** when there is a real defect, user-visible impact, review/failure routing, an unexpected mismatch, or a near-threshold condition worth monitoring.
- Do not promote normal telemetry into an INFO finding. If no corrective action is needed, keep it in the relevant Artifact Analysis section as neutral context or omit it.
- Use `[INFO]` findings sparingly for unusual/borderline observations, not for expected below-threshold QC flags.

**Cross-episode data (TV):**
- Build the majority profile line directly from `analysis.episode_consistency.majority_profile` and deviation list from `analysis.episode_consistency.deviations`
- When all episodes match (`majority_count == total_episodes`), use a single summary line:
  `"All 12 episodes: AV1 1436x1080, 1x Opus mono eng, 1x subrip eng"`
- Only expand to a per-episode table when `deviations` is non-empty, and only show the differing fields
- Note: for TV items, `media[]` only contains the representative probe, deviation probes, and error probes. Use `media_omitted` to report how many clean probes were compressed. The representative probe has `representative: true`.
- Duration and size ranges come directly from `analysis.media_stats`: `"Duration: 1485-1520s | Size: 292-557 MB"`

**Decision traces:**
- `analysis.decision_groups` already provides the deduplication -- groups with nil `entries` are identical repeats (show as `"type x{count}: result (reason)"`), groups with `entries` have varying messages
- Only expand individual entries for decisions with different outcomes, notable parameter variations, or anomalous confidence/scores
- For episode matches below 0.90, use `confidence_quality` and margins from `episode_match` extras to distinguish true ambiguity from `decisive_low_similarity` (strong margins, weaker transcript overlap). Do not file a finding for `decisive_low_similarity` when margins are strong and no review routing occurred.

**Episode manifest:**
- Always show the full per-episode table with confidence scores, matched episode numbers, and titles. Episode identification is a core pipeline feature and the manifest is the primary evidence of correctness. This table is never compressed.

**External validation:**
- When all checks confirm, use a compact paragraph rather than multi-level section/subsection structure
- Only expand into detailed comparison when a mismatch is found

**Do not report as findings (these are normal):**
- Non-sequential disc title ordering — disc layout varies by manufacturer and is irrelevant once content ID resolves episodes
- Inconsistent source audio track counts across titles on the same disc — different playlists routinely carry different language sets
- Audio refinement stripping non-English tracks — that's its job
- Subtitle `qc_observations` that are below review thresholds and have `validation_result=passed`

**Stage timing:**
- Always show the timing table — it's compact and useful for spotting anomalies

### Report Template

```
## Audit Report for Item #<id>

**Title:** <item.disc_title>
**Stage:** <item.stage> | **Progress:** <item.progress_stage>/<item.progress_percent> | **NeedsReview:** <item.needs_review> | **ReviewReason:** <item.review_reason>
**Media Type:** <stage_gate.media_type> | **Source:** <stage_gate.disc_source>
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
- Title selection (movie): <feature-length title count, which was selected, durations of candidates>
- Anomalies: <any detected>

#### Episode Identification (if phase_episode_id)
- Content ID method: <envelope.attributes.content_id.method>
- Episodes synchronized: <envelope.attributes.content_id.episodes_synchronized>
- Confidence overview: <from analysis.episode_stats: min/max/mean, below thresholds, unresolved count>
- Episode manifest: <full per-episode table with confidence scores; if `placeholder_only=true` and episodeid has not run yet, label this as a placeholder episode inventory>
- Sequence continuity: <analysis.episode_stats.sequence_contiguous, episode_range>

#### Encoded File (if phase_encoded)

**Movie:**
- Video: <codec> <resolution> <HDR status> | Duration: <seconds>s | Size: <bytes>
- Audio: <stream summary>
- Encoding config: <encoding.snapshot.quality> | SVT-AV1 preset <encoding.snapshot.preset> | tune <encoding.snapshot.tune> | <encoding.snapshot.audio_codec>
- Crop: <analysis.crop_analysis.filter> (<analysis.crop_analysis.standard_ratio>)
- Validation: <passed/failed, expand individual steps only if failed>

**TV:**
- Common profile: <from analysis.episode_consistency.majority_profile>
- Encoding config: <encoding.snapshot.quality> | SVT-AV1 preset <encoding.snapshot.preset> | tune <encoding.snapshot.tune> | <encoding.snapshot.audio_codec>
- Duration: <analysis.media_stats.duration_min_sec>-<max>s | Size: <analysis.media_stats.size_min_bytes>-<max>
- Cross-episode consistency: <analysis.episode_consistency — pass if no deviations, else list deviations>
- Failed episodes: <count, with details if > 0>

#### Subtitles (if phase_subtitles)
- Tracks: <count and config from media probes>
- Labels correct: <yes/no>
- Validation result: <from subtitle_generation_results.validation_result; list review_issues/severe_issues only when populated>
- QC observations: <optional neutral summary; omit if uninteresting and below thresholds>
- Subtitle mux/output: <mux status and single-display-subtitle checks>

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

### Post-Ripping (phase_rip_cache)
- [ ] Analyzed rip cache metadata
- [ ] If TV: validated per-episode ripped assets in `envelope.assets.ripped`

### Post-Episode-Identification (phase_episode_id)
- [ ] Checked content ID method in `envelope.attributes`
- [ ] Reviewed episode manifest with MatchConfidence scores
- [ ] Verified episode sequence continuity
- [ ] Checked `content_id_matches` attribute completeness
- [ ] Verified `envelope.attributes.content_id.episodes_synchronized` flag

### Post-Encoding (phase_encoded, phase_crop)
- [ ] Analyzed streams from `media[]` entries (video, audio, subtitle)
- [ ] Validated crop detection from `encoding.snapshot.crop_filter`
- [ ] Verified commentary labeling
- [ ] If TV: checked cross-episode consistency

### Post-Audio-Analysis (phase_commentary)
- [ ] Reviewed commentary decisions from `logs.decisions`
- [ ] If TV: verified cross-episode audio stream count consistency

### Post-Subtitling (phase_subtitles)
- [ ] Verified subtitle tracks in media probes
- [ ] Verified subtitle track labels
- [ ] If TV: checked per-episode subtitle asset status

### External Validation (phase_external_validation)
- [ ] Looked up blu-ray.com review
- [ ] Validated crop and commentary count against review

### Report
- [ ] Generated report with only applicable sections
- [ ] Applied presentation density guidelines (compact for clean data, expanded for anomalies)
- [ ] Used `analysis.decision_groups` for decision trace
