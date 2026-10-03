### Phase 4: Encoded File Analysis (when `phase_encoded` is true)

Analyze the `media` array from the audit output. Each entry contains full
ffprobe results for the most complete completed asset (`role` final, else
subtitled, else encoded). A probe that fails is kept with `error` and never
replaced by an earlier-stage file: an `error` entry with `role=final` means the
delivered file itself is missing or unreadable, and it is pre-flagged as a
`media` anomaly.

**TV note:** The encoding snapshot only contains data for the last episode
encoded (the snapshot is overwritten per-episode during encoding). The `media[]`
array is compressed for TV: only the representative probe (matching the majority
profile, marked `representative: true`), deviation probes, and error probes are
included. Use `media_omitted` to see how many clean probes were dropped. The
representative probe is sufficient for stream-level checks (items 2-6 below);
`analysis.final_validation` carries a per-output verdict for every episode
regardless of probe compression, and `analysis.episode_consistency` confirms all
omitted episodes match the same profile. The snapshot is still useful for crop
detection, encoding config, and validation results (which are consistent across
episodes from the same disc).

**For movies** (single entry) or **the representative probe for TV**:

1. **Check the pipeline's final-output verdict** in `analysis.final_validation`:
   - The apply stage probes each delivered file after every rewrite and compares
     the primary audio's offset relative to video against the ripped source;
     absolute drift over 100 ms fails the output and routes it to review. This
     is measured independently of Reel's persisted
     `encoding.snapshot.validation` result — never accept the Reel validation
     line as proof of sync by itself, but do not recompute the comparison here
     either: the ripped source is deleted with staging when the item completes.
   - Do not re-derive the verdict. Verify it exists, then investigate every
     entry with `failed_checks` (each names the invariant that broke) and
     correlate it with the episode's review reasons and the `final_validation`
     decision logs.
   - A negative `av_sync.drift_milliseconds` means output audio moved earlier;
     positive means it moved later.
   - An entry with `error` (or `av_sync.error`) is UNAVAILABLE, not passing: the
     file or its source could not be probed. Investigate rather than claiming
     the output is verified.
   - A missing `analysis.final_validation` on an item whose outputs were
     organized is itself a finding — the apply stage always persists a verdict.

2. **Verify video stream** (from `media[].probe.streams` where
   `codec_type=video`):
   - Resolution matches expected (SD/HD/4K)
   - Codec is AV1 (`av1`) from Reel's SVT-AV1 (libsvtav1) encoder; libaom-av1
     cannot occur
   - Duration matches source within tolerance (~1-2 seconds)
   - Static HDR signaling present if expected (`color_primaries`, transfer
     characteristics, and mastering metadata)
   - Reel intentionally does not preserve HDR10+ dynamic metadata (SMPTE ST
     2094-40) because the target playback environment does not consume it. An
     HDR10+ source producing a static-HDR AV1 output is expected; do not flag
     missing HDR10+ side data or an external HDR10+ vs output static-HDR
     difference.

3. **Verify audio streams** (verdict first, then `media[].probe.streams` where
   `codec_type=audio`):
   - The apply stage already enforces this layout on the delivered file: exactly
     the refinement plan's track count, stream 0 default and no other, an
     English default whenever an English track exists, and commentary flags on
     exactly the tracks it labeled. A violation appears as a `final_validation`
     failed check, not as something to rediscover here.
   - Use the probe to add context the verdict cannot: unexpected stereo downmix
     tracks, a track count that disagrees with the disc's known audio layout,
     odd channel layouts.

4. **Check commentary labeling**:
   - The apply stage verifies every track it marked carries
     `disposition.comment=1` and a title containing "Commentary", and that no
     other track carries the comment flag. Read `analysis.final_validation` for
     the verdict.
   - The remaining judgment call is whether the right tracks were classified as
     commentary: cross-reference commentary decisions in
     `analysis.decision_groups` and, when `phase_external_validation` is true,
     the disc review's commentary count.

