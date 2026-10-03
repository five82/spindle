### Full JSON Schema

The JSON report schema may evolve with this skill; treat it as diagnostic input
rather than a stable public API. It contains:

- **`item`**: Queue item summary (`stage`, `tasks[]` per-task state/progress,
  review flags, paths, timestamps). `item.stage` is the scheduler's coarse
  position and lags running tasks during rip/encode overlap -- read
  `item.tasks[]` for what is actually happening (`type`, `state`, `attempts`,
  `error`, `progress_percent`, `progress_message`, `active_asset_key`)
- **`stage_gate`**: Pre-computed phase applicability (which analyses apply,
  resolved media type, media hint, disc source)
- **`logs`**: Parsed diagnostic log entries — warnings and errors (with `extras`
  maps of non-standard log fields), and every other item-specific INFO line
  (`logs.events`, usually with an `event_type`; untyped lines such as
  `Phase N/M` progress are kept with an empty `event_type`), but NOT native
  stage/substage transitions. Gathered from every daemon log file overlapping the item's
  lifetime (`logs.paths` — daemon restarts mid-item span multiple files),
  clamped to the item's creation time so reused item IDs / re-ripped discs don't
  leak earlier runs' lines. Flooding `*_progress` event types are downsampled
  (first/last/evenly-strided, ~20 per type); `logs.events_omitted` counts
  dropped ticks — it is normal on long encodes, not data loss. Decisions are NOT
  in `logs` — they live in `analysis.decision_groups`. Every entry and decision
  logged inside a stage run carries `stage` (and `attempt` in extras); this
  includes shared-client lines (`llm_request_*`, `llm_retry`, `opensubtitles_*`,
  `tmdb_retry`, `loom_scan*`, transcription), so an LLM or OpenSubtitles retry
  is attributable to the stage that issued it. DEBUG lines are never parsed;
  `logs.is_debug` only says the files contain them. Lines are read whole with no
  length cap. If every log file active at item creation has expired, `errors`
  says so: the earliest log lines are missing, not clean.
- **`transitions`**: Native, queue-backed per-item event history, ordered by
  increasing `id` across daemon restarts. Each entry has `time`, `type`,
  `stage`, `itemId`, `taskId`, and `attempt`; a run is one (`stage`, `taskId`,
  `attempt`). Each run emits `stage_start` and one terminal event:
  `stage_complete`, `stage_failed`, `stage_canceled`, `stage_stopped`, or
  `stage_degraded`, with numeric `durationSeconds`. Startup recovery records
  `stage_interrupted` (with the task's last `episodeKey`, `message`, `percent`,
  and the run's `durationSeconds`) for a run the daemon died under.
  Task activities, including Reel's encoding phases, are journaled as
  `activity_running`/`activity_waiting` when they start and `activity_done`/`activity_ended` (with `durationSeconds`) when they
  finish or are superseded; `substage` holds the activity's operation. The
  executor ends every activity still open when a run reaches its terminal
  outcome, so a live activity on a finished run is a defect. The digest's
  "Stage runs" section renders all of this per run; use the JSON for exact
  sequences. The queue DB is transient: removed/cleared items lose their
  transitions.
- **`rip_cache`**: Cache metadata (disc title, cached_at, title_count,
  total_bytes). Serialized `rip_spec_data` and `metadata_json` blobs are omitted
  (already in parsed `envelope`). `disabled: true` means the cache is turned off
  in config — do not report that as a pruned entry.
- **`envelope`**: Parsed ripspec Envelope (titles, episodes, assets at each
  stage, attributes). `attributes.content_id` records the dialogue-reference
  method, `reference_source=opensubtitles`, usable `reference_episodes`,
  probability threshold, outcome counts, and completion. Reference file IDs,
  release/file names, and selection/omission reasons live in `reference_search`
  decision extras; they contain provenance, not cue text.
- **`encoding`**: Encoding details snapshot (crop, validation, config, result).
  Spindle always uses Reel target-quality mode, so the snapshot carries the full
  Reel-reported config summary (`encoder`, `quality`, `preset`, `tune`,
  `audio_codec`) plus crop and validation (pass/fail) — nothing is omitted
- **`media`**: ffprobe output for the most complete completed asset per output
  (final, else subtitled, else encoded), with `role` naming which. A failed
  probe is kept with `error` and is never replaced by an earlier-stage file, so
  a missing or corrupt delivered file shows up here. For TV, only the
  representative probe (matching majority profile, marked
  `representative: true`), deviation probes, and error probes are included.
  `media_omitted` indicates how many clean probes were dropped.
- **`errors`**: Any gathering errors (missing or expired logs, read and parse
  failures). Data below them may be incomplete.
- **`analysis`**: Pre-computed summaries — decision groups, episode consistency,
  crop analysis, episode stats, media stats, asset health, the apply stage's
  final-output validation verdict, anomaly flags (see Analysis Reference below)

**The `stage_gate` object tells you exactly which phases to run.** Each
`phase_*` boolean is pre-computed from the item's task states (which lead the
coarse item stage during rip/encode overlap), media type, and disc source. Do
not re-derive these — trust the gate.

