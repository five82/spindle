# Content ID Design

Episode identification subsystem for mapping disc titles to broadcast episode
numbers using transcript similarity matching.

---

## 1. Purpose

Physical Blu-ray/DVD discs store TV episodes as generic titles (Title00.mkv,
Title01.mkv, etc.) with no episode metadata. Disc title ordering frequently
differs from broadcast order. Content ID resolves this by:

1. Transcribing each ripped title using WhisperX speech-to-text
2. Downloading reference subtitles from OpenSubtitles for the target season
3. Computing text similarity between transcripts and references
4. Decoding the best ordered contiguous episode path with structured dynamic programming
5. Verifying ambiguous matches with an LLM

The matcher is invoked by the episode identification stage after ripping
completes. It updates the rip specification envelope in-place with confirmed
episode mappings so downstream encoding and organizing stages have correct
metadata.

---

## 2. Prerequisites

Content ID requires all of the following:

- **TV content**: `media_type` is `tv` in queue item metadata
- **OpenSubtitles enabled**: `subtitles.opensubtitles_enabled = true`
- **OpenSubtitles API key**: `subtitles.opensubtitles_api_key` configured
- **Ripped files**: At least one episode with a ripped asset path in the envelope
- **TMDB ID**: Present in queue item metadata (used for season episode list)
- **Season number**: Derived from metadata, envelope episodes, or defaults to 1
- **WhisperX**: `uvx` binary available for transcription
- **TMDB client**: For fetching season details (episode list)

If any prerequisite is missing, the matcher returns early with no changes.

---

## 3. Pipeline Overview

```
Ripped Episode Files
        |
        v
[1. Transcribe] -- WhisperX via shared transcription service
        |
        v
[2. Build Candidate Set] -- Determine which episodes to fetch references for
        |
        v
[3. Fetch References] -- Download OpenSubtitles SRTs for candidate episodes
        |
        v
[4. Fingerprint + IDF] -- TF-IDF cosine similarity between rips and references
        |
        v
[5. Ordered Decoder] -- Windowed structured path search over rip/episode order
        |
        v
[6. Confidence Derivation] -- Split raw score from derived confidence
        |
        v
[7. Structural Validation] -- Check contiguity, gaps, and disc-1 constraints
        |
        v
[8. LLM Verification] -- Verify ambiguous matches
        |
        v
[9. Apply Matches] -- Update envelope episodes with confirmed mappings
```

---

## 4. Transcription

### 4.1 WhisperX Execution

For each episode in the rip spec envelope that has a ripped asset:

1. Create working directory: `<staging_root>/contentid/<episode_key>/`
2. Probe the ripped MKV and run the shared primary-audio selection policy.
   Content ID must transcribe the **selected primary audio stream**, not blindly
   `0:a:0`, because multi-language TV discs often place non-English dubs first.
3. Derive the WhisperX language from the selected stream's language tag when
   available; otherwise fall back to the caller default (`en`).
4. Invoke the shared transcription service (see DESIGN_INFRASTRUCTURE.md
   Section 9) with the ripped file path and a content-stable `ContentKey`
   (`disc_fingerprint:episode_key:audio_index`). The selected audio index is
   part of the cache identity so retries do not reuse transcripts from the
   wrong stream after audio-selection fixes. The `whisperxSem` semaphore is held
   by the episode identification stage for the duration.
5. Read the generated SRT file and normalize it (strip SRT formatting, clean text)
6. Create a text fingerprint from the normalized plain text

### 4.2 Progress Reporting

Phase: `transcribe`
Reports: `(current_episode, total_episodes, episode_key)` after each transcript.

### 4.3 Caching

Transcripts are cached by the shared transcription service using
content-stable keys (see DESIGN_INFRASTRUCTURE.md Section 9.3). This
allows later stages (subtitling) to reuse episode ID transcripts without
re-running WhisperX, even though the input file path changes from the
ripped file to the encoded file.

The cache key must include the **selected audio-relative stream index**. Two
transcripts from the same file but different audio tracks are different inputs
and must never collide.

---

## 5. Reference Downloads

