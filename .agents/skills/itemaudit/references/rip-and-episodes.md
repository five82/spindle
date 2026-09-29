### Phase 3: Rip Cache Analysis (when `phase_rip_cache` is true)

Analyze the `rip_cache` section from the audit output:

1. **Verify** `rip_cache.found` is true — if false, check `rip_cache.disabled`
   first (cache turned off in config); otherwise the entry may have been pruned
2. **Check metadata**:
   - `disc_title` matches expected content
   - `cached_at`, `title_count`, and `total_bytes` look plausible
3. **Title selection analysis** (movies only):
   - Prefer `analysis.title_selection` for candidate counts, selected title,
     similar runtimes, and selection decision; fall back to `envelope.titles`
     only when the summary is absent
   - Feature-length titles are titles with `chapters > 1` AND `duration > 3600`
     seconds
   - The pipeline uses multi-stage selection (`ChoosePrimaryTitle`), not simply
     the longest title:
     - **Disney multi-language detection**: when 2+ feature-length 800-series
       playlists (00800-00899) exist with runtimes within 30 seconds, the
       pipeline prefers the lowest playlist number (00800.mpls = English). The
       selected title may be _shorter_ than alternatives — this is correct
       behavior for Disney/Pixar/Marvel/Star Wars multi-language discs where
       language variants differ only in localized title cards and credits.
     - **Different cuts**: when 800-series playlists differ by >30 seconds,
       treated as different cuts (theatrical vs director's) and longest is
       preferred.
     - Additional tiebreakers: chapter count, MPLS over M2TS, segment count,
       TitleHash fingerprint frequency.
   - Check `decision_reason` in the decision groups: `"primary_title_selector"`
     indicates the multi-stage algorithm was used.
   - Report which title was selected with playlist and duration context.
     Example: "Selected title 0 (00800.mpls, 6151s / 102.5 min, English) over
     title 1 (00801.mpls, 6181s / 103.0 min) and title 3 (00802.mpls, 6181s /
     103.0 min) via Disney multi-language heuristic"
   - **Flag for review**: if a non-800 playlist was selected when 800-series
     alternatives exist with similar runtimes (possible mis-selection)
   - The ripped asset filename (from `envelope.assets.ripped[].path`) often
     contains a title index (e.g., `_t02`) that maps to the
     `envelope.titles[].id`
   - Include this in the Rip Cache section of the report, not as an issue — it
     is informational context about what was ripped
   - If only one feature-length title exists, note it briefly ("single
     feature-length title on disc")
4. **Per-episode asset validation** (TV only, from `envelope.assets.ripped`):
   - Verify each episode in `envelope.episodes` has a corresponding `ripped`
     asset with matching `episode_key`
   - Pre-episodeid, keys are placeholders (`s01_001`, `s01_002`) with
     `episode=0` — this is expected
   - Check for any ripped assets with `status: "failed"` or missing `path`
   - Verify ripped asset count matches episode count
5. **Asset mapping strategy**: Check `decision_type=asset_mapping` —
   `title_file_map` is the normal path for TV, `directory_scan` is the fallback

### Phase 3b: Episode Identification Validation (when `phase_episode_id` is true)

**TV only.** Analyze `envelope.episodes`, `envelope.attributes`, and
`item.needs_review`:

1. **Content ID provenance**: Check `envelope.attributes.content_id`
   - Expect `method=whisperx_jev_episode_choice`, `reference_source=tmdb`, and
     `review_threshold=0.90`. `reference_episodes` counts catalog entries, not
     subtitle downloads or titles expected on this disc.
   - The classifier uses one typed Choice per full primary-audio transcript
     against every canonical episode in the trusted TMDB season plus `none`.
     There is no reference-subtitle, similarity, disc-position shortlist, or
     forced hole-filling fallback.
   - Read `transcribed_episodes`, `matched_episodes`, `unresolved_episodes`, and
     `review_episodes`. `completed=true` means the classification pass finished,
     not that every title matched or cleared review. Current per-episode review
     flags remain authoritative: concurrent encoding can add review flags after
     the summary was calculated.

2. **Episode manifest review**: Use
   `analysis.episode_stats.probability_min/max/mean`, `below_090`, `unresolved`,
   `placeholder_only`, and `sequence_contiguous` for the overview, then inspect
   every `envelope.episodes[]` entry.
   - Probability aggregates and `below_090` cover resolved identities only. A
     resolved identity below 0.90 (including zero) is **CRITICAL**: it violates
     the acceptance rule. A probability of exactly 0.90 is accepted; it is not
     Jev's separate confidence statistic.
   - `episode=0` means unresolved. A positive stored `match_probability` on such
     an entry is a rejected candidate's probability, not an accepted identity.
     Zero can mean abstention or unavailable evidence/classification;
     distinguish these using the decision reason.
   - Unresolved titles are **WARNING** review outcomes, not proof of mislabeling
     or extras. Investigate missing/unreadable/empty transcripts, incomplete
     titles/overviews, invalid catalogs, classifier failures, or insufficient
     distinctive evidence. Catalogs allow at most 254 episodes plus `none`;
     transcript text plus catalog/instructions must fit 96 KiB, with no
     truncation. Server token-limit failures also route to review.

3. **Canonical match outcomes live in `episodes[]`**:
   - Review `season`, `episode`, `episode_end`, `episode_title`,
     `match_probability`, `needs_review`, and `review_reason`. Never infer
     episode identity from a placeholder key.
   - Preserve canonical TMDB numbering. Runtime must not invent episode ranges
     or shift later episode numbers. High probability may identify only the
     dominant episode in a composite; it does not prove that the entire file is
     one episode.
   - Do not read transcript/subtitle cue text to verify a match. This read-only
     metadata audit can verify decision integrity and structural evidence, not
     independently prove semantic episode identity; state that limit when
     relevant.

4. **Structural safety and persistence**:
   - Inspect overlaps/duplicate assignments, missing source or TMDB runtimes,
     and runtime differences exceeding
     `max(300 seconds, 25% of expected runtime)`. Disc 1 starting after E1 or a
     resolved subset with multiple gaps also triggers review. These safeguards
     flag every resolved title in an unsafe set without changing identities;
     they cannot detect every composite.
   - Use `analysis.episode_stats.sequence_contiguous` and `episode_range` to
     investigate gaps, not to force a permutation. A contiguous sequence alone
     does not prove disc completeness.
   - Check that all uncertainty/safety reasons survive into per-episode review
     flags and final routing, along with review flags and assets written by
     concurrent encoding. A lost review flag or overwritten encoding asset is a
     persistence bug, not a reason to hand-edit the output.

### Phase 3c: Final Output Routing Validation (post-organizing items, especially TV with review flags)

The organizer enforces routing itself: after copying, it re-derives each
output's expected destination from the review flags (TV: resolved + no episode
review flag -> library, otherwise review; movie: the item's review flag) and
fails the organize stage when a recorded final asset is missing or sits under
the wrong root. A misrouted item therefore surfaces as a FAILED item at
`organizing`, not as a quietly wrong library.

1. **Read `envelope.assets.final`** and map final paths by `episode_key`. The
   digest's "Final routing" section shows expected-vs-actual per output for
   display.
2. **If the item failed at `organizing` with `routing verification failed`**,
   that message names the keys and the expected root: diagnose why the flags and
   the routing branch disagree (it indicates an organizer bug, not a content
   problem).
3. For a completed item, confirm the routing summary agrees with the per-episode
   review flags; a disagreement here means the check and the summary disagree,
   which is itself a finding.
4. If the structured audit data is incomplete or suspicious, **inspect the
   actual directories on disk** rather than assuming the envelope tells the
   whole story.
