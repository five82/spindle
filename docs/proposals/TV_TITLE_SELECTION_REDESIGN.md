# Spec Proposal: Evidence-Based TV Title Selection

Status: Accepted and implemented (2026-07-05). Implementation notes: no
envelope schema change was needed (expectations are consumed inside the
identify stage), and no organizer change was needed (contentid flags episodes
for review, which the existing partition routing already honors). The
runtime-mismatch filter uses a 25% tolerance and requires at least one
candidate to match expectations before it may drop anything.

## Summary

TV title selection currently makes permanent keep/drop decisions using only
intra-disc runtime statistics (clustering, median ratios, duration bands).
Those statistics are guesses about disc authoring conventions, and every new
authoring style breaks a guess. This proposal inverts the policy:

> **Statistics may rank titles. Only structural evidence may drop them.**

Titles are excluded only for structural reasons (too short, exact duplicate,
proven play-all composite). Everything episode-plausible is ripped, and the
episode identification stage — which has transcripts, references, and
per-episode confidence — becomes the arbiter of what is actually an episode.
The runtime-cluster machinery that exists to avoid ripping extra titles is
deleted.

This trades an occasional cheap, recoverable cost (ripping and transcribing a
title that turns out to be an extra) for eliminating an expensive,
unrecoverable failure class (a real episode silently dropped before ripping,
partial output delivered to the library, manual reprocessing).

## Motivating failure: Breaking Bad S01 Disc 1

Item #1 (2026-07) completed with only S01E02-S01E04 in the library. The
S01E01 pilot was never ripped.

Disc titles: 2891s, 2892s, 2895s, and 3486s (the pilot; Breaking Bad's pilot
runs 58 minutes against ~48-minute regular episodes).

What happened in `internal/identify/tv_titles.go`:

1. Clustering starts a new cluster when the duration gap exceeds
   `max(480s, 20% of previous title)`. The pilot's gap was 591s against a
   579s threshold — **excluded by 12 seconds**.
2. The double-episode rescue only admits titles at 1.8x-2.4x the primary
   median. A 1.2x pilot can never qualify.
3. The pilot's single-title cluster failed the `total*2 >= maxTotal`
   eligibility gate, fell through to the catch-all `runtime_cluster_extra`,
   and **no ambiguity flag fired**. Selection was confidently wrong.
4. Downstream, content ID matched the three rips as E02-E04 at 0.99
   confidence and raised the structural review reason
   `disc 1 matched subset starts at episode 2` — proof the pipeline *knew*
   the output was wrong — but the review flag does not gate library
   placement, so a partial season was delivered anyway.
5. TMDB already knew the pilot runs 58 minutes (3480s). The excluded title
   was 3486s — a 6-second match. The evidence to self-correct existed and
   was never consulted.

## Why this is a failure class, not a bug

`tv_titles.go` is three months old. Six of its eight commits are fixes for
specific discs: opening double episodes (d1ff3e0), runtime clusters vs
doubles (c830151), combined-playlist pilots (80dc207), seamless-branch
pilots (6e0be79), independent doubles (a7b8706), play-all playlists
(0de1f56, 045c3b5). Before that the same pattern hit episode matching
(Batman 1966: 4f00edd, 37dfefa, 531c196). After all of that, the file still
dropped the pilot of one of the most-pressed Blu-ray sets in existence.

The structural diagnosis:

1. **The irreversible decision is made at the point of least information.**
   Selection permanently drops titles knowing only durations and segment
   maps. Episode identification runs later with vastly better evidence and
   can *detect* selection's mistakes (`structuralReviewReasons`) but cannot
   *recover* them — the excluded title was never ripped and nothing loops
   back.
2. **Statistical exclusions are the bug source; structural ones are not.**
   Duplicate-key dedup and segment-union play-all detection are facts about
   the disc and have not produced failures. Cluster gaps, median ratios, and
   duration bands are guesses, and every fix commit above is a broken guess
   patched with another band or special case.
3. **Threshold tuning cannot fix this.** Moving the 20% gap to 25% fixes
   Breaking Bad and relocates the breakage to some other show's runtime
   profile. The class of bug survives any constant.
4. **TMDB expectations are fetched but unused where they matter.** At
   selection time the show and season are identified; the season episode
   list (count and per-episode runtimes) is one call away and already
   fetched later by content ID. Selection consults none of it.

## Design invariant

> No title may be permanently excluded from a TV disc on statistical
> runtime evidence alone. A title is excluded only when structural evidence
> proves it is not an episode, or content evidence fails to match it to one.

Everything below follows from enforcing this invariant.

## Proposed design

### 1. Title selection (`internal/identify`)

Selection keeps only structural exclusions:

- `below_min_title_length` — under `MakeMKV.MinTitleLength` (existing).
- `gross_runtime_outlier` — under half the candidate median (existing
  filter concept from 323d1e3; catches menus, trailers, credits reels).
- `duplicate_title` — same dedup key / segment map (existing).
- `combined_play_all_extra` — segment map is explainable as the union of
  other selected titles (existing segment-union proof). This subsumes the
  play-all and combined-double detection that exists today; the proof is
  structural, so it stays.

Everything else episode-plausible is selected for ripping, bounded by
expectation:

