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

The goal is to **uncover problems that automated code does not detect**. Quick log scans saying "no warnings, no errors" are insufficient. This skill performs deep analysis of the applicable artifacts to find anomalies.

**Subtitle content is out of scope.** Never read, extract, sample, quote, compare, or judge subtitle/transcript cue text. The pipeline adopts a verified OpenSubtitles download (cleaned and retimed against the rip's WhisperX transcript) or skips subtitles for that title. Item audits check only subtitle pipeline integrity and metadata (adoption/skip outcome, validation verdicts, routing, assets, muxing, stream format/dispositions/labels). If subtitle wording is questioned, recommend re-running `spindle subtitle <mkv>` (retries adoption; useful after better uploads appear) or the whisperx-subtitles skill; do not run those modifying commands during an item audit.

## Audit Procedure

### Phase 1: Gather Artifacts

Run:

```bash
spindle queue audit <item_id>
```

This prints a deterministic **text digest** to stdout and writes the **full JSON report** to `spindle-audit-<item_id>.json` in the system temp dir (exact path shown in the digest header). The digest is rendered by Spindle itself — do not write your own extraction script. It contains: item/task status, stage gate, gathering errors, pre-flagged anomalies, all warnings and errors with context, stage timings, all captured events, every decision (grouped, with timestamps), and the pre-computed analysis summaries. Any cap or truncation in the digest is explicit ("+N more in JSON", "truncated") — nothing is silently dropped.

**The digest is the starting point, not the audit.** Drill into the full JSON for raw ffprobe streams (`media[].probe`), full envelope detail (`envelope.titles/assets/attributes`), untruncated extras values, and anything the digest marks as omitted. Investigate every anomaly, warning, error, and suspicious value before reporting.

Drill-down mechanics: pass Python via a single-quoted heredoc (`python3 << 'PYEOF'` ... `PYEOF`) and `json.load(open(path))` — never `python3 -c "..."` (bash mangles `!` and `\` inside double quotes) and never pipe the file into the heredoc's stdin (the heredoc consumes stdin).

If `/itemaudit` is invoked without an item ID, run `spindle status` and `spindle queue list` to diagnose daemon-level issues instead.

### Full JSON Schema

The JSON report schema may evolve with this skill; treat it as diagnostic input rather than a stable public API. It contains:

- **`item`**: Queue item summary (`stage`, `tasks[]` per-task state/progress, review flags, paths, timestamps). `item.stage` is the scheduler's coarse position and lags running tasks during rip/encode overlap -- read `item.tasks[]` for what is actually happening (`type`, `state`, `attempts`, `error`, `progress_percent`, `progress_message`, `active_asset_key`)
- **`stage_gate`**: Pre-computed phase applicability (which analyses apply, resolved media type, media hint, disc source)
- **`logs`**: Parsed log entries — warnings and errors (with `extras` maps of non-standard log fields), item-specific INFO events/progress (`logs.events` with `event_type` and extras), and stage timing events (`ts`, `event_type`, `stage`, `duration_seconds`). Gathered from every daemon log file overlapping the item's lifetime (`logs.paths` — daemon restarts mid-item span multiple files), clamped to the item's creation time so reused item IDs / re-ripped discs don't leak earlier runs' lines. Flooding `*_progress` event types are downsampled (first/last/evenly-strided, ~20 per type); `logs.events_omitted` counts dropped ticks — it is normal on long encodes, not data loss. Decisions are NOT in `logs` — they live in `analysis.decision_groups`.
- **`rip_cache`**: Cache metadata (disc title, cached_at, title_count, total_bytes). Serialized `rip_spec_data` and `metadata_json` blobs are omitted (already in parsed `envelope`). `disabled: true` means the cache is turned off in config — do not report that as a pruned entry.
- **`envelope`**: Parsed ripspec Envelope (titles, episodes, assets at each stage, attributes)
- **`encoding`**: Encoding details snapshot (crop, validation, config, result). Spindle always uses Reel target-quality mode, so the snapshot carries the full Reel-reported config summary (`encoder`, `quality`, `preset`, `tune`, `audio_codec`) plus crop and validation (pass/fail) — nothing is omitted
- **`media`**: ffprobe output for encoded files. For TV, only the representative probe (matching majority profile, marked `representative: true`), deviation probes, and error probes are included. `media_omitted` indicates how many clean probes were dropped.
- **`errors`**: Any gathering errors (missing logs, parse failures, etc.)
- **`analysis`**: Pre-computed summaries — decision groups, episode consistency, crop analysis, episode stats, media stats, asset health, the apply stage's final-output validation verdict, anomaly flags (see Analysis Reference below)

**The `stage_gate` object tells you exactly which phases to run.** Each `phase_*` boolean is pre-computed from the item's task states (which lead the coarse item stage during rip/encode overlap), media type, and disc source. Do not re-derive these — trust the gate.

### Analysis Reference

The `analysis` object (always present; sub-fields omitted when empty) contains pre-computed summaries:

| Field | Present When | Contents |
|-------|-------------|----------|
| `decision_groups` | Decisions exist | Groups by (type, result, reason) with count, in log order. `entries` always carries every grouped decision with its timestamp — this is the only record of individual decisions and their spacing. |
| `notable_decisions` | Notable decisions exist | Curated subset of decisions most useful for reporting (TMDB/title/crop/validation/source normalization/audio/subtitle/routing/episode match), avoiding noisy full decision scans. |
| `stage_timings` | Stage events exist | One row per stage with start, completion, duration, start count, and completion count. Prefer this over raw `logs.stages` for the timing table. |
| `source_summary` | Source/output traits known | Disc source, UHD-likely flag, input/output resolution, input codecs, output codec, HDR/dynamic range. |
| `title_selection` | Movie titles exist | Feature-length candidates, selected title, selection decision/reason, and similar-runtime candidate count. Prefer this over hand-parsing `envelope.titles`. |
| `output_media` | Valid probes exist | Compact stream summaries (video/audio/subtitle titles, languages, and dispositions) derived from ffprobe. Prefer this for normal stream checks; use raw `media[]` only for missing details. Label and disposition correctness is judged by `final_validation`, not here. |
| `final_validation` | The apply stage ran | The pipeline's own verdict on each delivered output, copied from `envelope.attributes.final_validation`. Per-output entries carry `output_path`, `passed`, `failed_checks[]`, an `error` when the file could not be probed, and an `av_sync` block (source/output A/V offsets, signed drift in milliseconds, pass/fail at 100 ms). The apply stage probes the delivered file against the ripped source after every rewrite, so this stays independent of Reel's persisted validation verdict. |
| `audio_summary` | Audio evidence exists | Primary track, output/excluded/commentary counts, and commentary decisions. Whether the labels are correct comes from `final_validation`. |
| `subtitle_summary` | Subtitle evidence exists | Subtitle pipeline metadata: per-title source (`opensubtitles`/`none`), validation counts, skipped count, and output subtitle count. Stream layout and label correctness come from `final_validation`. It is not evidence for auditing subtitle text. |
| `routing_summary` | Final assets exist | Display-only classification of each final output's destination and its expected-vs-actual route. The organizer enforces routing itself and fails the stage on a mismatch, so this table is context, not the check. |
| `episode_consistency` | 2+ TV probes | `majority_profile` (video_codec, width, height, audio_streams, subtitle_streams with codec/language/is_forced), `majority_count`, `total_episodes`, `deviations[]` with human-readable differences. |
| `crop_analysis` | Crop data exists | `filter`, `output_width/height`, `aspect_ratio`, `standard_ratio`, `required`. |
| `episode_stats` | Episodes exist | `count`, `matched`, `unresolved`, `placeholder_only`, `confidence_min/max/mean`, `below_070/080/090` (cumulative), `sequence_contiguous`, `episode_range`. |
| `media_stats` | Valid probes exist | `file_count`, `duration_min_sec/max_sec`, `size_min_bytes/max_bytes`. |
| `asset_health` | Assets exist | Per-stage (ripped/encoded/subtitled/final/transcript) `total/ok/failed/muxed` counts. `transcript` counts the shared per-episode WhisperX transcript artifacts reused across episode-ID, commentary, and subtitle generation. |
| `anomalies` | Issues/context detected | Pre-flagged signals with `severity` (critical/warning/info), `category`, `message`. |

**Use critical/warning `analysis.anomalies` as a starting checklist for Issues Found.** Info-level anomalies, if present, are context only unless investigation shows real user impact. Each anomaly is a machine-detected flag -- the LLM's job is to investigate context, assess impact, reject false positives, and add judgment-based findings the code cannot detect.

### Stage Gating

The `stage_gate` object in the audit output contains:

| Field | Meaning |
|-------|---------|
| `furthest_stage` | Status the item reached (or failed at) |
| `media_type` | Resolved media type: `movie`, `tv`, or `unknown`. `unknown` means identification has not completed or failed outright — it can never belong to an item that ripped |
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
- **No-TMDB-match is fatal at identification for every disc.** Expect these items to fail before ripping rather than continue as degraded unknown-media-type review items. An item that reached ripping therefore always has `media_type=movie` or `tv`; `media_type=unknown` means the item failed at (or has not yet finished) identification. A ripped item carrying `unknown` is itself a finding — the ripper rejects that media type outright.
- **A failed TMDB search is retried once on a narrowed title.** `decision_type=tmdb_search` with `decision_result=retry_narrowed` shows an edition or cut suffix being stripped ("Mary Poppins 50th Anniversary Edition" -> "Mary Poppins") after the first query found nothing. Its presence is normal recovery, not a defect. When an item still fails, read `event_type=tmdb_no_match`: `error_hint` distinguishes "TMDB returned no results for the query" (query pollution — check `query_title` against the disc label) from "no result met confidence threshold" (candidates existed but scored too low — check the `tmdb_search` candidate scores at DEBUG), and `result_count` confirms which.

### Phase 2: Log Analysis (when `phase_logs` is true)

Analyze `analysis.decision_groups`, `logs.events`, `logs.warnings`, `logs.errors`, and `logs.stages`. **Go beyond simple error counts.**

1. **Decision anomalies** (from `analysis.decision_groups`):
   - Low confidence scores on decisions that were accepted anyway
   - Unexpected fallbacks (encoding retries)
   - Decisions that contradict expected behavior for the content type
   - Look up groups by `decision_type` to find specific categories (`commentary_classification`, `tmdb_match`, etc.)
   - Infrastructure decisions to check: `decision_type=tmdb_match` (acceptance/rejection), `decision_type=title_resolution` (source priority), `decision_type=fingerprint_strategy` (disc type detection), `decision_type=disc_id_cache` (cache hit/miss), `decision_type=duplicate_detection` (duplicate guard), `decision_type=episode_id_skip` (episode-ID skips), `decision_type=rip_cache` (hit/miss/incomplete — misses log explicitly), `decision_type=keydb_refresh` (point-of-use catalog freshness)
   - `decision_type=source_timeline_normalization` with `decision_result=bounded_to_video` means Reel detected audio in the MakeMKV rip extending materially beyond the video endpoint and bounded it during encoding. Treat it as corrected source-artifact context, not a delivered-output defect, when Reel validation and apply final validation both pass. If either validation fails, report the remaining endpoint mismatch.
   - A WARN `event_type=keydb_download_error` means identification continued with a stale KeyDB catalog. Always report it as a **WARNING** because a newly added or corrected disc title may have been missed. A `keydb_refresh` decision with `decision_result=catalog_stale` followed by a successful `keydb_download_complete` is normal recovery, not a finding.
   - Movie title selection: `decision_type=title_selection_funnel` records each elimination stage (rule, `candidates_before/after`, `eliminated_title_ids`, `evidence` with the threshold values); the winner is the `decision_type=title_selection` "primary title decision" line. When the wrong cut/title was picked, the funnel shows which rule eliminated the right one.
   - Scheduler resource waits: `decision_type=stage_execution` with `decision_result=blocked` / `unblocked` shows a task waiting on GPU/drive/encode claims (`claims` attr) and the `waited` duration on grant. "stage started" lines also carry the resolved `claims` (so the GPU-for-TV choice is visible per dispatch). The `encode` claim has capacity 1 — encodes never run concurrently. A movie's encoding task takes that claim when identification completes and then polls without work until its rip finishes (`encoding_plan` logs `decision_result=deferred` for this; TV logs `streaming`). That idle hold is by design and is NOT a finding: ready tasks are ordered by item `created_at`, so the claim always goes to the oldest item still needing it, and under sequential ripping that is also the item whose rip completes first.
   - Warnings/errors include `extras` maps with non-standard log fields for diagnostic context; decisions use structured fields only (full log lines available at the files in `logs.paths`)

2. **Timing/progress anomalies** (from `logs.stages` and `logs.events`):
   - Stages taking unusually long or short (use `duration_seconds` when available)
   - Large gaps between stage events suggesting hangs
   - Repeated retry attempts
   - Use `logs.events` for long-running work visibility: `encoding_progress`, `rip_progress`, `copy_progress`, `transcription_extract[_complete]`, `transcription_whisperx[_complete]`, `commentary_llm_start/_complete`, `mux_start/_complete`, `loom_scan_start`, and plan events such as `*_plan`
   - Encode lifecycle events: `encode_init` (input resolution/dynamic range), `encoder_config` (preset/quality and full `svtav1_params` — check level/mbr cap here for playback-compat questions), `encoding_substage` (reel pipeline phase: chunking/encoding/merging/muxing), `encode_result` (sizes, wall time, speed). `encoding_progress` carries `bitrate` and `chunks_complete/chunks_total`.
   - Item lifecycle: `event_type=item_complete` is the one-line completion summary (per-stage `<stage>_duration` attrs plus `total_wall_time`); `event_type=operator_action` records user-initiated retry/stop/remove/clear/disc-pause; `event_type=startup_queue_state` shows what a daemon restart resumed.
   - Level layout: the stage executor emits exactly one `stage_start`/`stage_complete` event pair per stage run at DEBUG ("item stage derived" is also DEBUG; all still present in the log file and in gather output); the INFO narrative is the workflow "stage started/completed" decision pair. `stage_duration` (on the executor's `stage_complete` and the workflow decision log) is a human-readable Go duration string.
   - Transcription is BATCHED: expect one `transcription_whisperx[_complete]` pair per batch (with a `batch_files` extra), not one per episode; `transcription_extract` still fires per file. A missing per-episode WhisperX event is not an anomaly.
   - Episode-ID reference fetching runs CONCURRENTLY with transcription (`decision_result=fetch_overlapped`), so OpenSubtitles and WhisperX log lines legitimately interleave — do not flag the interleaving as disorder.
   - Rip-cache restores and stores hardlink when cache and staging share a filesystem: near-instant `copy_progress` (a single jump to 100%) is expected, not a truncated copy.

3. **Data flow anomalies**:
   - Track counts changing unexpectedly between stages
   - Reconcile TV counts across `makemkv_scan_complete.titles_found`, title-selection decisions, `episode_placeholders`, the episode manifest, and ripped assets. A contiguous resolved sequence does not prove the first or last episode is present.
   - Title-level TV deduplication only runs on Blu-ray segment maps: DVD maps are title-local and TitleHash is metadata-only, so identification refuses to dedup non-Blu-ray titles at all. A `duplicate_detection` decision carrying `title_id`/`duplicate_of` on a DVD should never appear — if one does, treat it as a CRITICAL missing-episode risk and a bug in the dedup gate.
   - Episode counts not matching expectations
   - File sizes that seem wrong for the content

4. **LLM decision review** (from `analysis.decision_groups`):
   - `decision_type=commentary_classification` entries
   - `decision_type=tmdb_match` and `decision_type=tmdb_match_preference` entries — verify acceptance thresholds are reasonable
   - Evaluate if confidence levels and reasons make sense for the content
   - Exclude subtitle wording from this review; only aggregate subtitle pipeline outcomes belong in this audit

5. **TV episode pipeline checks** (TV only, from `analysis.decision_groups`, `logs.warnings`, and stage events):
   - Stage events with `stage=episode_identification` — verify the stage started/completed or identify where it failed
   - `decision_type=episode_id_skip` entries — explain legitimate skips for non-TV content
   - `decision_type=episode_placeholders` — confirm placeholders were created before content ID
   - `event_type=episode_id_no_transcripts` or item review/error messages about missing references — degraded episode-ID failures
   - `event_type=low_confidence_match` — episodes with `MatchConfidence` below 0.70
   - `decision_type=contentid_matches` — final episode-to-reference matching results; compare `ambiguous_rips`, `decisive_low_similarity_rips`, and `contested_rips`
   - `decision_type=episode_match` with `decision_result=<key> -> unresolved` — per-rip unresolved lines carry `best_candidate_episode/score/confidence` and runner-up; the full pending-claim matrix is at DEBUG (`content ID pending claim`)
   - TV title exclusions carry their evidence: `outlier_bar_seconds`/`weighted_median_seconds` on `gross_runtime_outlier`, `expected_runtimes_seconds` on `expected_runtime_mismatch`/`over_expected_episode_count` — compare the excluded title's `duration` against these to judge the exclusion
   - `decision_type=reference_search` — reference subtitle candidate quality and suspect/fallback selections
   - Asset keys are PERMANENT placeholder identifiers (stable-key model): `episodeid` never renames `s01_001`-style keys. Episode identity lives in `envelope.episodes[]` fields (`season`, `episode`, `episode_end`) -- join assets to episodes by key and read identity from those fields. Placeholder-looking keys in logs, review reasons, and final routing are correct, not a defect
   - **Do not stop at episode-ID quality.** If organizer/review routing is implicated, compare per-episode review state against final destinations.

### Phase 3: Rip Cache Analysis (when `phase_rip_cache` is true)

Analyze the `rip_cache` section from the audit output:

1. **Verify** `rip_cache.found` is true — if false, check `rip_cache.disabled` first (cache turned off in config); otherwise the entry may have been pruned
2. **Check metadata**:
   - `disc_title` matches expected content
   - `cached_at`, `title_count`, and `total_bytes` look plausible
3. **Title selection analysis** (movies only):
   - Prefer `analysis.title_selection` for candidate counts, selected title, similar runtimes, and selection decision; fall back to `envelope.titles` only when the summary is absent
   - Feature-length titles are titles with `chapters > 1` AND `duration > 3600` seconds
   - The pipeline uses multi-stage selection (`ChoosePrimaryTitle`), not simply the longest title:
     - **Disney multi-language detection**: when 2+ feature-length 800-series playlists (00800-00899) exist with runtimes within 30 seconds, the pipeline prefers the lowest playlist number (00800.mpls = English). The selected title may be *shorter* than alternatives — this is correct behavior for Disney/Pixar/Marvel/Star Wars multi-language discs where language variants differ only in localized title cards and credits.
     - **Different cuts**: when 800-series playlists differ by >30 seconds, treated as different cuts (theatrical vs director's) and longest is preferred.
     - Additional tiebreakers: chapter count, MPLS over M2TS, segment count, TitleHash fingerprint frequency.
   - Check `decision_reason` in the decision groups: `"primary_title_selector"` indicates the multi-stage algorithm was used.
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
5. **Asset mapping strategy**: Check `decision_type=asset_mapping` — `title_file_map` is the normal path for TV, `directory_scan` is the fallback

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
   - For `decision_type=episode_match` groups, inspect `confidence_quality` before treating an accepted score as risky. `decisive_low_similarity` means text similarity is lower than a clear match but runner-up margins are strong enough for deterministic acceptance; it should not require `decision_reason=llm_verified`. `ambiguous` means margins were not decisive. `contested` is review-worthy.

3. **Canonical match outcomes live in `episodes[]`**:
   - Verify all episodes have sensible resolved/unresolved state
   - Review `match_confidence`, `needs_review`, and `review_reason` per episode
   - Minimum accepted similarity score is 0.58 — scores near this floor warrant scrutiny

4. **Episode sequence continuity**: `analysis.episode_stats.sequence_contiguous` and `episode_range` are pre-computed. If not contiguous, inspect `envelope.episodes[]` for gaps or duplicates indicating matching errors

### Phase 3c: Final Output Routing Validation (post-organizing items, especially TV with review flags)

The organizer enforces routing itself: after copying, it re-derives each output's expected destination from the review flags (TV: resolved + no episode review flag -> library, otherwise review; movie: the item's review flag) and fails the organize stage when a recorded final asset is missing or sits under the wrong root. A misrouted item therefore surfaces as a FAILED item at `organizing`, not as a quietly wrong library.

1. **Read `envelope.assets.final`** and map final paths by `episode_key`. The digest's "Final routing" section shows expected-vs-actual per output for display.
2. **If the item failed at `organizing` with `routing verification failed`**, that message names the keys and the expected root: diagnose why the flags and the routing branch disagree (it indicates an organizer bug, not a content problem).
3. For a completed item, confirm the routing summary agrees with the per-episode review flags; a disagreement here means the check and the summary disagree, which is itself a finding.
4. If the structured audit data is incomplete or suspicious, **inspect the actual directories on disk** rather than assuming the envelope tells the whole story.

### Phase 4: Encoded File Analysis (when `phase_encoded` is true)

Analyze the `media` array from the audit output. Each entry contains full ffprobe results.

**TV note:** The encoding snapshot only contains data for the last episode encoded (the snapshot is overwritten per-episode during encoding). The `media[]` array is compressed for TV: only the representative probe (matching the majority profile, marked `representative: true`), deviation probes, and error probes are included. Use `media_omitted` to see how many clean probes were dropped. The representative probe is sufficient for stream-level checks (items 2-6 below); `analysis.final_validation` carries a per-output verdict for every episode regardless of probe compression, and `analysis.episode_consistency` confirms all omitted episodes match the same profile. The snapshot is still useful for crop detection, encoding config, and validation results (which are consistent across episodes from the same disc).

**For movies** (single entry) or **the representative probe for TV**:

1. **Check the pipeline's final-output verdict** in `analysis.final_validation`:
   - The apply stage probes each delivered file after every rewrite and compares the primary audio's offset relative to video against the ripped source; absolute drift over 100 ms fails the output and routes it to review. This is measured independently of Reel's persisted `encoding.snapshot.validation` result — never accept the Reel validation line as proof of sync by itself, but do not recompute the comparison here either: the ripped source is deleted with staging when the item completes.
   - Do not re-derive the verdict. Verify it exists, then investigate every entry with `failed_checks` (each names the invariant that broke) and correlate it with the episode's review reasons and the `final_validation` decision logs.
   - A negative `av_sync.drift_milliseconds` means output audio moved earlier; positive means it moved later.
   - An entry with `error` (or `av_sync.error`) is UNAVAILABLE, not passing: the file or its source could not be probed. Investigate rather than claiming the output is verified.
   - A missing `analysis.final_validation` on an item whose outputs were organized is itself a finding — the apply stage always persists a verdict.

2. **Verify video stream** (from `media[].probe.streams` where `codec_type=video`):
   - Resolution matches expected (SD/HD/4K)
   - Codec is AV1 (`av1`) from Reel's SVT-AV1 (libsvtav1) encoder; libaom-av1 cannot occur
   - Duration matches source within tolerance (~1-2 seconds)
   - Static HDR signaling present if expected (`color_primaries`, transfer characteristics, and mastering metadata)
   - Reel intentionally does not preserve HDR10+ dynamic metadata (SMPTE ST 2094-40) because the target playback environment does not consume it. An HDR10+ source producing a static-HDR AV1 output is expected; do not flag missing HDR10+ side data or an external HDR10+ vs output static-HDR difference.

3. **Verify audio streams** (verdict first, then `media[].probe.streams` where `codec_type=audio`):
   - The apply stage already enforces this layout on the delivered file: exactly the refinement plan's track count, stream 0 default and no other, an English default whenever an English track exists, and commentary flags on exactly the tracks it labeled. A violation appears as a `final_validation` failed check, not as something to rediscover here.
   - Use the probe to add context the verdict cannot: unexpected stereo downmix tracks, a track count that disagrees with the disc's known audio layout, odd channel layouts.

4. **Check commentary labeling**:
   - The apply stage verifies every track it marked carries `disposition.comment=1` and a title containing "Commentary", and that no other track carries the comment flag. Read `analysis.final_validation` for the verdict.
   - The remaining judgment call is whether the right tracks were classified as commentary: cross-reference commentary decisions in `analysis.decision_groups` and, when `phase_external_validation` is true, the disc review's commentary count.

5. **Check subtitle streams** (verdict first, then `media[].probe.streams` where `codec_type=subtitle`):
   - The apply stage enforces the layout for the delivered file: an adopted title (`source=opensubtitles`) that was muxed must carry exactly one `subrip` stream with a language tag, a label naming that language, and neither the forced nor the default flag; a skipped title (`source=none`) must carry no subtitle stream. Failures appear in `analysis.final_validation`.
   - Use the probe only to explain a failed check or to look at an output the verdict marked unavailable.

6. **Parse encoding details** from `encoding.snapshot`:
   - Check `validation.passed` and individual step results
   - Review crop detection from `crop` fields
   - Check for `warning` or `error` in snapshot
   - Check encoding config: `encoder`, `quality` (Reel target-quality summary), `preset`, `tune`, `audio_codec`. These come from Reel's target-quality mode, not Spindle config (there is no `[encoding]` config block); `preset`/`quality` reflect what Reel chose internally, so there is no per-resolution CRF to verify
   - A passed validation step named `Source timeline normalization` records the source track count and maximum post-video overrun that Reel corrected. Correlate it with the structured `source_timeline_normalization` decision and the apply final verdict; do not call the delivered file defective when its endpoint checks pass.
   - Encoder-library warnings/errors surface as `event_type=reel_warning` in `logs.warnings` and `event_type=reel_error` in `logs.errors`; the persisted copy is in `encoding.snapshot.warning`/`error`
   - Check `decision_type=file_probe` for pre-encoding resolution and codec detection
   - Check `decision_type=crop_detection` for crop decision visibility
   - Check `decision_type=encoding_validation` for per-episode validation results
   - `decision_type=validation_failure_route` with `decision_result=flagged_for_review` indicates validation-failed items routed to review

7. **Per-episode asset status** (TV only, from `envelope.assets.encoded`):
   - Check for `status: "failed"` entries with `error_msg`
   - Encoding allows partial success
   - Verify encoded asset count matches episode count

8. **Cross-episode consistency** (TV only):
   - Use `analysis.episode_consistency` for the overview: `majority_profile` gives the common (video_codec, width, height, audio_streams, subtitle_streams), `majority_count`/`total_episodes` show how many match, and `deviations[]` lists episodes with human-readable differences
   - Use `analysis.media_stats` for duration range (`duration_min_sec/max_sec`) and size range (`size_min_bytes/max_bytes`)
   - Inspect the representative probe for stream-level checks (items 2-6); omitted probes are confirmed equivalent by the consistency analysis

### Phase 5: Crop Detection Validation (when `phase_crop` is true)

Analyze crop data from the audit output:

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

**Pipeline structure note:** the item template is a DAG. After
`episode_identification`, the ANALYSIS branch (`analysis` stage: per-episode
commentary detection from RIPPED sources; then `subtitling`: SRT ADOPTION
ONLY — download/clean/retime/verify into staging, never writing encoded
files) runs
CONCURRENTLY with `encoding`. The `apply` stage joins both branches and
performs every write to the encoded files: audio refinement, commentary
disposition, duration validation, sidecar placement, and subtitle muxing.
Consequences for audits:
- Log timelines legitimately interleave encoding events with analysis and
  subtitling events for the SAME item — not disorder.
- Progress is per task: each running stage writes its own `tasks[]` row, so
  `item.tasks[]` shows independent live progress for both branches during
  overlap (for example a `ripping` task and an `encoding` task both
  `state=running` with their own `progress_percent`/`progress_message`)
  instead of one arbitrated item-level progress field.
- `audio_analysis` does not exist as a stage name; commentary detection
  decisions appear under stage `analysis`, remuxes/muxing under `apply`.
- Commentary detection is PER-EPISODE (`envelope.attributes.audio_analysis
  .per_episode`), measured on ripped files; indices are remapped in apply.
- `mux_start/_complete` and subtitled assets are produced by `apply`, not
  `subtitling`.

### Phase 6: Subtitle Pipeline Integrity (when `phase_subtitles` is true)

Analyze only structural subtitle evidence from `media[].probe.streams` (codec_type=subtitle), `analysis.subtitle_summary`, and `envelope.assets.subtitled`. Do not open SRT files, inspect cue text, read transcripts for subtitle quality, or compare wording against audio/references. Keep this phase compact; subtitle content review is an operator action outside this audit.

**For movies** or **per-episode for TV**:

1. **Verify embedded subtitles**, primarily from `analysis.final_validation`:
   - The apply stage already reconciles each output's subtitle streams against that title's adoption record: one `subrip` stream, language tag present, label naming the language, no forced flag, no default flag for an adopted-and-muxed title; no subtitle stream at all for `source=none`. Read the verdict rather than re-deriving it from `media[]`.
   - A mux failure falls back to a sidecar SRT that Loom ignores, so it flags the episode for review with a `final_validation`/`subtitle_mux` review reason. Report it as a real defect, not a cosmetic one.
   - Never treat Matroska's subtitle `tags.DURATION` as the subtitle's absolute end timestamp. It is the cue span (`last cue end - first cue start`). For a suspected tail gap, use ffprobe packet metadata for the subtitle stream and calculate `max(pts_time + duration_time)`. Do not inspect packet payloads or cue text. A gap from that timestamp to video duration is not itself a finding: valid display subtitles stop before long credits, and sparse WhisperX end-credit hallucinations can extend the raw reference. For an adopted track, trust a `reference_tail_gap_s` at or below the 600-second gate unless other structural validation failed.

2. **Subtitle adoption outcome** (from `analysis.subtitle_summary`, `envelope.attributes.subtitle_generation_results`, and `analysis.decision_groups`):
   - The pipeline downloads the identified title's OpenSubtitles candidates, cleans them, retimes against the rip's WhisperX transcript with ffsubsync, and adopts the first candidate that passes the verification gate. When no candidate verifies (or none exists, or the title is multi-episode), it records `source=none` and the title completes WITHOUT subtitles. Spindle never generates subtitles itself.
   - `decision_type=subtitle_source` is the core trace: `decision_result=adopted` (reason carries the candidate and gate metrics), `candidate_rejected` per rejected candidate (reason explains which gate failed), and `skipped` (reason explains why nothing was adopted). A skip also emits WARN `event_type=subtitle_skipped` and a pre-flagged warning anomaly ("N title(s) completed without subtitles"). Report a skip as a WARNING with the rejection reasons as evidence — the expected recovery is the whisperx-subtitles skill (or `spindle subtitle` after better uploads appear), not a pipeline retry.
   - `decision_type=subtitle_duration_source` shows whether the verification gate measured video duration from ffprobe or fell back to the transcript span.
   - `decision_type=subtitle_mux` with `decision_result=skipped` indicates muxing was disabled in config.
   - `decision_type=transcription_asset` and `decision_type=transcription_profile` show which asset/profile WhisperX processed for the sync reference. Use `logs.events` entries (`transcription_extract_complete`, `transcription_whisperx_complete`, `transcription_complete`) for transcription timing before falling back to the raw files in `logs.paths`.
   - `decision_type=subtitle_transcript_source` with `decision_result=artifact_reused` means the stage reused the shared per-episode transcript artifact (`envelope.assets.transcript`) and ran no WhisperX of its own — absent transcription events in the subtitling stage are then expected, not a defect. For TV, verify transcript asset count matches episode count in `analysis.asset_health`.
   - Additional subtitle tracks, forced dispositions, and "Forced" subtitle labels fail the apply stage's layout check and route the output to review; if you see them without a matching `final_validation` failure, the output was not produced by the current pipeline (a stale file) — say so.

3. **Per-episode subtitle asset status** (TV only, from `envelope.assets.subtitled`):
   - Check for `status: "failed"` entries with `error_msg`
   - Verify `subtitles_muxed` flag per episode
   - Check `envelope.attributes["subtitle_generation_results"]` for per-episode details
   - Treat `validation_result` as the actionable summary: `passed` is clean, `needs_review` is actionable, and `skipped` accompanies `source=none` and is the no-subtitle outcome, not a failure. A severe issue rejects the candidate before any record is written, so an adopted record never reads `failed`
   - Treat `qc_observations` as telemetry only. Do not list below-threshold observations (for example `high_reading_speed`, `short_cue_duration`, `long_cue_duration`) as Issues Found unless they also appear in `review_issues`/`severe_issues` or caused review routing.

4. **Cross-episode subtitle consistency** (TV only):
   - Adopted episodes should share the same subtitle language and single-display-subtitle layout; a mix of adopted and skipped episodes on one disc is possible and each skip should have its own `subtitle_source` trace

### Phase 7: Commentary Track Validation (when `phase_commentary` is true)

Analyze commentary decisions from `analysis.decision_groups` and audio streams from `media[]`:

1. **From decisions**: Find `decision_type=commentary_classification`, `commentary_stereo_filter`, `commentary_remapping`, and `commentary_disposition` groups
2. **Expected behavior**:
   - 2-channel English tracks that aren't stereo downmixes should be candidates
   - High similarity to primary audio = stereo downmix (excluded)
   - LLM should classify based on content
   - Each candidate is transcribed ONCE (batched WhisperX invocation); the same transcript feeds both the similarity filter and LLM classification — there is no separate classification transcription or separate commentary model
   - The primary track fingerprint comes from the shared transcript artifact when one exists (`commentary_stereo_filter` with `decision_result=artifact_reused`); otherwise the primary is transcribed once and recorded as the artifact (`envelope.assets.transcript`)
   - If the whole candidate batch transcription fails, ALL candidates are conservatively marked commentary (`reason: "transcription failed"`) — report the batch failure as the root cause, not per-track defects

3. **Refinement impact**: Check `decision_type=commentary_remapping` — shows how many commentary tracks survived audio refinement. `remapped_count=0` means all commentary tracks were lost during refinement.

4. **Cross-reference with blu-ray.com** (only when `phase_external_validation` is true):
   - Check "Audio" section of disc review for commentary count
   - Compare against our detection count

5. **Verify in media probes**: Count audio streams with `disposition.comment=1` in `media[].probe.streams`

6. **Cross-episode commentary consistency** (TV only):
   - All episodes from the same disc should have same number of audio streams

## Problem Pattern Catalog

### Known Patterns to Check

| Pattern | Stage | Evidence in Audit Output | Impact |
|---------|-------|--------------------------|--------|
| Duplicate disc detection | Identification/Disc Monitor | `decision_type=duplicate_detection` groups | Item rejected or enqueue skipped |
| TMDB match rejected or weakly accepted | Identification | `decision_type=tmdb_match` groups with score/threshold details in `decision_reason` | Wrong title match, or item failed at identification |
| Unresolved placeholder episodes | Episode ID | `envelope.episodes` with `episode=0` and placeholder keys after episodeid | Episodes land in review_dir |
| Wrong crop detection | Encoding | `encoding.snapshot.crop_filter` aspect ratio mismatch vs blu-ray.com | Black bars or cut content |
| A/V sync changed after encoding | Encoding/Apply | `analysis.final_validation.entries[].failed_checks` names `av_sync drift ...`; the entry's `av_sync` block shows source vs output offsets | Audio leads or lags video; CRITICAL even if Reel's persisted validation says passed |
| MakeMKV audio extends past video | Ripping/Encoding | INFO source anomaly plus `decision_type=source_timeline_normalization` and Reel validation step `Source timeline normalization` | Corrected by Reel when `bounded_to_video` and final validation passes; otherwise report the remaining endpoint mismatch |
| Final output never verified | Apply | `analysis.final_validation` absent on an organized item, or an entry with `error`/`av_sync.error` | The delivered file shipped without the pipeline's own checks; investigate why the probe or ripped source was unavailable |
| Missing commentary | Audio Analysis | Count mismatch vs blu-ray.com review using `media[].probe.streams` | Commentary tracks not preserved |
| Unlabeled commentary | Apply | `final_validation` failed check `commentary track N ... lacks a Commentary label` or `... missing the comment flag` | Commentary track is not clearly labeled; output routed to review |
| Stereo downmix kept | Audio Analysis | Extra 2ch audio track in `media[].probe.streams` | Unnecessary audio bloat |
| Subtitle skipped | Subtitles | `subtitle_generation_results[].source` is `none`; WARN `event_type=subtitle_skipped`; pre-flagged warning anomaly | Title has no subtitles; WARNING not CRITICAL — recovery is the whisperx-subtitles skill or a `spindle subtitle` retry |
| SRT validation review | Subtitles | `subtitle_generation_results[].validation_result` is `needs_review`; `review_issues` populated; review routing present | Subtitle pipeline flagged output for separate review; do not inspect its text here |
| Subtitle tail mismatch | Subtitles | An adopted `subtitle_source` reports `reference_tail_gap_s` over 600 seconds despite the gate, or other structural validation explicitly rejected/review-routed the tail; do not use Matroska `tags.DURATION`, video-tail length, or a below-threshold raw WhisperX tail alone | Incomplete or wrong candidate adopted despite the gate |
| Extra/forced/default subtitle | Apply | `final_validation` failed check naming the stream count, the forced flag, or the default flag | Incorrect subtitle output; the apply stage routes it to review. Without a matching failed check the file is stale, not pipeline output |
| Subtitles not muxed | Apply | WARN `event_type=mux_error` with a `subtitle_mux:` review reason, or a `final_validation` failed check reporting 0 subtitle streams for an adopted title | Loom ignores sidecar subtitle files, so the delivered file has no visible subtitles; a `source=none` title with no subtitle stream is NOT this pattern |
| Unlabeled subtitles | Apply | `final_validation` failed check `subtitle label ... does not identify language ...` | Subtitle display issue; output routed to review |
| Low episode match confidence | Episode ID | `envelope.episodes[].match_confidence` < 0.70 | Episodes may be mislabeled |
| Decisive low-similarity episode match | Episode ID | `decision_type=episode_match` with `confidence_quality=decisive_low_similarity` and strong margins | Usually not a defect; explain as lower transcript/reference overlap rather than confusion with another episode |
| Episodes unresolved | Episode ID | `item.needs_review=true`, episodes with `episode=0` | Placeholder names in review_dir |
| Episode sequence gaps | Episode ID | Non-sequential episode numbers in `envelope.episodes[]` | Missing episodes or matching error |
| Per-episode rip failure | Ripping | `envelope.assets.ripped[]` with `status: "failed"` | Episode missing from pipeline |
| Per-episode encode failure | Encoding | `envelope.assets.encoded[]` with `status: "failed"` | Episode will not appear in Loom |
| Per-episode subtitle failure | Subtitles | `envelope.assets.subtitled[]` with `status: "failed"` | Episode missing subtitles |
| Cross-episode resolution mismatch | Encoding | Different resolutions across `media[]` entries | Inconsistent quality |
| Cross-episode audio mismatch | Encoding | Different audio stream counts across `media[]` entries | Inconsistent audio tracks |
| Fingerprint fallback used | Identification | `decision_type=fingerprint_strategy` with `decision_result=fallback` | Disc type detection degraded |
| TMDB match rejected | Identification | `decision_type=tmdb_match` with `decision_result=rejected` | No content match found; item fails before ripping |
| Polluted TMDB query | Identification | `event_type=tmdb_no_match` with `error_hint="TMDB returned no results for the query"`; `query_title` still carries disc-label cruft after the `retry_narrowed` attempt | Item fails at identification; the cleanup patterns in `CleanQueryTitle`/`NarrowQueryTitle` need the missing form |
| Unknown media type reached ripping | Ripping | `selectRipTargets` error `cannot select rip targets for media type` | Identification gate was bypassed; investigate how the envelope was built |
| Validation failed but continued | Encoding | `decision_type=validation_failure_route` with `decision_result=flagged_for_review` | Item routed to review |
| Commentary tracks lost in refinement | Audio Analysis | `decision_type=commentary_remapping` with remapped count 0 | Commentary detection effort wasted |
| Source stage fallback to encoded | Organization | `decision_type=source_stage_selection` with `decision_result=encoded` when subtitles enabled | Subtitles may be missing from output |
| Audio selection non-english fallback | Audio Analysis | `decision_type=audio_selection` with `decision_result=fallback_non_english`; a `final_validation` failed check when an English track survived but is not the default | Primary audio track is not English |
| Commentary disposition applied | Audio Analysis | `decision_type=commentary_disposition` with `decision_result=applied` | Commentary tracks marked in output |
| KeyDB stale fallback | Identification | WARN `event_type=keydb_download_error` with message `KeyDB download failed, using stale catalog` | Report as WARNING; identification may miss a newly added or corrected disc title |
| KeyDB lookup miss | Identification | `decision_type=keydb_lookup` with `decision_result=miss` | Disc ID not in KeyDB, fallback to title parsing |

### DEBUG/Raw Log Context

These details are optional context. Some are parsed into decision groups when debug logs are available; others require opening the raw files in `logs.paths`.

| Pattern | Stage | Evidence |
|---------|-------|----------|
| TMDB candidate scoring | Identification | DEBUG `decision_type=tmdb_search`; final selection is visible at INFO as `tmdb_match` |
| TV `below_min_title_length` exclusions | Identification | DEBUG `tv title excluded` (other exclusion reasons are INFO with evidence attrs) |
| Audio candidate scoring | Audio Analysis | Raw DEBUG `audio candidate scored`; final selection is visible at INFO as `audio_selection` |
| Content-ID pending claims | Episode ID | DEBUG `content ID pending claim` — full per-rip candidate scores behind unresolved/contested outcomes |
| Reel internals (chunk plan, CVVDP target-quality config, CRF search, timings) | Encoding | DEBUG `reel verbose` |
| SRT validation observations | Subtitles | DEBUG `SRT validation observation` (per-check detail); the INFO `SRT validation QC summary` and `SRT validation issue` lines carry the verdicts |
| Handler stage start/complete, item stage derived | All | DEBUG; the INFO narrative is the workflow stage pair + `item_complete` |
| LLM retry details | Various | Raw logs around the failed/slow decision |

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
- `analysis.decision_groups` already provides the deduplication -- show identical repeats as `"type x{count}: result (reason)"`; expand a group's `entries` only for decisions with different outcomes, notable parameter variations, or anomalous confidence/scores
- For episode matches below 0.90, use `confidence_quality` and margins from `episode_match` extras to distinguish true ambiguity from `decisive_low_similarity` (strong margins, weaker transcript overlap). Do not file a finding for `decisive_low_similarity` when margins are strong and no review routing occurred.

**Episode manifest:**
- Always show the full per-episode table with confidence scores, matched episode numbers, and titles. Episode identification is a core pipeline feature and the manifest is the primary evidence of correctness. This table is never compressed.

**External validation:**
- When all checks confirm, use a compact paragraph rather than multi-level section/subsection structure
- Only expand into detailed comparison when a mismatch is found

**Do not report as findings (these are normal):**
- Individual subtitle wording or transcription accuracy — subtitle content is outside this skill's scope
- A subtitle skip as CRITICAL — it is a designed no-verified-candidate outcome, already pre-flagged as a warning anomaly; report it once as a WARNING with its recovery path
- Non-sequential disc title ordering — disc layout varies by manufacturer and is irrelevant once content ID resolves episodes
- Inconsistent source audio track counts across titles on the same disc — different playlists routinely carry different language sets
- Audio refinement stripping non-English tracks — that's its job
- Subtitle `qc_observations` that are below review thresholds and have `validation_result=passed`
- An adopted subtitle ending before long credits, or a `reference_tail_gap_s` at or below 600 seconds — Matroska duration is a cue span and sparse WhisperX end-credit hallucinations can make the raw reference appear longer
- Missing HDR10+ dynamic metadata in encoded output — Reel intentionally emits static HDR because the target playback environment does not consume it
- A movie's encoding task holding the `encode` claim with no encoded output while its rip runs — the deferred plan is expected; see Stage Gating above
- An identification-failed item having no rip, encode, or staging artifacts — that is the fatal no-TMDB-match rule working, not missing work

**Stage timing:**
- Always show the timing table — it's compact and useful for spotting anomalies

### Report Template

```
## Audit Report for Item #<id>

**Title:** <item.disc_title>
**Stage:** <item.stage> | **Running Tasks:** <comma-separated `type:progress_percent%` for each item.tasks[] with state=running> | **NeedsReview:** <item.needs_review> | **ReviewReasons:** <item.review_reasons joined with "; ">
**Media Type:** <stage_gate.media_type> | **Source:** <stage_gate.disc_source>
**Debug Logs:** <logs.is_debug>

### Executive Summary
<1-2 sentence overview of findings>

### Issues Found

**[CRITICAL] <Issue Name>**
- Evidence: <specific data from the audit output>
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
- Log files: <logs.paths>
- Lines scanned: <logs.lines_scanned>
- INFO events/progress: <summarize notable logs.events; note logs.events_omitted if progress ticks were downsampled; expand long-running progress/timing anomalies only>
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

- Final output validation: <from analysis.final_validation: per-output pass/fail, any failed_checks, and the av_sync source offset -> output offset with signed drift; explain unavailable entries and a missing verdict>

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

#### Subtitle Pipeline (if phase_subtitles)
- Source: <per-title adoption outcome from subtitle_generation_results[].source — opensubtitles/none; for skips, the subtitle_source rejection reasons>
- Tracks: <count and config from media probes>
- Stream layout and labels: <from analysis.final_validation: passed, or the failed_checks naming the stream count, codec, language tag, label, or forced/default flag>
- Validation result: <aggregate subtitle_generation_results.validation_result; list structured review_issues only when they affected routing, without inspecting cue text>
- Subtitle mux/output: <mux status and the apply stage's subtitle layout verdict; skipped titles legitimately have no subtitle stream>
- Content review: <not performed; subtitle text is out of scope. For skipped titles recommend the whisperx-subtitles skill or a later `spindle subtitle` retry>

#### Commentary (if phase_commentary)
- Decisions: <from analysis.decision_groups>
- Tracks in output: <count from media probes>

### External Validation (if phase_external_validation)
<Compact paragraph when all checks pass. Expand into detailed comparison only when mismatches found.>

### Decision Trace
<From analysis.decision_groups — type x{count}: result (reason) for identical groups. Expand entries only for groups with varying messages or anomalous results.>
```

## Execution Checklist

After running `spindle queue audit`, check only the phases flagged as `true` in `stage_gate`. **Do not check phases beyond the reached stage.**

### Always
- [ ] Ran `spindle queue audit <id>`, read the full digest, noted the JSON path
- [ ] Checked gathering errors (digest header) for incomplete data
- [ ] Reviewed `stage_gate` to determine applicable phases
- [ ] Reviewed pre-flagged anomalies
- [ ] Reported any `keydb_download_error` stale-catalog fallback as a WARNING
- [ ] Analyzed logs/decisions for anomalies beyond simple error counts, drilling into the full JSON wherever the digest flagged an omission or something looked off
- [ ] If TV: reconciled scanned, selected, placeholder, manifest, ripped, and final episode counts; investigated every reduction
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
- [ ] Read `analysis.final_validation`: confirmed a verdict exists for every output, investigated failed checks and unavailable entries, and did not rely solely on Reel's persisted validation verdict
- [ ] Analyzed streams from `media[]` entries (video, audio, subtitle)
- [ ] Validated crop detection from `encoding.snapshot.crop_filter`
- [ ] Reviewed the apply stage's commentary, audio layout, and subtitle layout verdicts
- [ ] Correlated any `source_timeline_normalization` decision with Reel's normalization step and the final audio endpoint verdict
- [ ] If TV: checked cross-episode consistency

### Post-Audio-Analysis (phase_commentary)
- [ ] Reviewed commentary decisions from `analysis.decision_groups`
- [ ] If TV: verified cross-episode audio stream count consistency

### Post-Subtitling (phase_subtitles)
- [ ] Read the apply stage's subtitle layout verdict (adopted titles only; `source=none` skips legitimately have no stream)
- [ ] Checked only aggregate adoption/validation outcomes and routing
- [ ] Did not open, extract, sample, quote, compare, or judge subtitle/transcript content
- [ ] If TV: checked per-episode subtitle asset status

### External Validation (phase_external_validation)
- [ ] Looked up blu-ray.com review
- [ ] Validated crop and commentary count against review

### Report
- [ ] Generated report with only applicable sections
- [ ] Applied presentation density guidelines (compact for clean data, expanded for anomalies)
- [ ] Used `analysis.decision_groups` for decision trace