5. **Check subtitle streams** (verdict first, then `media[].probe.streams` where
   `codec_type=subtitle`):
   - The apply stage enforces the layout for the delivered file: an adopted
     title (`source=opensubtitles`) that was muxed must carry exactly one
     `subrip` stream with a language tag, a label naming that language, and
     neither the forced nor the default flag; a skipped title (`source=none`)
     must carry no subtitle stream. Failures appear in
     `analysis.final_validation`.
   - Use the probe only to explain a failed check or to look at an output the
     verdict marked unavailable.

6. **Parse encoding details** from `encoding.snapshot`:
   - Check `validation.passed` and individual step results
   - Review crop detection from `crop` fields
   - Check for `warning` or `error` in snapshot
   - Check encoding config: `encoder`, `quality` (Reel target-quality summary),
     `preset`, `tune`, `audio_codec`. These come from Reel's target-quality
     mode, not Spindle config (there is no `[encoding]` config block);
     `preset`/`quality` reflect what Reel chose internally, so there is no
     per-resolution CRF to verify
   - A passed validation step named `Source timeline normalization` records the
     source track count and maximum post-video overrun that Reel corrected.
     Correlate it with the structured `source_timeline_normalization` decision
     and the apply final verdict; do not call the delivered file defective when
     its endpoint checks pass.
   - Reel logs structured records that the encode worker replays through the
     stage logger, so every Reel line carries `item_id`, `stage=encoding`, and
     `episode_key`. Reel warnings keep their own `event_type` in
     `logs.warnings`: `encode_workers_reduced` (memory pressure lowered
     concurrency: slower, same quality), `memory_pressure_cancel` (encode
     canceled before swap exhaustion; the encode fails with the memory-pressure
     error), `resume_state_discarded`, `refcache_fallback` (filtered reference
     cache unusable: slower, same quality), `grain_stage2_unavailable` /
     `grain_stage2_failed` (the fixed-CRF grain verdict stood for an ambiguous
     title), and `encode_output_exists`. `event_type=reel_error` lands in
     `logs.errors` with `context` and an `error_hint` suggestion. The latest
     warning and the error also persist in `encoding.snapshot.warning`/`error`.
     `event_type=encode_worker_stderr` means the worker wrote unexpected native
     or runtime output on a successful encode; quote its `error_hint`.
   - Reel INFO decisions in `analysis.decision_groups`: `chunk_plan` (chunk
     count, natural cuts, merges, splits, and `distribution`), `grain_treatment`
     (`treated`/`untreated`, with `median_bpp` against `treat_above_bpp`,
     `gate_stage`, `stage2_*`, `denoise`, and `reused`), `target_quality_metric`
     (CVVDP or SSIMULACRA2 and why, with target, tolerance, CRF range, and
     display model), `ssimu2_calibration` (the locked offset and warmup probe
     count), and `encode_workers` (`raised` when slots stayed saturated with
     memory stable). `logs.events` carries one
     `event_type=target_quality_summary` per metric with score min/mean/ max,
     `stops`, `probe_counts`, `initial_sources`, and the `multi_probe_chunks` /
     `max_probe_chunks` / `rate_capped_chunks` lists.
   - Target-quality search outcome:
     `envelope.attributes.encode_stats[].target_quality.metrics[]` carries
     per-encode `score_min/mean/max`, `probes_per_chunk`, and `stop_reasons`.
     Per-chunk detail is DEBUG in the daemon log files (`logs.paths`): one
     `msg="TQ probe"` record per probe (`chunk`, `round`, `crf`, `score`,
     `delta`, `peak_mbps`, `over_rate`, `initial_source`) and one
     `msg="TQ final"` per chunk (`crf`, `score`, `probes`, `stop`); filter with
     `jq 'select(.item_id==N and .msg=="TQ final")'`. Read `stop_reasons` by
     name:
     - `converged`: final score in band.
     - `rate_capped`: the AV1 level 5.1 bitstream cap (40 Mbps, 1-second peak
       gate) bounded the search from below on that chunk. Reel rejects probes
       whose worst second exceeds the cap (`over_rate=true` with `peak_mbps` on
       the `TQ probe` record), and on heavy-grain 4K chunks lowering CRF cannot
       raise the score because SVT's regulator holds the rate. The chunk ships
       at its best rate-legal CRF, below the band. This is the intended
       playback-compatibility trade-off, NOT a search defect: report the count
       and worst score as informational context, never as a retest/re-encode
       recommendation. A re-encode on the same source reproduces it exactly.
       Grainy UHD titles routinely show a few percent of chunks here.
     - `max_probes`, `bounds_crossed`, `monotonicity_guard`, `no_candidates`:
       the search stopped without the cap involved. A handful per title is
       normal (probe noise is about 0.075 JOD). A material tail (several percent
       of chunks, or misses well below the band with no cap involvement) is the
       retest condition in Reel's `docs/PERFORMANCE_TESTING.md`; report it as a
       WARNING with the chunk list from `max_probe_chunks` on the
       `target_quality_summary` event.
   - Grain treatment: `analysis.grain_treatments[]` carries Reel's automatic
     grain-gate verdict per encode. The gate samples long chunks in the middle
     60% at fixed CRF 22 and compares their median bpp against
     `treatment_bpp_cutoff`; ambiguous results are re-measured at the quality
     target (`stage2_median_bpp`). A treated title is encoded from an
     `fftdnoiz`-denoised source with a source-matched AV1 grain table, which is
     intended on grainy titles, NOT a defect. A whole-file average bitrate
     (including audio and the excluded title edges) is not comparable to the
     gate's sample median: never label an untreated title a missed treatment
     based on its file size alone. Read it as:
     - Target-quality scores for a treated title are measured against the
       denoised reference, so
       `denoise_ceiling_jod_mean`/`denoise_ceiling_jod_min` (CVVDP of the
       denoised source against the real source on the same chunks) is the honest
       cap on what those scores can mean. A treated title with
       `denoise_ceiling_jod_min` below `band_top_jod` (default 9.75) is
       pre-flagged as a warning anomaly: the denoiser removed quality the CRF
       search can never see as a missed band. Report it with the applicable
       sample median.
     - Automatic treatment fails the encode when the paired-frame ceiling pass
       cannot produce a grain model, so an auto-treated title with no ceiling
       fields violates that invariant and is pre-flagged as a warning anomaly
       ("no measured denoise ceiling"). An explicit `mode=override` may skip
       the pass; say so. Never read a missing ceiling as a clean one.
     - An untreated title with a `reason` (`SD sources are never treated`,
       `no chunk long enough to measure`, `grain treatment disabled`, explicit
       override) explains itself. An untreated title with no reason was measured
       below the cutoff — use `stage2_median_bpp` when `gate_stage=tq_probe`,
       otherwise `median_bpp`.
   - Check `decision_type=file_probe` for pre-encoding resolution and codec
     detection
   - Check `decision_type=crop_detection` for crop decision visibility
   - Check `decision_type=encoding_validation` for per-episode validation
     results
   - `decision_type=validation_failure_route` with
     `decision_result=flagged_for_review` indicates validation-failed items
     routed to review