### 5.1 OpenSubtitles Search Strategy

For each candidate episode number, the matcher:

1. Builds a primary `SearchRequest` with: `parent_tmdb_id`, `query` (show title),
   `languages`, `season`, `episode`, `media_type=episode`, `year` (air date year)
2. Generates search variants via `EpisodeSearchVariants()` which produces
   alternative queries (different ID fields, query text) to handle OpenSubtitles
   metadata inconsistencies
3. Tries each variant in order, stopping at the first that returns results

### 5.2 Reference Selection

From the returned subtitle candidates, `selectReferenceCandidate()` picks the best:

1. **Title consistency check**: Skip candidates whose release name contains a
   different episode's TMDB title but not the expected episode's title
2. **Hearing-impaired preference**: Among title-consistent candidates, prefer
   non-HI subtitles (HI annotations dilute similarity against WhisperX transcripts)
3. **Fallback**: If all candidates look suspect, pick the first non-HI or first HI

Selection reasons: `top_result`, `title_consistency_rerank`, `non_hi_preferred`,
`hi_fallback`

### 5.3 Download and Caching

- Downloads use format `srt`
- File cache: keyed by `file_id`, stored under the auto-derived OpenSubtitles
  cache directory (`$XDG_CACHE_HOME/spindle/opensubtitles`)
- Search cache: keyed by variant signature, stores search responses
- Cache hits skip network calls entirely

### 5.4 Rate Limiting

- Minimum 3-second interval between OpenSubtitles API calls (consistent with
  the rate limit enforced by the OpenSubtitles client; see API_SERVICES.md
  Section 3).
- Retriable errors (rate limits, transient server errors) use fixed-delay retry
  (3 retries, 5-second wait; see API_SERVICES.md Section 3)

---

## 6. Candidate Episode Set

### 6.1 Single Candidate Set

Rather than evaluating multiple strategies, the matcher builds a single candidate
episode set using the best available information:

1. **Rip spec episodes**: If the rip spec contains resolved episode numbers
   (`Episode > 0`), use those plus neighboring episodes as candidates.
2. **Disc block estimate**: If no resolved episodes but a disc number is known,
   estimate the episode range from disc position and number of ripped placeholder assets.
   Those placeholder assets come from identification-time TV title selection, which keeps
   the dominant long-form runtime cluster, excludes likely extras, and may preserve a
   probable double-length title as a single unresolved placeholder asset. If a combined
   double-length playlist and split episode-length playlists represent the same content
   family, only the combined placeholder asset is preserved. For disc 1,
   a probable opening double-length title increases the block estimate by one represented
   episode so the matcher can fetch the extra reference episode needed for a later
   `SxxExx-Eyy` range decision.
3. **Full season fallback**: When neither is available, use all episodes in the
   season.

All references for the candidate set are fetched before matching begins.

### 6.2 Disc Block Size

`discBlockSize(discEpisodes, numRips)` returns the number of episodes on the
disc. If zero, defaults to `min(4, numRips)` to avoid searching for more
episodes than there are ripped titles.

---

## 7. Text Fingerprinting

### 7.1 Tokenization

```
Input text -> lowercase -> split on non-alphanumeric sequences -> filter tokens < 3 chars
```

Regex pattern: `[^a-z0-9]+`

### 7.2 Term-Frequency Vector

A `Fingerprint` stores:
- `tokens`: `map[string]float64` -- term frequency counts
- `norm`: L2 norm of the token vector (precomputed)

### 7.3 Cosine Similarity

```
similarity(A, B) = dot(A, B) / (norm(A) * norm(B))
```

Returns 0 if either fingerprint is nil or has zero norm.

---

## 8. IDF Weighting

### 8.1 Corpus Construction

After downloading reference subtitles, build an IDF corpus from all reference
fingerprints:

```go
corpus := NewCorpus()
for _, ref := range references {
    corpus.Add(ref.RawVector)
}
idf := corpus.IDF()
```

### 8.2 IDF Computation

```
IDF(term) = log((N + 1) / (1 + df(term)))
```

