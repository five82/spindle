## Audit Report Format

**Only include sections applicable to the item's stage gate.** Omit sections for
stages the item never reached. For failed items, the report should focus on
diagnosing the failure rather than listing empty sections.

### Presentation Density Guidelines

The analysis must remain exhaustive, but the _presentation_ should be
proportional to findings. Use compact formats for clean data and expand only
where anomalies exist.

**Issues Found actionability:**

- Lead with the root cause (code bug vs non-code cause, with evidence and the
  appropriate fix). Group dependent symptoms such as unresolved identity,
  subtitle skip, and review routing under their cause unless they require
  independent action. Never recommend hand-editing a bad output as the fix for
  an upstream bug.
- Only put items in **Issues Found** when there is a real defect, user-visible
  impact, review/failure routing, an unexpected mismatch, or a near-threshold
  condition worth monitoring.
- Do not promote normal telemetry into an INFO finding. If no corrective action
  is needed, keep it in the relevant Artifact Analysis section as neutral
  context or omit it.
- Use `[INFO]` findings sparingly for unusual/borderline observations, not for
  expected below-threshold QC flags.

**Cross-episode data (TV):**

- Build the majority profile line directly from
  `analysis.episode_consistency.majority_profile` and deviation list from
  `analysis.episode_consistency.deviations`
- When all episodes match (`majority_count == total_episodes`), use a single
  summary line:
  `"All 12 episodes: AV1 1436x1080, 1x Opus mono eng, 1x subrip eng"`
- Only expand to a per-episode table when `deviations` is non-empty, and only
  show the differing fields
- Note: for TV items, `media[]` only contains the representative probe,
  deviation probes, and error probes. Use `media_omitted` to report how many
  clean probes were compressed. The representative probe has
  `representative: true`.
- Duration and size ranges come directly from `analysis.media_stats`:
  `"Duration: 1485-1520s | Size: 292-557 MB"`

**Decision traces:**

- `analysis.decision_groups` already provides the deduplication -- show
  identical repeats as `"type x{count}: result (reason)"`; expand a group's
  `entries` only for decisions with different outcomes, notable parameter
  variations, or anomalous confidence/scores
- For episode decisions, show candidate, probability, the 0.90 rule, and
  match/review reason. Distinguish an accepted identity below threshold (a bug)
  from an unresolved candidate below threshold (correct abstention requiring
  review); `candidate=none` carries P(none).

**Episode manifest:**

- Always show the full per-episode table with episode probabilities, canonical
  episode numbers, titles, and review flags/reasons. The manifest records the
  pipeline's decisions, not independent semantic verification. This table is
  never compressed.

**External validation:**

- When all checks confirm, use a compact paragraph rather than multi-level
  section/subsection structure
- Only expand into detailed comparison when a mismatch is found

**Do not report as findings (these are normal):**

- Individual subtitle wording or transcription accuracy — subtitle content is
  outside this skill's scope
- A subtitle skip as CRITICAL on its own — determine whether it is a consequence
  of an upstream bug or a genuine no-verified-candidate outcome; report the
  latter once as a WARNING with its recovery path
- Non-sequential disc title ordering — disc layout varies by manufacturer and is
  irrelevant once content ID resolves episodes
- Inconsistent source audio track counts across titles on the same disc —
  different playlists routinely carry different language sets
- Audio refinement stripping non-English tracks — that's its job
- A drain/deploy restart mid-item — `daemon_drain` decisions, cancelled
  non-drive workers, duplicate stage runs, `startup_queue_state`, skipped
  already-ripped titles, and encodes resuming from completed chunks are the
  designed `spindle stop` behavior (see the drain signature in Phase 2)
- Reel's "discarded stale resume state" warning right after a reel upgrade or a
  re-ripped source — the designed auto-reset; a finding only when nothing
  changed to explain it
- Subtitle `qc_observations` that are below review thresholds and have
  `validation_result=passed`
- An adopted subtitle ending before long credits, or a `reference_tail_gap_s` at
  or below 600 seconds — Matroska duration is a cue span and sparse WhisperX
  end-credit hallucinations can make the raw reference appear longer
- Missing HDR10+ dynamic metadata in encoded output — Reel intentionally emits
  static HDR because the target playback environment does not consume it
- Target-quality chunks with `stop_reason=rate_capped` (or a
  `TQ rate-capped chunks` line) scoring below the band — the level 5.1 bitrate
  cap holding on heavy-grain chunks is the designed playback-compat trade-off;
  informational only, not a retest or re-encode finding
- A `grain_treatments[]` entry with `treated: true` (denoise plus a grain
  table), or an untreated entry whose applicable sample median
  (`stage2_median_bpp` when re-measured, otherwise `median_bpp`) is below
  `treatment_bpp_cutoff` — the gate working as designed. High whole-file bpp
  alone is not a gate false negative; report it only if an independent quality
  check finds a defect. A treated title whose `denoise_ceiling_jod_min` falls
  below the recorded `band_top_jod` (default 9.75), or a treated title with no
  ceiling measured, is worth reporting