7. **Per-episode asset status** (TV only, from `envelope.assets.encoded`):
   - Check for `status: "failed"` entries with `error_msg`
   - Encoding allows partial success
   - Verify encoded asset count matches episode count

8. **Cross-episode consistency** (TV only):
   - Use `analysis.episode_consistency` for the overview: `majority_profile`
     gives the common (video_codec, width, height, audio_streams,
     subtitle_streams), `majority_count`/`total_episodes` show how many match,
     and `deviations[]` lists episodes with human-readable differences. A
     verified commentary track on only some episodes is context, not a warning;
     check whether the program streams differ after excluding it.
   - Use `analysis.media_stats` for duration range (`duration_min_sec/max_sec`)
     and size range (`size_min_bytes/max_bytes`)
   - Inspect the representative probe for stream-level checks (items 2-6);
     omitted probes are confirmed equivalent by the consistency analysis

### Phase 5: Crop Detection Validation (when `phase_crop` is true)

Analyze crop data from the audit output:

1. **Read pre-computed crop data**: `analysis.crop_analysis` provides
   `output_width`, `output_height`, `aspect_ratio`, `standard_ratio`, and
   `required`. Also read `encoding.snapshot.crop_message` for the detection
   summary.

2. **Verify aspect ratio**: Common ratios: 2.39:1/2.40:1 (scope), 1.85:1, 1.78:1
   (16:9), 2.00:1 (IMAX). Compare `analysis.crop_analysis.standard_ratio`
   against expected for the content.