Where:
- `N` = total number of documents (reference subtitles)
- `df(term)` = number of documents containing the term

### 8.3 Weight Application

Each fingerprint's term frequencies are multiplied by their IDF weights.
The norm is recomputed. Terms absent from the IDF map retain their original
weight. Terms with zero weight after IDF are dropped.

### 8.4 Minimum Corpus Size

IDF weighting requires at least 2 references. With fewer, raw term-frequency
vectors are used directly.

---

## 9. Ordered Structured Decoding

### 9.1 Candidate Windows

The matcher evaluates plausible contiguous candidate windows from the season
rather than solving an unrestricted assignment problem across all references at
once.

Candidate windows are derived from:
- rip-spec hints when episode numbers already exist
- disc-number heuristics
- anchor selection from high-signal rip/reference similarities
- full-season fallback when narrower windows are not trustworthy

### 9.2 Decoder

Within each candidate window, the matcher runs an **ordered dynamic-programming
sequence decoder** over rip order and episode order.

The decoder scores paths using:
- **emission score**: transcript/reference cosine similarity normalized around
  `MinSimilarityScore`
- **reference skip penalty**: discourages gaps inside the chosen episode block
- **unresolved penalty**: allows leaving a rip unmatched when forcing a match
  would be less trustworthy

The production TV path uses **one matcher**: ordered structured decoding.
Spindle does **not** keep a permanent runtime fallback matcher for TV.

### 9.3 Window Selection and Path Margin

The best decoded path is selected across all candidate windows. The matcher also
records the next-best path score and stores a **path margin**:

- `path_margin = best_path_score - second_best_path_score`

Small path margins indicate ambiguous block-level decisions and feed both
confidence scoring and LLM verification.

---

## 10. Contiguity and Structural Validation

### 10.1 Purpose

TV disc episodes should still resolve to a contiguous season block (for example
E05-E10), but contiguity is now a **primary decoder property** rather than a
post-hoc repair step layered on unrestricted assignment.

### 10.2 Logic

After decoding, the stage validates the structured path:
1. Collect matched episode numbers.
2. Check whether they form a contiguous range.
3. Check structural diagnostics such as internal skipped episodes, unresolved
   holes, and disc-1 start violations.
4. If the ordered path is structurally suspicious, lower confidence and/or send
   the item to verification/review.

### 10.3 Disc 1 Constraint

When `disc1MustStartAtEpisode1` is true and the disc number is 1, the selected
ordered path must begin at episode 1. If it does not, the item is flagged for
review.

---

## 11. LLM Verification

### 11.1 Trigger Conditions

LLM verification runs when an LLM client is configured and at least one match
is ambiguous after structured decoding. Triggers include:
- derived `match_confidence < LLMVerifyThreshold` (default: 0.85)
- small `path_margin`
- small adjacent-episode `neighbor_margin`
- structural flags from the ordered path (for example internal gaps or disc-1
  violations)

### 11.2 Transcript Extraction

For each match requiring verification:
1. Extract the middle portion of dialogue from both the rip SRT and reference
   SRT files. Window half-size: `min(300.0, totalDuration/2)` seconds (clamped
   so short episodes don't extend past their boundaries; default
   `middleWindowHalfSec = 300.0` = 5 minutes each side for episodes >= 10 min).
2. Truncate each to `maxTranscriptChars = 6000` characters

### 11.3 LLM Prompt

See DESIGN_LLM_PROMPTS.md Section 3 for the exact system prompt, user prompt
template, and response schema. The prompt instructs the LLM to compare two
middle-portion transcripts and determine if they represent the same episode,
accounting for WhisperX recognition errors and localization differences.

### 11.4 Escalation Logic

| Condition | Action |
|-----------|--------|
| 0 ambiguous matches | Skip verification entirely |
| Verification confirms match | Keep assignment and raise confidence if warranted |
| Verification fails or rejects | Flag `NeedsReview` |

When the LLM rejects matches, the item is flagged for manual review rather than
attempting algorithmic reassignment. Ambiguity escalates to review instead of a
second production matcher.

---

## 12. Review Flags

### 12.1 NeedsReview Propagation

