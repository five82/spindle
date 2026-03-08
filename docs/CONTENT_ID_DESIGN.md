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
5. Verifying low-confidence or non-contiguous matches with an LLM

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
[5. Hungarian Assignment] -- Optimal one-to-one matching
        |
        v
[6. Score Filter] -- Discard matches below minimum threshold
        |
        v
[7. Contiguity Check] -- Flag outlier matches for LLM verification
        |
        v
[8. LLM Verification] -- Verify low-confidence and non-contiguous matches
        |
        v
[9. Apply Matches] -- Update envelope episodes with confirmed mappings
```

---

## 4. Transcription

### 4.1 WhisperX Execution

For each episode in the rip spec envelope that has a ripped asset:

1. Create working directory: `<staging_root>/contentid/<episode_key>/`
2. Invoke the shared transcription service (see DESIGN_INFRASTRUCTURE.md
   Section 9) with the ripped file path. The `whisperxSem` semaphore is
   held by the episode identification stage for the duration.
3. Read the generated SRT file and normalize it (strip SRT formatting, clean text)
4. Create a text fingerprint from the normalized plain text

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

- Minimum 3-second interval between OpenSubtitles API calls (consistent with
  the rate limit enforced by the OpenSubtitles client; see API_SERVICES.md
  Section 3).
- Retriable errors (rate limits) use exponential backoff with `MaxRateRetries`
- Backoff: `InitialBackoff * 2^(attempt-1)`, capped at `MaxBackoff`

---

## 6. Candidate Episode Set

### 6.1 Single Candidate Set

Rather than evaluating multiple strategies, the matcher builds a single candidate
episode set using the best available information:

1. **Rip spec episodes**: If the rip spec contains resolved episode numbers
   (`Episode > 0`), use those plus neighboring episodes as candidates.
2. **Disc block estimate**: If no resolved episodes but a disc number is known,
   estimate the episode range from disc position and number of rips.
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

## 9. Global Assignment (Hungarian Algorithm)

### 9.1 Cost Matrix

Build an `N x M` matrix (padded to square) where:
- `cost[i][j] = 1 - cosineSimilarity(rip[i], ref[j])`
- Padded cells use cost = 2.0 (ensures they are not preferred)

### 9.2 Algorithm

Standard Hungarian algorithm (O(n^3)) for minimum-cost assignment on the square
cost matrix. Returns `assignment[i] = j` for each row `i`.

**Implementation**: Direct implementation in Go (~100 lines). Matrix sizes are
small (typically < 30 episodes per season), so performance is not a concern.
No external dependency needed.

### 9.3 Minimum Score Filter

After assignment, discard matches where `score < MinSimilarityScore` (default:
0.58). These are unconfident enough that no assignment is better than a wrong one.

---

## 10. Contiguity Check

### 10.1 Purpose

TV disc episodes should map to a contiguous range (e.g., E05-E10). After
Hungarian assignment, check whether the matched episodes form a contiguous
sequence.

### 10.2 Logic

1. Collect all matched episode numbers and sort them.
2. Check if they form a contiguous range (each consecutive pair differs by 1).
3. If contiguous: done, no further action.
4. If not contiguous: flag the outlier matches (episodes outside the longest
   contiguous subsequence) for LLM verification.

### 10.3 Disc 1 Constraint

When `disc_1_must_start_at_episode_1 = true` (default) and the disc number is 1,
the contiguous range must start at episode 1. If it doesn't, flag `NeedsReview`.

---

## 11. LLM Verification

### 11.1 Trigger Conditions

LLM verification runs when an LLM client is configured and either:
- At least one match has `Score < LLMVerifyThreshold` (default: 0.85)
- At least one match was flagged by the contiguity check

### 11.2 Transcript Extraction

For each match requiring verification:
1. Extract the middle portion of dialogue from both the rip SRT and reference
   SRT files. Window half-size: `min(300.0, totalDuration/2)` seconds (clamped
   so short episodes don't extend past their boundaries; default
   `middleWindowHalfSec = 300.0` = 5 minutes each side for episodes >= 10 min).
2. Truncate each to `maxTranscriptChars = 6000` characters

### 11.3 LLM Prompt

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

### 11.4 Escalation Logic

| Condition | Action |
|-----------|--------|
| 0 matches below threshold | Skip verification entirely |
| 0 rejections | All verified -- no changes |
| Any rejections | Flag `NeedsReview` |

When the LLM rejects matches, the item is flagged for manual review rather than
attempting algorithmic reassignment. LLM rejections indicate genuine ambiguity
that automated recovery is unlikely to resolve correctly.

---

## 12. Review Flags

### 12.1 NeedsReview Propagation

Review flags are appended to the rip spec envelope via `env.AppendReviewReason()`.
Multiple reasons accumulate. The organizer stage routes items with review flags
to the review directory instead of the main library.

### 12.2 Review Sources

| Source | Condition |
|--------|-----------|
| Contiguity check | Disc 1 range doesn't start at episode 1 |
| Contiguity check | Matched episodes are non-contiguous |
| LLM verification | Verification call failed (kept original match) |
| LLM verification | Any match rejected by LLM |

### 12.3 Low-Confidence Review

Matches below `LowConfidenceReviewThreshold` (default: 0.70) are flagged during
matching (handled by the episode identification stage, not the matcher directly).

---

## 13. Policy Configuration Reference

All thresholds are in the `Policy` struct, configurable via the `content_id`
config section.

| Field | Type | Default | Description |
|-------|------|---------|-------------|
| `min_similarity_score` | float64 | 0.58 | Minimum cosine similarity for a valid match. Matches below this are discarded. |
| `low_confidence_review_threshold` | float64 | 0.70 | Matches below this trigger a review flag. |
| `llm_verify_threshold` | float64 | 0.85 | Matches below this are sent to LLM verification. |
| `disc_1_must_start_at_episode_1` | bool | true | Disc 1 contiguous range must start at episode 1. |

### 13.1 Normalization

`Policy.normalized()` validates all fields and replaces out-of-range values with
defaults:
- Float fields must be in `(0, 1)` (exclusive)
- Boolean fields have no validation

---

## 14. Attributes Written to Envelope

After successful matching, the following attributes are set on the rip spec
envelope:

| Attribute | Type | Description |
|-----------|------|-------------|
| `ContentIDMatches` | `[]ContentIDMatch` | Per-episode match details (key, title_id, episode, score, subtitle info) |
| `ContentIDMethod` | `string` | Always `"whisperx_opensubtitles"` |
| `ContentIDTranscripts` | `map[string]string` | Episode key -> WhisperX SRT path |
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