- A movie's encoding task holding the `encode` claim with no encoded output
  while its rip runs — the deferred plan is expected; see Stage Gating above
- An identification-failed item having no rip, encode, or staging artifacts —
  that is the fatal no-TMDB-match rule working, not missing work

**Stage timing:**

- Always show the timing table — it's compact and useful for spotting anomalies

### Report Template

```text
## Audit Report for Item #<id>

**Title:** <item.disc_title>
**Stage:** <item.stage> | **Running Tasks:** <comma-separated `type:progress_percent%` for each item.tasks[] with state=running> | **NeedsReview:** <item.needs_review> | **ReviewReasons:** <item.review_reasons joined with "; ">
**Media Type:** <stage_gate.media_type> | **Source:** <stage_gate.disc_source>
**Debug Logs:** <logs.is_debug>

### Executive Summary
<1-2 sentence overview of findings>

### Issues Found

**[CRITICAL] <Root Cause>**
- Cause: <code bug and violated invariant, or non-code condition; separate confirmed facts from inference>
- Evidence: <specific data from the audit output, including the earliest wrong decision>
- Expected: <what should have happened>
- Actual: <what did happen, including downstream consequences>
- Impact: <user-facing consequence>
- Fix: <underlying code change or non-code condition to address, not an output workaround>
- Verification: <regression test plus reprocess/re-audit where possible>

**[WARNING] <Issue Name>**
...

**[INFO] <Observation>**
...

### Artifact Analysis

#### Log Analysis
- Log files: <logs.paths>
- Lines scanned: <logs.lines_scanned>
- INFO events/progress: <summarize notable logs.events; note logs.events_omitted if progress ticks were downsampled; expand long-running progress/timing anomalies only>
- Native transitions: <summarize stage terminal outcomes, repeated starts/retries, and per-episode encoding_substage changes from full JSON transitions; distinguish cancellations/degraded runs from failures>
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
- Catalog/completion: <content_id.reference_source, reference_episodes, completed; completion does not mean every title cleared review>
- Probability overview: <analysis.episode_stats.probability_min/max/mean and below_090 for resolved identities; unresolved count; content_id.review_episodes snapshot>
- Episode manifest: <full per-episode table with match_probability, canonical numbers/titles, and current review flags/reasons; pre-episodeid placeholders are an inventory, not failed matches>
- Structural safety: <runtime, overlap, and sequence reasons; sequence_contiguous and episode_range; review routing and concurrent asset/flag preservation>
- Verification scope: <decision/metadata integrity only; semantic identity and file completeness are not independently established by probability>

#### Encoded File (if phase_encoded)

- Final output validation: <from analysis.final_validation: per-output pass/fail, any failed_checks, and the av_sync source offset -> output offset with signed drift; explain unavailable entries and a missing verdict>

**Movie:**
- Video: <codec> <resolution> <HDR status> | Duration: <seconds>s | Size: <bytes>
- Audio: <stream summary>
- Encoding config: <encoding.snapshot.quality> | SVT-AV1 preset <encoding.snapshot.preset> | tune <encoding.snapshot.tune> | <encoding.snapshot.audio_codec>
- Crop: <analysis.crop_analysis.filter> (<analysis.crop_analysis.standard_ratio>)
- Grain treatment: <from analysis.grain_treatments: treated tier or untreated with reason, median_bpp vs cutoffs, and the denoise ceiling JOD mean/min for treated titles>
- Validation: <passed/failed, expand individual steps only if failed>

**TV:**
- Common profile: <from analysis.episode_consistency.majority_profile>
- Encoding config: <encoding.snapshot.quality> | SVT-AV1 preset <encoding.snapshot.preset> | tune <encoding.snapshot.tune> | <encoding.snapshot.audio_codec>
- Duration: <analysis.media_stats.duration_min_sec>-<max>s | Size: <analysis.media_stats.size_min_bytes>-<max>
- Cross-episode consistency: <analysis.episode_consistency — pass if no deviations, else list deviations>
- Grain treatment: <from analysis.grain_treatments: which episodes were treated, at which tier, and the lowest denoise ceiling JOD across them>
- Failed episodes: <count, with details if > 0>

#### Subtitle Pipeline (if phase_subtitles)
- Source: <per-title adoption outcome from subtitle_generation_results[].source — opensubtitles/none; for skips, the subtitle_source rejection reasons>
- Tracks: <count and config from media probes>
- Stream layout and labels: <from analysis.final_validation: passed, or the failed_checks naming the stream count, codec, language tag, label, or forced/default flag>
- Validation result: <aggregate subtitle_generation_results.validation_result; list structured review_issues only when they affected routing, without inspecting cue text>
- Subtitle mux/output: <mux status and the apply stage's subtitle layout verdict; skipped titles legitimately have no subtitle stream>
- Content review: <not performed; subtitle text is out of scope. For skips, name the upstream cause; recommend subtitle-specific recovery only for a genuine no-verified-candidate outcome>

#### Commentary (if phase_commentary)
- Decisions: <from analysis.decision_groups; for Jev include P(commentary) and its 0.65 rule, not the episode-identification 0.90 gate>
- Conservative fallbacks: <classification/transcription failures, if any; zero stored confidence is not a successful negative decision>
- Tracks in output: <count from media probes>

### External Validation (if phase_external_validation)
<Compact paragraph when all checks pass. Expand into detailed comparison only when mismatches found.>

### Decision Trace
<From analysis.decision_groups — type x{count}: result (reason) for identical groups. Expand entries only for groups with varying messages or anomalous results.>
```

