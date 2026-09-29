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
  maps of non-standard log fields), item-specific INFO events/progress
  (`logs.events` with `event_type` and extras), but NOT native stage/substage
  transitions. Gathered from every daemon log file overlapping the item's
  lifetime (`logs.paths` — daemon restarts mid-item span multiple files),
  clamped to the item's creation time so reused item IDs / re-ripped discs don't
  leak earlier runs' lines. Flooding `*_progress` event types are downsampled
  (first/last/evenly-strided, ~20 per type); `logs.events_omitted` counts
  dropped ticks — it is normal on long encodes, not data loss. Decisions are NOT
  in `logs` — they live in `analysis.decision_groups`.
- **`transitions`**: Native, queue-backed per-item event history, ordered by
  increasing `id` across daemon restarts. Each entry has `time`, `type`,
  `stage`, and `itemId`; terminal events have numeric `durationSeconds`, and
  `encoding_substage` entries may carry `episodeKey`, `substage`, `message`, and
  `percent`. Each run emits `stage_start` and one terminal event:
  `stage_complete`, `stage_failed`, `stage_canceled`, `stage_stopped`, or
  `stage_degraded`. Consult the full JSON for restart/retry sequences, failures,
  and substage progress; these entries are not in `logs.events` or the digest's
  Events section. The queue DB is transient: removed/cleared items lose their
  transitions.
- **`rip_cache`**: Cache metadata (disc title, cached_at, title_count,
  total_bytes). Serialized `rip_spec_data` and `metadata_json` blobs are omitted
  (already in parsed `envelope`). `disabled: true` means the cache is turned off
  in config — do not report that as a pruned entry.
- **`envelope`**: Parsed ripspec Envelope (titles, episodes, assets at each
  stage, attributes)
- **`encoding`**: Encoding details snapshot (crop, validation, config, result).
  Spindle always uses Reel target-quality mode, so the snapshot carries the full
  Reel-reported config summary (`encoder`, `quality`, `preset`, `tune`,
  `audio_codec`) plus crop and validation (pass/fail) — nothing is omitted
- **`media`**: ffprobe output for encoded files. For TV, only the representative
  probe (matching majority profile, marked `representative: true`), deviation
  probes, and error probes are included. `media_omitted` indicates how many
  clean probes were dropped.
- **`errors`**: Any gathering errors (missing logs, parse failures, etc.)
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
| `stage_timings`       | Native stage transitions exist     | One row per stage with start, successful completion, duration, start count, and completion count. Derived from `transitions`; `stage_failed`/`stage_canceled`/`stage_stopped`/`stage_degraded` are NOT counted as successful completions. Read raw transitions for the outcome of each run.                                                                                                                                                                                                   |
| `source_summary`      | Source/output traits known         | Disc source, UHD-likely flag, input/output resolution, input codecs, output codec, HDR/dynamic range.                                                                                                                                                                                                                                                                                                                                                                                         |
| `title_selection`     | Movie titles exist                 | Feature-length candidates, selected title, selection decision/reason, and similar-runtime candidate count. Prefer this over hand-parsing `envelope.titles`.                                                                                                                                                                                                                                                                                                                                   |
| `output_media`        | Valid probes exist                 | Compact stream summaries (video/audio/subtitle titles, languages, and dispositions) derived from ffprobe. Prefer this for normal stream checks; use raw `media[]` only for missing details. Label and disposition correctness is judged by `final_validation`, not here.                                                                                                                                                                                                                      |
| `final_validation`    | The apply stage ran                | The pipeline's own verdict on each delivered output, copied from `envelope.attributes.final_validation`. Per-output entries carry `output_path`, `passed`, `failed_checks[]`, an `error` when the file could not be probed, and an `av_sync` block (source/output A/V offsets, signed drift in milliseconds, pass/fail at 100 ms). The apply stage probes the delivered file against the ripped source after every rewrite, so this stays independent of Reel's persisted validation verdict. |
| `audio_summary`       | Audio evidence exists              | Primary track, output/excluded/commentary counts, and commentary decisions. Whether the labels are correct comes from `final_validation`.                                                                                                                                                                                                                                                                                                                                                     |
| `subtitle_summary`    | Subtitle evidence exists           | Subtitle pipeline metadata: per-title source (`opensubtitles`/`none`), validation counts, skipped count, and output subtitle count. Stream layout and label correctness come from `final_validation`. It is not evidence for auditing subtitle text.                                                                                                                                                                                                                                          |
| `routing_summary`     | Final assets exist                 | Display-only classification of each final output's destination and its expected-vs-actual route. The organizer enforces routing itself and fails the stage on a mismatch, so this table is context, not the check.                                                                                                                                                                                                                                                                            |
| `episode_consistency` | 2+ TV probes                       | `majority_profile` (video_codec, width, height, audio_streams, subtitle_streams with codec/language/is_forced), `majority_count`, `total_episodes`, `deviations[]` with human-readable differences. Commentary-only deviations remain visible but do not trigger a consistency warning.                                                                                                                                                                                                       |
| `crop_analysis`       | Crop data exists                   | `filter`, `output_width/height`, `aspect_ratio`, `standard_ratio`, `required`.                                                                                                                                                                                                                                                                                                                                                                                                                |
| `grain_treatments`    | Reel reported a grain-gate verdict | Per-encode `episode_key`, `mode` (auto/off/override), `treated`, `tier` (light/med), `resolution_class`, `denoise`, `grain_table`, `reason`, `gate_crf`, `sample_chunks`/`sample_bpp`, `median_bpp` against `light_bpp_cutoff`/`med_bpp_cutoff`, `gate_seconds`/`ceiling_seconds`, and `denoise_ceiling_jod_mean`/`denoise_ceiling_jod_min`. Lifted from `envelope.attributes.encode_stats[].grain_treatment`.                                                                                |
| `episode_stats`       | Episodes exist                     | `count`, `matched`, `unresolved`, `placeholder_only`, `probability_min/max/mean` and `below_090` (resolved identities only), `sequence_contiguous`, `episode_range`. These are episode option probabilities, not Jev's separate confidence statistic.                                                                                                                                                                                                                                         |
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
  scored too low — check the `tmdb_search` candidate scores at DEBUG), and
  `result_count` confirms which.