3. **External cross-reference** (only when `phase_external_validation` is true):
   - Search: `site:blu-ray.com "<title>" review`
   - Flag if our crop differs significantly from the review's stated ratio

4. **IMAX/variable aspect ratio issues**:
   - If crop detection shows "multiple ratios" or low top-candidate percentage

5. **TV episode crop consistency** (TV only):
   - All episodes from the same disc should have identical or very similar crop
   - Spot-check one or two episodes rather than performing full validation on
     every episode

**Pipeline structure note:** the item template is a DAG. After
`episode_identification`, the ANALYSIS branch (`analysis` stage: per-episode
commentary detection from RIPPED sources; then `subtitling`: SRT ADOPTION ONLY —
download/clean/retime/verify into staging, never writing encoded files) runs
CONCURRENTLY with `encoding`. The `apply` stage joins both branches and performs
every write to the encoded files: audio refinement, commentary disposition,
duration validation, sidecar placement, and subtitle muxing. Consequences for
audits:

- Log timelines legitimately interleave encoding events with analysis and
  subtitling events for the SAME item — not disorder.
- Progress is per task: each running stage writes its own `tasks[]` row, so
  `item.tasks[]` shows independent live progress for both branches during
  overlap (for example a `ripping` task and an `encoding` task both
  `state=running` with their own `progress_percent`/`progress_message`) instead
  of one arbitrated item-level progress field.
- `audio_analysis` does not exist as a stage name; commentary detection
  decisions appear under stage `analysis`, remuxes/muxing under `apply`.
- Commentary detection is PER-EPISODE
  (`envelope.attributes.audio_analysis .per_episode`), measured on ripped files;
  indices are remapped in apply.
- `mux_start/_complete` and subtitled assets are produced by `apply`, not
  `subtitling`.

### Phase 6: Subtitle Pipeline Integrity (when `phase_subtitles` is true)

Start from structural subtitle evidence in `media[].probe.streams`
(codec_type=subtitle), `analysis.subtitle_summary`, and
`envelope.assets.subtitled`. Subtitle quality review (wording, line breaks,
reading speed) is an operator action outside this audit. Read candidate,
reference, or transcript text only to diagnose an adoption decision - for
example, whether a rejected candidate belongs to another episode or a cut with
different timing - and keep findings about that decision. Keep this phase
compact.

**For movies** or **per-episode for TV**:

1. **Verify embedded subtitles**, primarily from `analysis.final_validation`:
   - The apply stage already reconciles each output's subtitle streams against
     that title's adoption record: one `subrip` stream, language tag present,
     label naming the language, no forced flag, no default flag for an
     adopted-and-muxed title; no subtitle stream at all for `source=none`. Read
     the verdict rather than re-deriving it from `media[]`.
   - A mux failure falls back to a sidecar SRT that Loom ignores, so it flags
     the episode for review with a `final_validation`/`subtitle_mux` review
     reason. Report it as a real defect, not a cosmetic one.
   - Never treat Matroska's subtitle `tags.DURATION` as the subtitle's absolute
     end timestamp. It is the cue span (`last cue end - first cue start`). For a
     suspected tail gap, use ffprobe packet metadata for the subtitle stream and
     calculate `max(pts_time + duration_time)`; packet timing, not cue text,
     answers this. A gap from that timestamp to video duration is not itself a
     finding: valid display subtitles stop before long credits, and sparse
     WhisperX end-credit hallucinations can extend the raw reference. For an
     adopted track, trust a `reference_tail_gap_s` at or below the 600-second
     gate unless other structural validation failed.