## Execution Checklist

After running `spindle queue audit`, check only the phases flagged as `true` in
`stage_gate`. **Do not check phases beyond the reached stage.**

### Always

- [ ] Ran `spindle queue audit <id>`, read the full digest, noted the JSON path
- [ ] Checked gathering errors (digest header) for incomplete data
- [ ] Reviewed `stage_gate` to determine applicable phases
- [ ] Reviewed pre-flagged anomalies
- [ ] Reported any `keydb_download_error` stale-catalog fallback as a WARNING
- [ ] Analyzed logs/decisions for anomalies beyond simple error counts, drilling
      into the full JSON wherever the digest flagged an omission or something
      looked off
- [ ] Read full JSON `transitions` for stage starts/terminal outcomes and
      encoding substages; used `analysis.stage_timings` for the timing table,
      not nonexistent `logs.stages`
- [ ] If TV: reconciled scanned, selected, placeholder, manifest, ripped, and
      final episode counts; checked a credible disc-specific episode listing if
      available (DVD included); investigated every reduction or excess
- [ ] For failed items: diagnosed failure cause from `item.error_message` and
      log events
- [ ] Traced each finding to its earliest wrong decision or non-code cause;
      grouped dependent symptoms and distinguished evidence from inference

### Post-Ripping (phase_rip_cache)

- [ ] Analyzed rip cache metadata
- [ ] If TV: validated per-episode ripped assets in `envelope.assets.ripped`

### Post-Episode-Identification (phase_episode_id)

- [ ] Checked `envelope.attributes.content_id` method, TMDB catalog, completion,
      and matched/unresolved/review counts
- [ ] Reviewed every manifest entry's `match_probability` against 0.90;
      distinguished rejected candidates and `none` from accepted identities
- [ ] Traced unresolved outcomes to catalog/evidence/classifier reasons without
      inspecting transcript text or assuming extras
- [ ] Checked runtime, overlap, sequence, and canonical-numbering safeguards;
      did not infer completeness from high probability
- [ ] Verified current per-episode review flags, concurrent encoding assets, and
      final routing; did not treat summary completion as review clearance

### Post-Encoding (phase_encoded, phase_crop)

- [ ] Read `analysis.final_validation`: confirmed a verdict exists for every
      output, investigated failed checks and unavailable entries, and did not
      rely solely on Reel's persisted validation verdict
- [ ] Analyzed streams from `media[]` entries (video, audio, subtitle)
- [ ] Validated crop detection from `encoding.snapshot.crop_filter`
- [ ] Read `analysis.grain_treatments`: noted which encodes were treated and at
      which tier, and checked every treated encode's `denoise_ceiling_jod_min`
      against the 9.75 band top
- [ ] Reviewed the apply stage's commentary, audio layout, and subtitle layout
      verdicts
- [ ] Correlated any `source_timeline_normalization` decision with Reel's
      normalization step and the final audio endpoint verdict
- [ ] If TV: checked cross-episode consistency

### Post-Audio-Analysis (phase_commentary)

- [ ] Reviewed commentary decisions from `analysis.decision_groups`,
      interpreting Jev probabilities with the 0.65 rule rather than an
      LLM/episode confidence gate
- [ ] Checked conservative error fallbacks and verified that similarity did not
      exclude a track classified or preserved as commentary
- [ ] If TV: verified cross-episode program-audio consistency; allowed
      episode-specific commentary tracks with valid labels/dispositions

### Post-Subtitling (phase_subtitles)

- [ ] Read the apply stage's subtitle layout verdict (adopted titles only;
      `source=none` skips legitimately have no stream)
- [ ] Checked only aggregate adoption/validation outcomes and routing
- [ ] Did not open, extract, sample, quote, compare, or judge
      subtitle/transcript content
- [ ] If TV: checked per-episode subtitle asset status

### External Validation (phase_external_validation)

- [ ] Looked up blu-ray.com review
- [ ] Validated crop and commentary count against review

### Report

- [ ] Generated report with only applicable sections
- [ ] Applied presentation density guidelines (compact for clean data, expanded
      for anomalies)
- [ ] Used `analysis.decision_groups` for decision trace
- [ ] Recommended an underlying fix or identified the non-code condition to
      address; if a fix was requested, verified it by reprocessing and
      re-auditing when possible, or stated the blocker