### Analysis Reference

The `analysis` object (always present; sub-fields omitted when empty) contains
pre-computed summaries:

| Field                 | Present When                       | Contents                                                                                                                                                                                                                                                                                                                                                                                                                                                                                      |
| --------------------- | ---------------------------------- | --------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------- |
| `decision_groups`     | Decisions exist                    | Groups by (type, result, reason) with count, in log order. `entries` always carries every grouped decision with its timestamp — this is the only record of individual decisions and their spacing.                                                                                                                                                                                                                                                                                            |
| `notable_decisions`   | Notable decisions exist            | Curated subset of decisions most useful for reporting (TMDB/title/crop/validation/source normalization/audio/subtitle/routing/episode match), avoiding noisy full decision scans.                                                                                                                                                                                                                                                                                                             |
| `stage_timings`       | Native stage transitions exist     | One row per stage with start, successful completion, duration, start count, completion count, and `interruptions` (`stage_interrupted` runs). Derived from `transitions`; `stage_failed`/`stage_canceled`/`stage_stopped`/`stage_degraded` are NOT counted as successful completions. The digest's "Stage runs" section shows each run's own outcome.                                                                                                                                           |
| `source_summary`      | Source/output traits known         | Disc source, UHD-likely flag, input resolution, output resolution (delivered probe, else the encode snapshot's cropped output), input codecs, output codec, HDR/dynamic range.                                                                                                                                                                                                                                                                                                                                                                                         |
| `title_selection`     | Movie titles exist                 | Feature-length candidates, selected title, selection decision/reason, and similar-runtime candidate count. Prefer this over hand-parsing `envelope.titles`.                                                                                                                                                                                                                                                                                                                                   |
| `output_media`        | Valid probes exist                 | Compact stream summaries (video/audio/subtitle titles, languages, and dispositions) derived from ffprobe. Prefer this for normal stream checks; use raw `media[]` only for missing details. Label and disposition correctness is judged by `final_validation`, not here.                                                                                                                                                                                                                      |
| `final_validation`    | The apply stage ran                | The pipeline's own verdict on each delivered output, copied from `envelope.attributes.final_validation`. Per-output entries carry `output_path`, `passed`, `failed_checks[]`, an `error` when the file could not be probed, and an `av_sync` block (source/output A/V offsets, signed drift in milliseconds, pass/fail at 100 ms). The apply stage probes the delivered file against the ripped source after every rewrite, so this stays independent of Reel's persisted validation verdict. |
| `audio_summary`       | Audio evidence exists              | Primary track, output/excluded/commentary counts, and commentary decisions. Whether the labels are correct comes from `final_validation`.                                                                                                                                                                                                                                                                                                                                                     |
| `subtitle_summary`    | Subtitle evidence exists           | Subtitle pipeline metadata: per-title source (`opensubtitles`/`none`), validation counts, skipped count, and output subtitle count. Stream layout and label correctness come from `final_validation`. It is not evidence for auditing subtitle text.                                                                                                                                                                                                                                          |
| `routing_summary`     | Final assets exist                 | Classification of each final output's destination and its expected-vs-actual route. The organizer enforces routing itself and fails the stage on a mismatch, so a mismatch here means the organizer's check and this rule disagree; it raises a critical `routing` anomaly.                                                                                                                                                                                                                   |
| `episode_consistency` | 2+ TV probes                       | `majority_profile` (video_codec, width, height, audio_streams, subtitle_streams with codec/language/is_forced), `majority_count`, `total_episodes`, `deviations[]` with human-readable differences. Commentary-only deviations remain visible but do not trigger a consistency warning.                                                                                                                                                                                                       |
| `crop_analysis`       | Crop data exists                   | `filter`, `output_width/height`, `aspect_ratio`, `standard_ratio`, `required`.                                                                                                                                                                                                                                                                                                                                                                                                                |
| `grain_treatments`    | Reel reported a grain-gate verdict | Per-encode `episode_key`, `mode` (auto/off/override), `treated`, `resolution_class`, `denoise`, `estimation` (source-matched grain model), `reason`, `gate_crf`, `sample_chunks`/`sample_bpp`, `median_bpp` against `treatment_bpp_cutoff`, `gate_stage` with the `stage2_*` re-measurement and `ambiguous_bpp_cutoff`, `gate_seconds`/`ceiling_seconds`, `ceiling_measured`, `denoise_ceiling_jod_mean`/`_min` against `band_top_jod`, `reused`, and per-encode sizes. Lifted from `envelope.attributes.encode_stats[].grain_treatment`.  |
| `episode_stats`       | Episodes exist                     | `count`, `matched`, `unresolved`, `placeholder_only`, `probability_min/max/mean`, `below_090` (resolved identities violating their rule: direct <0.90, slot-corroborated <0.50), `slot_corroborated`, `sequence_contiguous`, `episode_range`. These are episode option probabilities, not Jev's separate confidence statistic.                                                                                                                                                                                                                                         |
| `media_stats`         | Valid probes exist                 | `file_count`, `duration_min_sec/max_sec`, `size_min_bytes/max_bytes`.                                                                                                                                                                                                                                                                                                                                                                                                                         |
| `asset_health`        | Assets exist                       | Per-stage (ripped/encoded/subtitled/final/transcript) `total/ok/failed/muxed` counts. `transcript` counts the shared per-episode WhisperX transcript artifacts reused across episode-ID, commentary, and subtitle generation.                                                                                                                                                                                                                                                                 |
| `anomalies`           | Issues/context detected            | Pre-flagged signals with `severity` (critical/warning/info), `category`, `message`.                                                                                                                                                                                                                                                                                                                                                                                                           |

**Use critical/warning `analysis.anomalies` as a starting checklist for Issues
Found.** Info-level anomalies, if present, are context only unless investigation
shows real user impact. Each anomaly is a machine-detected flag -- the LLM's job
is to investigate context, assess impact, reject false positives, and add
judgment-based findings the code cannot detect.

### Stage Gating

The `stage_gate` object in the audit output contains:

| Field                       | Meaning                                                                                                                                                            |
| --------------------------- | ------------------------------------------------------------------------------------------------------------------------------------------------------------------ |
| `furthest_stage`            | Status the item reached (or failed at)                                                                                                                             |
| `media_type`                | Resolved media type: `movie`, `tv`, or `unknown`. `unknown` means identification has not completed or failed outright — it can never belong to an item that ripped |
| `media_hint`                | Hint inferred before/without full identification (for example `tv` on a failed TMDB lookup)                                                                        |
| `disc_source`               | `bluray`, `dvd`, or `unknown`                                                                                                                                      |
| `phase_logs`                | Always true                                                                                                                                                        |
| `phase_rip_cache`           | Post-ripping                                                                                                                                                       |
| `phase_episode_id`          | TV only, post-episode-identification                                                                                                                               |
| `phase_encoded`             | Post-encoding                                                                                                                                                      |
| `phase_crop`                | Post-encoding                                                                                                                                                      |
| `phase_subtitles`           | Post-subtitling                                                                                                                                                    |
| `phase_commentary`          | Post-audio-analysis                                                                                                                                                |
| `phase_external_validation` | Post-encoding AND non-DVD source                                                                                                                                   |

**Key principles:**

- Blu-ray.com crop/commentary validation requires encoded files and a Blu-ray
  source. **Skip Blu-ray.com validation for DVDs.** Separately, for any TV disc
  (including DVD), search for a credible disc-specific episode listing when
  available and compare its count to the selected title/placeholder count. Do
  not infer the per-disc count from the season's total; check edition/reissue
  notes (episodes may have been removed), and if no reliable listing for this
  edition exists, say the count is unverified.
- UHD status is not encoded in `disc_source`. Infer UHD from contextual signals:
  disc title containing "UHD", 2160p resolutions in bdinfo, or similar markers
  in the audit data.
- **For failed items:** Focus the report on diagnosing the failure. Analyze the
  error, the events leading up to it, and any retry patterns. Do not pad the
  report with sections that say "N/A - not reached".
- **No-TMDB-match is fatal at identification for every disc.** Expect these
  items to fail before ripping rather than continue as degraded
  unknown-media-type review items. An item that reached ripping therefore always
  has `media_type=movie` or `tv`; `media_type=unknown` means the item failed at
  (or has not yet finished) identification. A ripped item carrying `unknown` is
  itself a finding — the ripper rejects that media type outright.
- **A failed TMDB search is retried once on a narrowed title.**
  `decision_type=tmdb_search` with `decision_result=retry_narrowed` shows an
  edition or cut suffix being stripped ("Mary Poppins 50th Anniversary Edition"
  -> "Mary Poppins") after the first query found nothing. Its presence is normal
  recovery, not a defect. When an item still fails, read
  `event_type=tmdb_no_match`: `error_hint` distinguishes "TMDB returned no
  results for the query" (query pollution — check `query_title` against the disc
  label) from "no result met confidence threshold" (candidates existed but
  scored too low — check the raw DEBUG `TMDB candidate scored` lines), and
  `result_count` confirms which.