- Fetch the TMDB season episode list during identification (season number
  is already known; content ID already makes this call — fetch once, store
  in the envelope, and let content ID reuse it).
- If plausible titles exceed the expected remaining episode count for the
  disc (plus slack), rank by expected-runtime fit (TMDB per-episode
  runtimes) and prefer the best-fitting set; log what was left behind and
  flag ambiguity. TMDB runtimes are a *ranking* signal only — they are
  often missing or rounded for older shows, and their absence must never
  exclude a title.

Deleted outright: cluster formation, primary-cluster election, the
double-pattern cluster pairing, the 1.8x-2.4x qualifying band,
combined-vs-independent double classification as a selection gate, and the
ambiguity flags that exist to hedge those heuristics
(`competing_runtime_clusters`, `extras_dominate_candidates`,
`multiple_double_episode_candidates`, `primary_cluster_single_title`).
Roughly half of `tv_titles.go`'s 569 lines exists to serve the machinery
this removes.

### 2. Episode identification (`internal/contentid`)

Content ID already performs one-to-one constrained matching with margins,
LLM tie-breaks, and single-hole reconciliation. It gains one disposition it
currently lacks:

- A rip that matches **no** candidate episode after verification is
  classified `probable_extra` rather than `unresolved`. It is routed to the
  review directory (not the library, not silently deleted) and does not
  block the item's real episodes.

This is the counterpart that makes over-selection safe: falsely included
titles are rejected by content evidence and cost nothing but rip time.
The asymmetry is the point — content ID can reject a bad inclusion, but
nothing can resurrect a bad exclusion.

### 3. Library gating (`internal/organizer`)

Structural review reasons become gating, not advisory:

- `disc 1 matched subset starts at episode N` and
  `accepted episode subset is fragmented` route the item's episodes to the
  review directory instead of the library. A known-incomplete season set
  must not be delivered next to (or instead of) a complete one.

Per-episode low-confidence review handling is unchanged.

## What this fixes on the motivating disc

All four plausible titles (including the 3486s pilot) are structurally
sound and within the expected count for disc 1, so all four are ripped.
Content ID matches them as E01-E04. No review flag, complete output. Had
the pilot genuinely been a play-all of shorter titles, the segment-union
proof would still have excluded it.

## Costs and risks

- **Extra rip/transcription time.** The main cost. Bounded by the expected
  episode count; in practice the titles this newly admits are exactly the
  borderline ones (pilots, doubles, specials) that are usually real
  episodes. True extras at episode length that survive the structural
  filters are rare, and each costs minutes, not a reprocess.
- **False `probable_extra`.** A real episode with a bad transcript or no
  usable reference could be misfiled as an extra. Mitigation: it goes to
  the review directory with its evidence, never silently dropped — strictly
  better than today, where the same episode would either block the item or
  never have been ripped at all.
- **TMDB metadata quality.** Missing episode counts or runtimes degrade the
  bounding/ranking, not correctness: with no expectation available,
  selection admits all structurally plausible titles and content ID
  arbitrates, which is the safe direction.
- **Longer discs (many episodes + many doubles).** The expected-count bound
  plus runtime ranking keeps rip volume sane; ambiguity is logged when
  titles are left behind.

## Non-goals

- **No automatic re-rip recovery loop.** Closing the loop (content ID
  detects a gap, re-rips a matching excluded title) is a stage-flow
  architecture change. With statistical exclusion gone, the gap it would
  recover from should no longer occur; revisit only with evidence, and
  likely on top of the task scheduler.
- **No expansion of the LLM's role.** The pairwise verifier is unchanged.
  The deferred LLM candidate picker proposal is orthogonal — this failure
  was not a matching ambiguity.
- **No threshold retuning.** The cluster constants are deleted, not tuned.
- **No change to movie title selection** (`internal/ripper/selection.go`).

## Complexity accounting (per AGENTS.md)

- Production LOC: expected net negative. Deletions in `tv_titles.go`
  (clustering/election/double machinery) should exceed additions
  (season-list fetch in identify, runtime ranking, `probable_extra`
  disposition, organizer gating).
- No new packages, interfaces, config flags, or goroutines anticipated.
  One new envelope field (season episode expectations) replaces a duplicate
  TMDB fetch in content ID.
- New decision reasons (`expected_episode_runtime_rank`, `probable_extra`)
  replace a larger set of deleted cluster-heuristic reasons; all decisions
  remain logged with `decision_type`/`decision_result`/`decision_reason`.
- Tests: existing tv_titles tests contract to the structural rules; new
  tests encode the motivating discs (Breaking Bad pilot, Batman 1966
  duplicates, TNG seamless-branch pilot, play-all discs) as fixtures.

## Validation plan

1. Regression-fixture the known problem discs from git history against the
   new selection: Breaking Bad S01D1 (long pilot), Batman 1966 S01D1
   (duplicate playlists), TNG S1D1 (seamless-branch composite pilot),
   opening-double discs, play-all discs. Each has recorded title tables in
   commit messages, tests, or audit logs.
2. Verify the segment-union proof still excludes every play-all variant in
   those fixtures with the surrounding cluster machinery gone.
3. Reprocess the physical Breaking Bad disc (still in the drive) end to end:
   expect E01-E04 in the library, no review flags.
4. Confirm organizer gating: simulate a disc-start gap and verify episodes
   route to review, not the library.
