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
4. Finding the optimal assignment using the Hungarian algorithm
5. Optionally verifying low-confidence matches with an LLM

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
[1. Transcribe] -- WhisperX via uvx generates SRT for each ripped title
        |
        v
[2. Fetch References] -- Download OpenSubtitles SRTs for candidate episodes
        |                  (with caching and search variant fallback)
        v
[3. Fingerprint] -- Convert text to TF term-frequency vectors
        |
        v
[4. IDF Weighting] -- Apply TF-IDF across reference corpus to downweight
        |              common show vocabulary (character names, catchphrases)
        v
[5. Anchor Selection] -- Find confident rip->episode mapping using first/second
        |                  rip titles to narrow the episode search window
        v
[6. Build Strategies] -- Generate 4 ordered candidate strategies
        |
        v
[7. Evaluate Strategies] -- For each strategy: compute similarity, run
        |                     Hungarian matching, apply block refinement
        v
[8. Select Best] -- Pick strategy by: coverage > avg_score > needs_review
        |
        v
[9. LLM Verification] -- Optional: verify low-confidence matches via LLM
        |
        v
[10. Apply Matches] -- Update envelope episodes with confirmed mappings
```

---

## 4. Transcription

### 4.1 WhisperX Execution

For each episode in the rip spec envelope that has a ripped asset:

1. Create working directory: `<staging_root>/contentid/<episode_key>/`
2. Build a `subtitles.GenerateRequest` with the ripped file path
3. Execute WhisperX via the subtitle generator service
4. Read the generated SRT file and normalize it (strip SRT formatting, clean text)
5. Create a text fingerprint from the normalized plain text

### 4.2 Progress Reporting

Phase: `transcribe`
Reports: `(current_episode, total_episodes, episode_key)` after each transcript.

### 4.3 Caching

Transcripts are stored in the staging directory under `contentid/<episode_key>/`.
The staging directory is tied to the queue item's fingerprint and persists across
retries of the same disc.

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
- File cache: keyed by `file_id`, stored under `opensubtitles_cache_dir`
- Search cache: keyed by variant signature, stores search responses
- Cache hits skip network calls entirely

### 5.4 Rate Limiting

- Minimum 1-second interval between OpenSubtitles API calls (`MinInterval`)
- Retriable errors (rate limits) use exponential backoff with `MaxRateRetries`
- Backoff: `InitialBackoff * 2^(attempt-1)`, capped at `MaxBackoff`

### 5.5 Progressive Fetching (Episode Passes)

Rather than fetching all season references upfront, the matcher uses progressive
passes to minimize API calls:

1. `buildEpisodePasses()` creates ordered batches of episode numbers
2. First pass covers the most likely candidates (from disc block estimate or
   rip spec episodes)
3. After each pass, attempt anchor selection
4. If an anchor is found, stop fetching -- the anchor narrows the search window
5. Additional passes expand outward from the initial batch

---

## 6. Text Fingerprinting

### 6.1 Tokenization

```
Input text -> lowercase -> split on non-alphanumeric sequences -> filter tokens < 3 chars
```

Regex pattern: `[^a-z0-9]+`

### 6.2 Term-Frequency Vector

A `Fingerprint` stores:
- `tokens`: `map[string]float64` -- term frequency counts
- `norm`: L2 norm of the token vector (precomputed)

### 6.3 Cosine Similarity

```
similarity(A, B) = dot(A, B) / (norm(A) * norm(B))
```

Returns 0 if either fingerprint is nil or has zero norm.

---

## 7. IDF Weighting

### 7.1 Corpus Construction

After downloading reference subtitles, build an IDF corpus from all reference
fingerprints:

```go
corpus := NewCorpus()
for _, ref := range references {
    corpus.Add(ref.RawVector)
}
idf := corpus.IDF()
```

### 7.2 IDF Computation

```
IDF(term) = log((N + 1) / (1 + df(term)))
```

Where:
- `N` = total number of documents (reference subtitles)
- `df(term)` = number of documents containing the term

### 7.3 Weight Application

Each fingerprint's term frequencies are multiplied by their IDF weights.
The norm is recomputed. Terms absent from the IDF map retain their original
weight. Terms with zero weight after IDF are dropped.

### 7.4 Minimum Corpus Size

IDF weighting requires at least 2 references. With fewer, raw term-frequency
vectors are used directly.

### 7.5 RawVector Preservation

All fingerprints store both `Vector` (current, possibly IDF-weighted) and
`RawVector` (original term-frequency only). This allows recomputing IDF when
the reference set changes across strategy evaluations.

---

## 8. Matching Strategies

### 8.1 Strategy Construction

`buildStrategyAttempts()` creates up to 4 strategies in priority order:

| # | Strategy        | Source                  | Reason                |
|---|-----------------|-------------------------|-----------------------|
| 1 | `ripspec_seed`  | Rip spec episode numbers| `derived_from_ripspec` or `disc_block_estimate` |
| 2 | `anchor_window` | Anchor selection        | `first_anchor` or `second_anchor` |
| 3 | `disc_block`    | Disc number estimate    | `disc_number_window`  |
| 4 | `full_season`   | All season episodes     | `season_fallback`     |

### 8.2 Deduplication

Strategies with identical episode lists (after sorting and compacting) are
deduplicated. The first occurrence wins.

### 8.3 Strategy Evaluation

For each strategy:

1. Filter existing references to the strategy's episode set
2. Fetch missing references for episodes not yet downloaded
3. Apply IDF weighting across all references
4. Run `resolveEpisodeMatches()` (Hungarian algorithm)
5. Apply `refineMatchBlock()` (contiguous block constraint)
6. Compute average match score

### 8.4 Strategy Selection

`betterOutcome()` compares strategies in order:

1. **Match count**: More matches wins
2. **Average score**: Higher average similarity wins
3. **Needs review**: Strategy not needing review wins

---

## 9. Anchor Selection

### 9.1 Purpose

The anchor identifies a high-confidence mapping between a single rip title and
a reference episode. This narrows the episode search window, reducing the number
of references that need downloading.

### 9.2 Anchor Evaluation

Tries the first rip title, then the second:

1. Compute cosine similarity between the rip fingerprint and all available
   reference fingerprints
2. Track the best score, second-best score, and best-matching episode number
3. Check thresholds:
   - `BestScore >= AnchorMinScore` (default: 0.63)
   - `ScoreMargin >= AnchorMinScoreMargin` (default: 0.03)
     where `ScoreMargin = BestScore - SecondBestScore`

### 9.3 Window Derivation

When an anchor is found for rip index `idx` matching episode `E`:

```
windowStart = E - idx
windowEnd = windowStart + numRips - 1
```

Clamped to `[1, totalSeasonEpisodes]`.

### 9.4 Failure Reasons

- `anchor_inputs_unavailable`: No rips or refs
- `anchor_vector_missing`: Rip fingerprint is nil
- `anchor_no_scored_references`: No valid reference scores
- `anchor_score_below_threshold`: Best score < `AnchorMinScore`
- `anchor_score_ambiguous`: Margin < `AnchorMinScoreMargin`
- `anchor_not_selected`: Neither first nor second anchor succeeded

---

## 10. Candidate Episode Planning

### 10.1 Tiered Episode Discovery

`deriveCandidateEpisodes()` builds the initial episode candidate set:

**Tier 1 -- Rip Spec Episodes**: Collect resolved episode numbers from the rip
spec (episodes with `Episode > 0`).

**Tier 2 -- Disc Block Neighbors** (when Tier 1 found episodes and disc number
is known): Compute disc block size (= number of rip episodes) and add episodes
from `(discNumber-1)*blockSize` through `discNumber*blockSize`.

**Tier 2b -- Disc Block Estimate** (when Tier 1 found no episodes but disc number
is known): Estimate starting position with padding:
- `padding = max(DiscBlockPaddingMin, blockSize / DiscBlockPaddingDivisor)`
- Range: `(discNumber-1)*block - padding` through `discNumber*block + padding`

**Tier 3 -- Season Fallback**: When no episodes are resolved, use all season
episode numbers.

### 10.2 Disc Block Size

`discBlockSize(discEpisodes)` returns the number of episodes on the disc. If
zero, defaults to 4.

---

## 11. Block Refinement

### 11.1 Purpose

TV disc episodes should map to a contiguous range (e.g., E05-E10). When
high-confidence matches establish a block, outliers (matches outside the block)
are reassigned to gap positions within the block.

### 11.2 High-Confidence Determination

Two thresholds compute a "high-confidence" cutoff:

1. **Delta threshold**: `maxScore - BlockHighConfidenceDelta` (default: 0.05)
2. **Top ratio threshold**: Score at the `1 - BlockHighConfidenceTopRatio`
   percentile (default: top 70%)

The higher (more selective) threshold is used.

Requires at least 2 high-confidence matches to establish a block.

### 11.3 Block Positioning

The block contains `numEpisodes` (= number of rip titles) contiguous episodes
and must include all high-confidence matches.

**Disc 1**: `blockStart = 1` when `Disc1MustStartAtEpisode1 = true` (default).
If episode 1 falls outside the valid range of high-confidence matches, flag
`NeedsReview`.

**Disc 2+**: Default to expanding upward (`blockStart = validHigh`). Check
displaced matches for directional hints: if a displaced match points below
the high-confidence range, expand downward instead. Hard constraint:
`blockStart >= Disc2PlusMinStartEpisode` (default: 2).

**Disc unknown (0)**: Use `hcMin` (lowest high-confidence episode).

### 11.4 Gap Reassignment

1. Partition matches into valid (in block) and displaced (outside block)
2. Find gap episodes (block positions with no valid match)
3. If gap count matches displaced count and reference fingerprints exist for
   gap episodes: use Hungarian matching on displaced x gap references
4. Otherwise: assign by position order with score = 0
5. If gaps exist but displaced != gaps, flag `NeedsReview`
6. If no gaps but displaced exist, flag `NeedsReview` (unusual case)

---

## 12. Global Assignment (Hungarian Algorithm)

### 12.1 Cost Matrix

Build an `N x M` matrix (padded to square) where:
- `cost[i][j] = 1 - cosineSimilarity(rip[i], ref[j])`
- Padded cells use cost = 2.0 (ensures they are not preferred)

### 12.2 Algorithm

Standard Hungarian algorithm for minimum-cost assignment on the square cost
matrix. Returns `assignment[i] = j` for each row `i`.

### 12.3 Minimum Score Filter

After assignment, discard matches where `score < MinSimilarityScore` (default:
0.58). These are unconfident enough that no assignment is better than a wrong one.

---

## 13. LLM Verification

### 13.1 Trigger Condition

LLM verification runs when:
- An LLM client is configured (`llm` section in config)
- At least one match has `Score < LLMVerifyThreshold` (default: 0.85)

### 13.2 Transcript Extraction

For each low-confidence match:
1. Extract the middle 10 minutes of dialogue from both the rip SRT and reference
   SRT files (`middleWindowHalfSec = 300.0` = 5 minutes each side)
2. Truncate each to `maxTranscriptChars = 6000` characters

### 13.3 LLM Prompt

System prompt asks the LLM to compare two transcripts and determine if they
represent the same episode. Response format:
```json
{"same_episode": true/false, "confidence": 0.0-1.0, "explanation": "brief reason"}
```

The prompt explicitly tells the LLM to:
- Focus on whether the same scenes and dialogue events occur
- Not penalize minor word differences, transcription errors, or timing differences
- Account for WhisperX speech recognition errors
- Account for localization differences in reference subtitles

### 13.4 Escalation Logic

| Rejections | Action |
|------------|--------|
| 0 below threshold | Skip verification entirely |
| 0 rejections | All verified -- no changes |
| 1 rejection | `NeedsReview` (single disagreement not enough to rematch) |
| 2+ rejections | Cross-match rejected episodes against rejected references |
| All rejected in cross-match | `NeedsReview` |

### 13.5 Cross-Matching (Rematch)

When 2+ matches are rejected:

1. Collect all rejected episode keys and their target episodes
2. Pre-extract middle transcripts from all involved SRT files
3. Run N x M LLM comparisons: each rejected rip against each rejected reference
4. Build a confidence matrix from accepted (same_episode=true) comparisons
5. Run Hungarian algorithm on the confidence matrix to find optimal reassignment
6. Apply reassigned matches

---

## 14. Review Flags

### 14.1 NeedsReview Propagation

Review flags are appended to the rip spec envelope via `env.AppendReviewReason()`.
Multiple reasons accumulate. The organizer stage routes items with review flags
to the review directory instead of the main library.

### 14.2 Review Sources

| Source | Condition |
|--------|-----------|
| Block refinement | Disc 1 anchor outside valid high-confidence range |
| Block refinement | Displaced matches with no gaps in block |
| Block refinement | Displaced count does not match gap count |
| LLM verification | Verification call failed (kept original match) |
| LLM verification | Single rejection (not enough to rematch) |
| LLM verification | All cross-match combinations rejected |

### 14.3 Low-Confidence Review

Matches below `LowConfidenceReviewThreshold` (default: 0.70) are flagged during
strategy evaluation (handled by the episode identification stage, not the matcher
directly).

---

## 15. Policy Configuration Reference

All thresholds are in the `Policy` struct, configurable via the `content_id`
config section.

| Field | Type | Default | Description |
|-------|------|---------|-------------|
| `min_similarity_score` | float64 | 0.58 | Minimum cosine similarity for a valid match. Matches below this are discarded. |
| `low_confidence_review_threshold` | float64 | 0.70 | Matches below this trigger a review flag. |
| `llm_verify_threshold` | float64 | 0.85 | Matches below this are sent to LLM verification. |
| `anchor_min_score` | float64 | 0.63 | Minimum score for an anchor rip-to-reference match. |
| `anchor_min_score_margin` | float64 | 0.03 | Minimum margin between best and second-best anchor scores. |
| `block_high_confidence_delta` | float64 | 0.05 | Score within this delta of the maximum is "high confidence". |
| `block_high_confidence_top_ratio` | float64 | 0.70 | Top N% of matches count as "high confidence". |
| `disc_block_padding_min` | int | 2 | Minimum episode padding when estimating disc blocks. |
| `disc_block_padding_divisor` | int | 4 | Divisor for computing padding: `blockSize / divisor`. |
| `disc_1_must_start_at_episode_1` | bool | true | Disc 1 block always starts at episode 1. |
| `disc_2_plus_min_start_episode` | int | 2 | Minimum start episode for disc 2+. |

### 15.1 Normalization

`Policy.normalized()` validates all fields and replaces out-of-range values with
defaults:
- Float fields must be in `(0, 1)` (exclusive)
- `BlockHighConfidenceTopRatio` must be in `(0, 1]` (inclusive upper)
- Integer fields must be `> 0`
- Boolean fields have no validation

---

## 16. Attributes Written to Envelope

After successful matching, the following attributes are set on the rip spec
envelope:

| Attribute | Type | Description |
|-----------|------|-------------|
| `ContentIDMatches` | `[]ContentIDMatch` | Per-episode match details (key, title_id, episode, score, subtitle info) |
| `ContentIDMethod` | `string` | Always `"whisperx_opensubtitles"` |
| `ContentIDTranscripts` | `map[string]string` | Episode key -> WhisperX SRT path |
| `ContentIDSelectedStrategy` | `string` | Name of the winning strategy |
| `ContentIDStrategyScores` | `[]StrategyScore` | All evaluated strategies with scores |
| `EpisodesSynchronized` | `bool` | Set to `true` after successful matching |

Each `ContentIDMatch` contains:
- `EpisodeKey`: Rip spec episode key (e.g., `s01_001`)
- `TitleID`: MakeMKV title number
- `MatchedEpisode`: Target episode number
- `Score`: Cosine similarity score
- `SubtitleFileID`: OpenSubtitles file ID used
- `SubtitleLanguage`: Language of the matched reference
- `SubtitleCachePath`: Local cache path of the reference SRT

Queue item metadata is also updated with `episode_numbers`, `season_number`,
and `media_type` fields.
