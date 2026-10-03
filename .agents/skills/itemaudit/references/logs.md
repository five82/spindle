### Phase 2: Log and Lifecycle Analysis (when `phase_logs` is true)

Analyze `analysis.decision_groups`, `logs.events`, `logs.warnings`,
`logs.errors`, `analysis.stage_timings`, and the full JSON's `transitions`. **Go
beyond simple error counts.** Native transitions remain available even if a
daemon log file has rotated away or is missing.

1. **Decision anomalies** (from `analysis.decision_groups`):
   - Scores that contradict the decision's own acceptance rule. Jev commentary
     uses P(commentary) >= 0.65; episode identification requires P(episode) >=
     0.90. Neither uses Jev's separate confidence statistic.
   - Unexpected fallbacks (encoding retries)
   - Decisions that contradict expected behavior for the content type
   - Look up groups by `decision_type` to find specific categories
     (`commentary_classification`, `tmdb_match`, etc.)
   - Infrastructure decisions to check: `decision_type=tmdb_match`
     (acceptance/rejection), `decision_type=title_resolution` (source priority),
     `decision_type=fingerprint_strategy` (disc type detection),
     `decision_type=disc_id_cache` (cache hit/miss),
     `decision_type=duplicate_detection` (duplicate guard),
     `decision_type=episode_id_skip` (episode-ID skips),
     `decision_type=rip_cache` (hit/miss/incomplete — misses log explicitly),
     `decision_type=keydb_refresh` (point-of-use catalog freshness)
   - `decision_type=source_timeline_normalization` with
     `decision_result=bounded_to_video` means Reel detected audio in the MakeMKV
     rip extending materially beyond the video endpoint and bounded it during
     encoding. Treat it as corrected source-artifact context, not a
     delivered-output defect, when Reel validation and apply final validation
     both pass. If either validation fails, report the remaining endpoint
     mismatch.
   - A WARN `event_type=keydb_download_error` means identification continued
     with a stale KeyDB catalog. Always report it as a **WARNING** because a
     newly added or corrected disc title may have been missed. A `keydb_refresh`
     decision with `decision_result=catalog_stale` followed by a successful
     `keydb_download_complete` is normal recovery, not a finding.
   - Movie title selection: `decision_type=title_selection_funnel` records each
     elimination stage (rule, `candidates_before/after`, `eliminated_title_ids`,
     `evidence` with the threshold values); the winner is the
     `decision_type=title_selection` "primary title decision" line. When the
     wrong cut/title was picked, the funnel shows which rule eliminated the
     right one.
   - Scheduler resource waits: `decision_type=stage_execution` with
     `decision_result=blocked` / `unblocked` shows a task waiting on
     GPU/drive/encode claims (`claims` attr) and the `waited` duration on grant.
     "stage started" lines also carry the resolved `claims` (so the GPU-for-TV
     choice is visible per dispatch). The `encode` claim has capacity 1 —
     encodes never run concurrently. A movie's encoding task takes that claim
     when identification completes and then polls without work until its rip
     finishes (`encoding_plan` logs `decision_result=deferred` for this; TV logs
     `streaming`). That idle hold is by design and is NOT a finding: ready tasks
     are ordered by item `created_at`, so the claim always goes to the oldest
     item still needing it, and under sequential ripping that is also the item
     whose rip completes first.
   - Disk admission waits: `decision_type=stage_execution` with
     `decision_result=blocked` / `unblocked` and `disk_space_wait` /
     `disk_space_available` transitions mean the item paused before a write, not
     that ripping or encoding failed. Check the persisted task activity
     (`operation=disk_space`) for required/available GiB, the staging
     filesystem, and whether the wait is still active. A blocked identification
     needs 150 GiB; a fresh rip needs max(150 GiB, 1.1x selected title bytes +
     100 GiB); encoding needs 100 GiB before dispatch and before each asset. A
     cache hit uses its cached size instead of selected title estimates.
     Distinguish an expected low-space wait (operator action) from an actual
     ENOSPC after the check (check concurrent writes and per-encode scratch
     usage). The rip cache cap is retention, not a free-space reservation. One
     ntfy warning is sent on entry into each wait; do not count polling as
     repeated failures.
   - Warnings/errors and decision entries include `extras` maps with
     non-standard log fields for diagnostic context, including the underlying
     `extras.error` when logged. Jev commentary decisions carry
     `extras.commentary_probability` and `extras.track_index`; full log lines
     are also available at the files in `logs.paths`.

