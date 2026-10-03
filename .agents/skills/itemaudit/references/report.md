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
  match/review reason. Distinguish an accepted identity below its rule (a bug)
  from a slot-corroborated identity (accepted at or above 0.50 because its
  winner filled the disc's only open slot) and from an unresolved candidate
  below threshold (correct abstention requiring review); `candidate=none`
  carries P(none).

**Episode manifest:**

- Always show the full per-episode table with episode probabilities, canonical
  episode numbers, titles, and review flags/reasons. The manifest records the
  pipeline's decisions, not independent semantic verification. This table is
  never compressed.

**External validation:**

- When all checks confirm, use a compact paragraph rather than multi-level
  section/subsection structure
- Only expand into detailed comparison when a mismatch is found

**Do not report as findings (these are normal).** Each rule's conditions live
in the reference named; apply them there. This list is the reporting-time
reminder, not a second definition:

- Subtitle or transcript wording quality (SKILL.md scope); text read as
  evidence supports a finding about a decision, never one about the text
- A subtitle skip as CRITICAL on its own; report a genuine no-verified-candidate
  skip once as a WARNING (encode-and-subtitles.md Phase 6.2)
- Non-sequential disc title ordering — disc layout varies by manufacturer and is
  irrelevant once content ID resolves episodes
- Inconsistent source audio track counts across titles on the same disc —
  different playlists routinely carry different language sets
- Audio refinement stripping non-English tracks — that's its job
- Drain/deploy restarts and their resume evidence (logs.md Phase 2)
- `resume_state_discarded` after an upgrade or a re-ripped source (logs.md
  Phase 2)
- Below-threshold subtitle `qc_observations` with `validation_result=passed`
  (encode-and-subtitles.md Phase 6.3)
- An adopted subtitle ending before long credits, or a `reference_tail_gap_s`
  at or below 600 seconds (encode-and-subtitles.md Phase 6.1)
- Missing HDR10+ dynamic metadata (encode-and-subtitles.md Phase 4.2)
- `rate_capped` target-quality chunks below the band (encode-and-subtitles.md
  Phase 4.6)
- Grain-gate verdicts working as designed, including high whole-file bpp on an
  untreated title; the exceptions are pre-flagged anomalies
  (encode-and-subtitles.md Phase 4.6)
- A movie's encoding task holding the `encode` claim while its rip runs, shown
  as a Stage runs "waited: input" line (logs.md Phase 2)
- A resolved disk-space wait on an item that later succeeds (logs.md Phase 2)
- An identification-failed item with no rip, encode, or staging artifacts
  (schema.md Stage Gating)

**Stage timing:**

- Always show the timing table, built from the digest's Stage runs (outcome
  and duration per run); it is compact and useful for spotting anomalies

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
- Native transitions: <from the digest's Stage runs: terminal outcome per run, repeated starts/retries, waits, activities left open (Reel encoding phases are activities); distinguish cancellations/degraded runs from failures>
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
- References/completion: <content_id.reference_source, usable reference_episodes, completed; reference_search selected/omitted reasons and file IDs; completion does not mean every title cleared review>
- Probability overview: <analysis.episode_stats.probability_min/max/mean, below_090 (acceptance-rule violations), and slot_corroborated for resolved identities; unresolved count; content_id.review_episodes snapshot>
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
- Grain treatment: <from analysis.grain_treatments: treated or untreated with reason, median_bpp (or stage2_median_bpp) vs treatment_bpp_cutoff, and the denoise ceiling JOD mean/min for treated titles>
- Validation: <passed/failed, expand individual steps only if failed>

**TV:**
- Common profile: <from analysis.episode_consistency.majority_profile>
- Encoding config: <encoding.snapshot.quality> | SVT-AV1 preset <encoding.snapshot.preset> | tune <encoding.snapshot.tune> | <encoding.snapshot.audio_codec>
- Duration: <analysis.media_stats.duration_min_sec>-<max>s | Size: <analysis.media_stats.size_min_bytes>-<max>
- Cross-episode consistency: <analysis.episode_consistency — pass if no deviations, else list deviations>
- Grain treatment: <from analysis.grain_treatments: which episodes were treated, and the lowest denoise ceiling JOD across them>
- Failed episodes: <count, with details if > 0>

#### Subtitle Pipeline (if phase_subtitles)
- Source: <per-title adoption outcome from subtitle_generation_results[].source — opensubtitles/none; for skips, the subtitle_source rejection reasons>
- Tracks: <count and config from media probes>
- Stream layout and labels: <from analysis.final_validation: passed, or the failed_checks naming the stream count, codec, language tag, label, or forced/default flag>
- Validation result: <aggregate subtitle_generation_results.validation_result; list structured review_issues only when they affected routing; no wording-quality judgments>
- Subtitle mux/output: <mux status and the apply stage's subtitle layout verdict; skipped titles legitimately have no subtitle stream>
- Content review: <subtitle quality not reviewed (out of scope); note any text read as decision evidence. For skips, name the upstream cause; recommend subtitle-specific recovery only for a genuine no-verified-candidate outcome>

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
      looked off; for a fresh-item resume warning, checked prior
      same-fingerprint runs outside the current item's clamped log window
- [ ] Read the digest's Stage runs for every run's terminal outcome, waits,
      and open activities (JSON `transitions` for exact
      sequences); used them for the timing table
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

- [ ] Checked `envelope.attributes.content_id` method, canonical TMDB catalog,
      usable OpenSubtitles references, completion, and matched/unresolved/review
      counts
- [ ] Traced `reference_search` selection/omission and file IDs; checked title
      trust, retry identity reset, and full-transcript sharing; did not
      mistake dialogue agreement for label verification
- [ ] Reviewed every manifest entry's `match_probability` against 0.90
      (0.50 for `slot_corroborated` entries, after checking the slot rule
      held); distinguished rejected candidates and `none` from accepted
      identities
- [ ] Traced unresolved outcomes to catalog/reference/evidence/classifier
      reasons without assuming extras; for a close call, read the runner-up
      and P(none), then compared source and reference excerpts when those
      left the cause open (re-transcription only with operator approval)
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
      whether it was re-measured at stage 2, and checked every treated encode's `denoise_ceiling_jod_min`
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
- [ ] Checked adoption/validation outcomes and routing; read candidate or
      reference text only to diagnose an adoption decision
- [ ] Did not judge subtitle wording, line breaks, reading speed, or
      translation; quoted only the lines needed as decision evidence
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