Review flags are written to individual `episodes[]` entries via
`episode.AppendReviewReason()`. Multiple reasons accumulate per episode. The
queue item also receives aggregate `needs_review` / `review_reason` state for
status reporting. For TV, the organizer uses the episode-level flags to split
clean episodes to the library and flagged episodes to the review directory.

### 12.2 Provenance Storage

Per-episode outcomes live in `episodes[]` and are the canonical source for
resolved episode numbers, confidence, and review status. Envelope attributes do
**not** duplicate per-episode matches. Instead, `attributes.content_id` stores a
compact run-level summary for auditability and tooling.

Expected `attributes.content_id` fields:
- `method`
- `reference_source`
- `reference_episodes`
- `transcribed_episodes`
- `matched_episodes`
- `unresolved_episodes`
- `low_confidence_count`
- `review_threshold`
- `sequence_contiguous`
- `episodes_synchronized`
- `completed`

### 12.3 Review Sources

| Source | Condition |
|--------|-----------|
| Structured decoder | Disc 1 path doesn't start at episode 1 |
| Structured decoder | Matched episodes are non-contiguous or contain internal gaps |
| Confidence model | Derived confidence falls below review threshold |
| LLM verification | Verification call failed |
| LLM verification | Any match rejected by LLM |

### 12.4 Low-Confidence Review

Matches below `LowConfidenceReviewThreshold` (default: 0.70) are flagged during
matching (handled by the episode identification stage, not the matcher directly).

---

## 13. Policy Constants

Key policy values live in the `Policy` struct.

| Constant | Value | Description |
|----------|-------|-------------|
| `minSimilarityScore` | 0.58 | Baseline similarity used by the ordered decoder emission score. |
| `lowConfidenceReviewThreshold` | 0.70 | Derived confidence below this triggers a review flag. |
| `llmVerifyThreshold` | 0.85 | Derived confidence below this triggers LLM verification. |
| `referenceSkipPenalty` | 0.12 | Penalty for skipping reference episodes inside a candidate window. |
| `unresolvedPenalty` | 0.08 | Penalty for leaving a rip unresolved. |
| `scoreMarginTarget` | 0.05 | Desired separation from the forward runner-up. |
| `reverseMarginTarget` | 0.05 | Desired separation from the reverse runner-up. |
| `neighborMarginTarget` | 0.03 | Desired separation from adjacent episodes. |
| `pathMarginTarget` | 0.12 | Desired separation from the next-best decoded path. |
| `disc1MustStartAtEpisode1` | true | Disc 1 ordered path must start at episode 1. |

---

## 14. Results Written to Envelope

After successful matching, the episode ID stage updates the envelope:

1. **Episode resolution**: Each `episodes[]` entry is updated with resolved
   `episode` number, optional `episode_end` for range assets, `episode_title`,
   and `episode_air_date`. The stage stores both raw `match_score` and derived
   `match_confidence`. The current implementation supports a conservative
   opening-double inference for disc 1: when the first placeholder title has a
   probable double-length runtime profile and the resolved single-episode
   matches form an opening contiguous run, the first entry is promoted to a
   range key like `s01e01-e02` and later entries are shifted accordingly.
2. **Episode review flags**: Low-confidence or unresolved matches mark the
   specific `episodes[]` entry with `needs_review=true` and a `review_reason`.
   Queue-level `needs_review` is also set as an aggregate signal when any
   episode is flagged.
3. **Envelope provenance summary**: Run-level provenance is persisted in
   `attributes.content_id`, including the matching method, reference source,
   reference/transcript counts, low-confidence count, contiguity result, and
   whether the envelope episodes were synchronized from the run.
4. **Logging**: Per-episode match details (raw score, derived confidence,
   runner-up margins, neighbor ambiguity, path margin, subtitle file IDs,
   and method) are logged at INFO level for diagnostics.

Organizer behavior for TV consumes these episode-level flags directly:
clean resolved episodes go to the library, while unresolved or flagged episodes
are routed to review.

Queue item metadata is also updated with `episode_numbers`, `season_number`,
and `media_type` fields.