2. **Timing/progress anomalies** (from the digest's "Stage runs" section,
   `transitions`, and `logs.events`):
   - Stages taking unusually long or short: each run line carries its terminal
     outcome and duration, and "time by activity" shows where the run's time
     went (sums of journaled activity durations, largest first)
   - Waits: a "waited" line is a journaled `activity_waiting` (for example a
     movie encode holding its slot until the rip completes, or a disk or
     resource wait) with its duration
   - "OPEN at run end" is an activity still running or waiting when a
     non-interrupted run reached its terminal outcome. The executor closes open
     activities at every terminal outcome, so on a current binary this is a
     defect in the executor or activity journaling; runs recorded before that
     fix show it routinely
   - Large gaps between native transitions suggesting hangs; compare with
     `logs.events` progress before concluding a task stalled
   - Repeated starts and terminal outcomes on retries or restarts
   - Use `logs.events` for long-running work visibility: `encoding_progress`,
     `rip_progress`, `copy_progress`, `transcription_extract`,
     `transcription_whisperx_complete`, `commentary_llm_start/_complete`,
     `mux_start/_complete`, `loom_scan_start`, plan events such as `*_plan`,
     and untyped INFO progress lines (`Phase N/M - ...`, empty `event_type`)
   - Encode lifecycle evidence: `logs.events` carries `encode_init` (input
     resolution/dynamic range), `encoder_config` (preset/quality and full
     `svtav1_params` — check level/mbr cap here for playback-compat questions),
     and `encode_result` (sizes, wall time, speed). Reel phase changes
     (chunking/grain gate/encoding/merging/muxing) are native `transitions`
     activities (`activity_running`/`activity_ended`) with episode key,
     message, and duration.
     `logs.events` entry `encoding_progress` carries `bitrate` and
     `chunks_complete/chunks_total`.
   - Item lifecycle: `event_type=item_complete` is the one-line completion
     summary (per-stage `<stage>_duration` attrs plus `total_wall_time`);
     `event_type=operator_action` records user-initiated
     retry/stop/remove/clear/disc-pause/daemon-stop;
     `event_type=startup_queue_state` shows what a daemon restart resumed.
   - **Drain restarts are NORMAL, not anomalies.** `spindle stop` (and therefore
     every deploy) drains by default: dispatch pauses, non-drive workers
     (encoding, GPU stages) are cancelled and revert to pending, in-flight drive
     work (identification, ripping) finishes, then the daemon exits and the next
     start resumes the queue. Expect this mid-item signature and do not flag it:
     `operator_action` with `action=daemon_stop` (`mode=drain` or `mode=force`),
     `decision_type=daemon_drain` (`draining` -> per-worker `cancelled` ->
     `drained`), a native `stage_canceled` transition followed by another
     `stage_start` on resume, `startup_queue_state` on the next start, and logs
     spanning multiple daemon log files.
   - Resume evidence after a drain/restart is also normal: a repeated
     `encode_start` for the same episode with reel resuming from completed
     chunks (`encoding_progress` starting at nonzero `chunks_complete`);
     `decision_type=staging_cleanup` reporting `kept_ripped_titles` and
     `kept_encoded_state=true` when completed rips are recorded, and
     `decision_type=title_rip` with `decision_result=skipped` for titles
     preserved across the restart; `event_type=makemkv_rip_cancelled` when a
     force-stop killed a rip. A fresh queue item for the same fingerprint with
     no recorded completed rips discards inherited encoded state
     (`kept_encoded_state=false`); it cannot resume the previous item's encode.
     Timing caveat: a resumed run's native `durationSeconds`/task timing covers
     only that run, while `total_wall_time` includes time the daemon was stopped
     — do not flag that mismatch as a hang.
   - A drain during commentary transcription can log
     `commentary_detection_failed` and temporarily persist conservative
     commentary labels even when the handler returns success. The executor now
     records `stage_canceled` and reverts the analysis task to pending if its
     context was cancelled; the resumed analysis run overwrites those labels.
     Correlate the warning with native analysis transitions and the final
     `audio_analysis`/output labels before reporting a lasting commentary
     defect. A cancellation warning followed by a successful rerun is
     interruption context, not evidence that the final output kept the fallback
     labels.
   - What IS a finding around a stop/restart: an item marked failed by the
     interruption itself (e.g. `error_message` containing `signal: killed` or
     `context canceled` — cancellation must revert tasks to pending, never fail
     the item), a `stage_interrupted` transition (the daemon died under the run:
     crash or kill, never a drain; the pre-flagged `stage_interrupted` anomaly
     names the stage, the transition keeps its last progress, and
     `daemon-console.log.prev` in the state directory holds any panic), or
     Reel's `event_type=resume_state_discarded` warning when neither the source
     was re-ripped nor reel/encode settings changed (after a reel upgrade or a
     rip re-run it is the designed auto-reset, costing a from-scratch encode but
     producing correct output). When this warning appears on a fresh item for
     the same fingerprint, inspect earlier daemon logs for a previous queue run
     and disk/write failures; the current item's audit logs intentionally
     exclude earlier items. With the fresh-item staging reset, inherited resume
     state should no longer cause this warning.
   - Level layout: the stage executor records `stage_start` and exactly one
     terminal outcome per run in the queue journal, NOT at DEBUG in daemon logs.
     `stage_complete` has numeric `durationSeconds`; failed, cancelled, stopped,
     degraded, or interrupted runs have their own terminal type. The workflow
     still writes INFO "stage started/completed" decision logs, with a
     human-readable `stage_duration`; "item stage derived" is raw DEBUG. Every
     line logged in a stage run carries `stage`, so digest warning, error, and
     event rows show it in parentheses.
   - Failed items get a durable record too: `metrics.jsonl` in the state
     directory appends `outcome=failed` with `failed_stage`, `error`, and stage
     timings when an item fails, alongside the `outcome=completed` records, so a
     failure stays findable after logs expire and the queue is cleared.
   - Transcription is BATCHED: one WhisperX run per batch is logged as a
     `decision_type=transcription_profile` decision (message "running WhisperX
     transcription", `batch_files` extra), while `transcription_extract` and
     `transcription_whisperx_complete` fire per file. Every file in a batch
     reports the batch's shared WhisperX `duration_ms`; do not sum them as
     separate runs.
   - Episode identification acquires title-vetted OpenSubtitles references
     across the TMDB season, then compares five-minute middle excerpts in one
     Jev Choice per source. Full primary-audio WhisperX transcripts and word
     timestamps remain shared with downstream analysis/subtitle adoption.
     Episode identification and adoption each log their own OpenSubtitles
     searches; adoption never reuses the identification reference except
     through its own source-aware ranking and the shared download cache.
   - Rip-cache restores and stores hardlink when cache and staging share a
     filesystem: near-instant `copy_progress` (a single jump to 100%) is
     expected, not a truncated copy.

3. **Data flow anomalies**:
   - Track counts changing unexpectedly between stages
   - Reconcile TV counts across `makemkv_scan_complete.titles_found`,
     title-selection decisions, `episode_placeholders`, the episode manifest,
     and ripped assets. A contiguous resolved sequence does not prove the first
     or last episode is present. If a disc listing includes E1 but a selected
     title remains unresolved, trace the title selection, trusted show/season
     identity, catalog/reference coverage and title trust, transcript asset
     status, and Jev decision before accepting a missing-E1 conclusion. An
     unresolved title is not a probable extra; high episode probability alone
     does not prove complete file coverage.
   - Title-level TV deduplication only runs on Blu-ray segment maps: DVD maps
     are title-local and TitleHash is metadata-only, so identification refuses
     to dedup non-Blu-ray titles at all. A `duplicate_detection` decision
     carrying `title_id`/`duplicate_of` on a DVD should never appear — if one
     does, treat it as a CRITICAL missing-episode risk and a bug in the dedup
     gate.
   - Episode counts not matching expectations. For TV, cross-check against a
     credible disc-specific web listing when available, even on DVDs; trace any
     excess/missing titles to the selection decision and disc structure rather
     than assuming every episode-length scan title is an episode.
   - File sizes that seem wrong for the content

4. **LLM decision review** (from `analysis.decision_groups`):
   - `decision_type=commentary_classification` entries
   - `decision_type=tmdb_match` and `decision_type=tmdb_match_preference`
     entries — verify acceptance thresholds are reasonable
   - Evaluate probabilities against the rule that produced them: commentary
     0.65, episode identification 0.90. Both use typed Jev Choice; probability
     comparisons and fixed decision reasons are intentional, not missing
     generated explanations. There is no chat pair-verification fallback.
   - Exclude subtitle/transcript wording from this review; use decisions,
     failure events, track metadata, and applicable external disc evidence.

5. **TV episode pipeline checks** (TV only, from `analysis.decision_groups`,
   `logs.warnings`, and native `transitions`):
   - Transitions with `stage=episode_identification` — verify the stage
     started/completed or identify where it failed
   - `decision_type=episode_id_skip` entries — explain legitimate skips for
     non-TV content
   - `decision_type=episode_placeholders` — confirm placeholders were created
     before content ID
   - `event_type=tmdb_season_error` stops season acquisition for retry;
     `event_type=episode_classification_failed` leaves the affected title
     unresolved for review. Missing/incomplete catalogs degrade the stage;
     per-title evidence problems, abstentions, and low probabilities can leave
     review outcomes even when the stage completes.
   - `decision_type=contentid_matches` with
     `decision_result=classify_full_season` or `review` records the plan;
     `resolved_episodes_routed_to_review` records a structural safety failure
     affecting the resolved set.
   - `decision_type=episode_match` uses `decision_result=matched` or `review`;
     inspect `episode_key`, `title_id`, `candidate`, `match_probability`,
     `probability_threshold`, and `decision_reason` in each entry's
     extras/reason. When `candidate=none`, the logged probability belongs to
     `none`, not an episode. On evidence/API failure it is zero, not a
     successful negative classification.
   - TV title exclusions carry their evidence:
     `outlier_bar_seconds`/`weighted_median_seconds` on `gross_runtime_outlier`,
     `expected_runtimes_seconds` on
     `expected_runtime_mismatch`/`over_expected_episode_count`, and
     `min_title_length` on `below_min_title_length` (all INFO) — compare the
     excluded title's `duration` against these to judge the exclusion
   - Asset keys are PERMANENT placeholder identifiers (stable-key model):
     `episodeid` never renames `s01_001`-style keys. Episode identity lives in
     `envelope.episodes[]` fields (`season`, `episode`, `episode_end`) -- join
     assets to episodes by key and read identity from those fields.
     Placeholder-looking keys in logs, review reasons, and final routing are
     correct, not a defect
   - **Do not stop at episode-ID quality.** If organizer/review routing is
     implicated, compare per-episode review state against final destinations.