2. **Subtitle adoption outcome** (from `analysis.subtitle_summary`,
   `envelope.attributes.subtitle_generation_results`, and
   `analysis.decision_groups`):
   - The pipeline downloads the identified title's OpenSubtitles candidates,
     cleans them, retimes against the rip's WhisperX transcript with ffsubsync,
     and adopts the first candidate that passes the verification gate. The
     daemon and `spindle subtitle` build the same attempt set (search, rank by
     source affinity against the actual video, cap at 3, one
     `subtitle_candidate_ranking` decision listing `candidate_file_ids`).
     Episode identification's reference is not promoted: it is tried only if
     the ranking selects it (then served from the shared download cache), so a
     DVD-release reference never outranks a Blu-ray-matched upload. A
     `contentid_reference` candidate label is from an older binary. The short
     Jev excerpt is never the display verification transcript. When no candidate
     verifies (or none exists, or the title is multi-episode), it records
     `source=none` and the title completes WITHOUT subtitles. Spindle never
     generates subtitles itself.
   - `decision_type=subtitle_source` is the core trace:
     `decision_result=adopted` (reason carries the candidate and gate metrics),
     `candidate_rejected` per rejected candidate (reason explains which gate
     failed), and `skipped` (reason explains why nothing was adopted). A skip
     also emits WARN `event_type=subtitle_skipped` and a pre-flagged warning
     anomaly ("N title(s) completed without subtitles"). Report a skip as a
     WARNING with the reason as evidence, then trace it upstream. If bad title
     selection or episode identity caused the skip, fix that bug and reprocess
     before considering subtitle-specific recovery. Only a genuine
     no-verified-candidate outcome calls for the whisperx-subtitles skill (or
     `spindle subtitle` after better uploads appear), not a blind pipeline
     retry.
   - `decision_type=subtitle_duration_source` shows whether the verification
     gate measured video duration from ffprobe or fell back to the transcript
     span.
   - `decision_type=subtitle_mux` with `decision_result=skipped` indicates
     muxing was disabled in config.
   - `decision_type=transcription_asset` and
     `decision_type=transcription_profile` show which asset/profile WhisperX
     processed for the sync reference. Use `logs.events` entries
     (`transcription_extract`, `transcription_whisperx_complete`) for
     transcription timing before falling back to
     the raw files in `logs.paths`.
   - `decision_type=subtitle_transcript_source` with
     `decision_result=artifact_reused` means the stage reused the shared
     per-episode transcript artifact (`envelope.assets.transcript`) and ran no
     WhisperX of its own — absent transcription events in the subtitling stage
     are then expected, not a defect. For TV, verify transcript asset count
     matches episode count in `analysis.asset_health`.
   - Additional subtitle tracks, forced dispositions, and "Forced" subtitle
     labels fail the apply stage's layout check and route the output to review;
     if you see them without a matching `final_validation` failure, the output
     was not produced by the current pipeline (a stale file) — say so.

3. **Per-episode subtitle asset status** (TV only, from
   `envelope.assets.subtitled`):
   - Check for `status: "failed"` entries with `error_msg`
   - Verify `subtitles_muxed` flag per episode
   - Check `envelope.attributes["subtitle_generation_results"]` for per-episode
     details
   - Treat `validation_result` as the actionable summary: `passed` is clean,
     `needs_review` is actionable, and `skipped` accompanies `source=none` and
     is the no-subtitle outcome, not a failure. A severe issue rejects the
     candidate before any record is written, so an adopted record never reads
     `failed`
   - Treat `qc_observations` as telemetry only. Do not list below-threshold
     observations (for example `high_reading_speed`, `short_cue_duration`,
     `long_cue_duration`) as Issues Found unless they also appear in
     `review_issues`/`severe_issues` or caused review routing.
   - Exception: an extreme `srt_validation` `max_cps` (hundreds or more) means
     a near-zero-duration cue. The word-snap pass (`snapped_cues` in the
     `subtitle_source` reason) skips any snap that would reorder cues or trim
     a neighbor below the 5/6 s minimum, so on a current binary such a cue
     must come from the source file itself; on an older binary it is the snap
     reorder bug. Confirm with ffprobe subtitle packet `pts_time`/
     `duration_time` (no payloads): a ~1 ms cue followed by one starting 1 ms
     later is the reorder signature, and a muxed packet count below
     `segments` means a zero-length cue was dropped. Report either as a
     WARNING, since the affected lines never display.

4. **Cross-episode subtitle consistency** (TV only):
   - Adopted episodes should share the same subtitle language and
     single-display-subtitle layout; a mix of adopted and skipped episodes on
     one disc is possible and each skip should have its own `subtitle_source`
     trace
