# Content ID Design

Episode identification subsystem for mapping ripped TV titles to canonical TMDB
season/episode numbers using transcript similarity against OpenSubtitles
references.

---

## 1. Purpose

Physical Blu-ray and DVD TV discs store episodes as generic titles such as
`Title00.mkv` with no canonical episode metadata. Disc title order often differs
from broadcast order, and subtitle releases may use alternate numbering schemes
for split pilots or other anomalies.

Content ID exists to answer one question reliably:

**Which canonical TMDB episode does each ripped title contain?**

The design is intentionally **content-first**:

1. Transcribe each ripped title with WhisperX.
2. Build an initial plausible TMDB episode set and expand once only if coverage
   is weak.
3. Fetch OpenSubtitles references for those canonical episodes.
4. Compare transcript content against reference subtitle content.
5. Resolve most episodes from transcript similarity alone.
6. Use the LLM only for ambiguous transcript-vs-transcript pair checks.
7. Use season-set reconciliation and weak ordering hints only after content
   matching, never as the primary solver.

This stage updates the rip spec envelope in place so downstream encoding,
subtitling, and organizing have canonical episode metadata.

---

## 2. Design Principles

### 2.1 Content is primary

TV episode identification is primarily a transcript similarity problem.
Ordering, disc number, and season structure are supporting evidence only.

### 2.2 Canonical numbering comes from TMDB

TMDB season/episode numbers are the canonical output numbering for Spindle.
OpenSubtitles metadata and release names are advisory only.

### 2.3 Reference numbering anomalies are expected

OpenSubtitles release names may use alternate numbering, especially for split
pilots. For example, a subtitle release named `S01E08 Justice` can still be a
valid reference for canonical TMDB `S01E07 Justice` if the title and transcript
content match.

### 2.4 The LLM has a narrow job

The LLM compares two transcripts and answers:

- are these the same episode?
- how confident are you?

It is not a hidden global solver, not a cross-matcher, and not a substitute for
canonical numbering rules.

### 2.5 Review is preferred over clever fallback stacks

There is one production TV matching pipeline. If transcript evidence remains
ambiguous after content matching, reference validation, LLM verification, and
set reconciliation, the item is sent to review.

### 2.6 Deterministic confidence comes from deterministic signals

`match_confidence` is derived from deterministic matcher evidence.
LLM output may gate acceptance of an ambiguous pair, but LLM confidence does not
numerically inflate stored `match_confidence` in the initial rewrite.

---

## 3. Preconditions

Content ID requires all of the following:

- `media_type = tv`
- OpenSubtitles enabled and configured
- a TMDB ID on the queue item
- at least one ripped TV title in the envelope
- WhisperX available
- TMDB season metadata available

If prerequisites are missing, the stage returns early without episode
resolution.

---

## 4. Pipeline Overview

```
Ripped Episode Files
        |
        v
[1. Transcribe] -- WhisperX on selected primary audio
        |
        v
[2. Build Initial Candidate Episode Set] -- derive likely TMDB episode numbers
        |
        v
[3. Fetch + Validate References] -- OpenSubtitles search and metadata scoring
        |
        v
[4. Expand Reference Scope Once If Needed] -- broader set only when coverage is weak
        |
        v
[5. Fingerprint + Similarity] -- TF-IDF cosine similarity between rip and refs
        |
        v
[6. Rank Claims + Accept Clear Matches] -- strongest non-conflicting content claims
        |
        v
[7. LLM Verification] -- same-episode check for a few ambiguous pairs only
        |
        v
[8. Set Reconciliation] -- fill the final obvious hole only under strict guardrails
        |
        v
[9. Weak Structural Checks] -- review-only sanity checks, not primary solver
        |
        v
[10. Apply Matches] -- update envelope episodes and review flags
```

---

## 5. Transcription

### 5.1 Audio selection

Each ripped title is probed and transcribed from the selected primary audio
stream, not blindly from `0:a:0`.

### 5.2 WhisperX output

For each ripped episode asset:

1. Create `<staging_root>/contentid/<episode_key>/`
2. Run the shared transcription service
3. Read the generated SRT
4. Normalize plain text for fingerprinting
5. Store the transcript path and fingerprint

### 5.3 Cache identity

The transcription cache key must include:

- disc fingerprint
- episode key
- selected audio-relative stream index

This prevents collisions when the same file is transcribed from different audio
tracks.

---

## 6. Candidate Episode Set and Reference Scope

The matcher builds a **plausible season candidate set** before reference fetch.
This is not the final solver; it only bounds reference acquisition.

### 6.1 Initial candidate set

Initial candidate episodes come from the best available signals, in order:

1. already resolved episode numbers in the envelope
2. disc number and ripped title count
3. nearby season windows around strong transcript anchors
4. full-season fallback when no narrower set is trustworthy

The candidate set should be broad enough to tolerate disc-order anomalies.
It must not assume disc title order equals broadcast order.

### 6.2 Progressive scope expansion

The runtime matcher uses one progressive fetch flow, not multiple competing
runtime strategies.

Behavior:

1. build an initial likely candidate set
2. fetch and validate references for that set
3. if confident coverage is weak, expand once to a broader set or full season
4. stop and match against the final fetched set

Weak coverage may include:

- too few usable references
- too few clear content claims
- obviously suspect reference quality

The matcher must not score several strategy outcomes and choose among them as a
hidden second-layer solver.

---

## 7. OpenSubtitles Reference Acquisition

### 7.1 Search

For each candidate TMDB episode number, search OpenSubtitles with:

- parent TMDB ID
- season number
- episode number
- configured languages
- show title query text

### 7.2 Candidate selection goals

OpenSubtitles search results are noisy. The selector must prefer references that
best represent the **target episode content**, not merely the most downloaded
result.

### 7.3 Reference validation rules

Each subtitle candidate is scored using metadata from:

- release name
- file name
- hearing-impaired flag
- download count
- TMDB episode title for the target episode
- TMDB episode titles for nearby episodes in the same season

The selector should:

1. reward exact target episode title matches
2. reward exact season/episode markers when present
3. prefer non-HI over HI when other evidence is comparable
4. use download count as a supporting signal, not the sole selector
5. heavily penalize candidates whose release or file name explicitly names a
   different episode title from the same season
6. penalize broad multi-episode packs when a specific single-episode release is
   available

### 7.4 Alternate numbering tolerance

Split-pilot and similar numbering anomalies are expected.
A candidate that names the correct episode title but uses an alternate numeric
label may still be valid.

Example:

- canonical TMDB target: `S01E07 Justice`
- OpenSubtitles release name: `S01E08 Justice`

This is acceptable evidence if the title and transcript content agree.
By contrast, a candidate named `S01E07 Lonely Among Us` is not acceptable for
Justice even though the numeric tag appears closer.

### 7.5 Weak-reference handling

The selector may accept at most one working reference per canonical episode, but
that reference must be treated as **trusted** or **suspect**.

If the best candidate is still weak or suspicious, the stage must either:

1. fall back to the next-best candidate for that same canonical episode, or
2. mark the episode reference as suspect and prevent it from producing a
   high-confidence clear match on its own

A suspicious best candidate must not silently become a strong reference merely
because it outranked worse garbage.

### 7.6 Reference quality is observable

Reference selection decisions must be logged at INFO level with enough detail to
see:

- why a candidate was chosen
- whether alternate numbering was tolerated
- whether conflicting titles were rejected
- whether the chosen reference remained suspect

---

## 8. Text Fingerprinting

### 8.1 Tokenization

Input text is:

- lowercased
- split on non-alphanumeric boundaries
- stripped of very short tokens

### 8.2 Fingerprints

A fingerprint stores term frequencies and a precomputed L2 norm.

### 8.3 Similarity

Similarity is cosine similarity between rip and reference fingerprints.

### 8.4 IDF weighting

IDF weighting is computed from the fetched reference set and applied to both rip
and reference vectors. With fewer than two references, raw term-frequency
vectors are used.

---

## 9. Content-First Matching

### 9.1 Claim matrix

For each ripped title and each fetched reference episode, compute:

- raw cosine similarity (`match_score`)
- rip-side runner-up margin
- episode-side runner-up margin
- nearby-episode ambiguity margin
- reference-quality penalties

These values describe how strongly the content supports a match.

### 9.2 Claim ranking

All rip/reference pairs above `minSimilarityScore` become provisional claims.
Each claim gets a deterministic strength derived from score, margins, and
quality penalties.

The matcher then:

1. sorts claims strongest-first
2. greedily accepts non-conflicting claims that satisfy clear-match rules
3. leaves conflicting or weak claims unresolved for later verification

This provides one-to-one uniqueness without a global ordered path decoder.

### 9.3 Clear-match acceptance

A rip/reference pair may be accepted directly when all of the following hold:

1. the raw score is above `minSimilarityScore`
2. the rip-side margin is strong enough to separate the claim from that rip's
   runner-up candidates
3. the episode-side margin is strong enough to separate the claim from other
   rips competing for the same canonical episode
4. nearby episodes are not close competitors
5. the chosen reference is not marked too suspect to trust directly
6. no stronger accepted claim already owns that canonical episode

### 9.4 Ambiguous pairs

Pairs that do not meet clear-match criteria remain unresolved and move to the
verification stage. Ambiguous pairs are not forced into a global path merely to
complete a sequence.

### 9.5 One-to-one uniqueness

Canonical episode numbers are unique per item. If multiple rips compete for the
same reference episode, the strongest clear claim wins. Other competitors remain
ambiguous or unresolved unless later confirmed by narrow pairwise verification.

### 9.6 Ordering is not the primary solver

The runtime matcher must not require all accepted matches to form a monotone
forward or reverse path. Disc order may be arbitrary.

---

## 10. LLM Verification

### 10.1 Trigger conditions

LLM verification runs only for ambiguous pairs when an LLM client is
configured.

Typical triggers:

- low derived confidence on a provisional claim
- weak separation from runner-up candidates
- duplicate competition for the same canonical episode
- a final unresolved tail where only a small number of episode candidates
  remain plausible

### 10.2 Verification breadth

The LLM is a narrow ambiguity resolver.
For each unresolved or contested rip, the stage should verify only the top one
or two plausible episode pairs.

The runtime matcher must not perform broad cross-product LLM verification across
an entire season.

### 10.3 Prompt goal

The prompt compares two transcripts and decides whether they are the same
episode.

The LLM is not asked to:

- infer global season order
- repair the whole assignment set
- renumber the season
- choose among many episodes at once

### 10.4 Inputs

For each challenged pair, extract the middle portion of:

- the WhisperX transcript from the rip
- the OpenSubtitles reference transcript

### 10.5 Output

The LLM returns:

- `same_episode: true|false`
- `explanation`

### 10.6 Acceptance semantics

A positive LLM result allows an already-proposed ambiguous pair to be accepted
as describing the same episode content.
It does **not** replace canonical episode-numbering rules, and it does **not**
numerically raise stored `match_confidence` in the initial rewrite.

The LLM is treated as a narrow boolean verification gate for ambiguous pairs.
Stored `match_confidence` remains fully deterministic.

### 10.7 Failure behavior

If the LLM fails, times out, or rejects the pair, that pair remains unresolved
and the item is flagged for review rather than invoking a second hidden
matcher.

---

## 11. Set Reconciliation

After clear matches and LLM-verified matches are accepted, the stage performs a
small reconciliation pass.

### 11.1 Missing-episode completion

Single-hole completion is allowed only when all of the following are true:

1. exactly one ripped title remains unresolved
2. exactly one canonical episode remains unassigned in the relevant fetched
   season subset
3. accepted matches already define `N-1` members of a contiguous `N`-episode
   subset, or otherwise leave one obvious canonical hole in the fetched scope
4. the unresolved rip is not strongly contradicted by another content claim
5. the accepted set is already trustworthy enough that the remainder is the
   obvious completion, not a guess

### 11.2 Purpose

Set reconciliation uses the fact that a TV disc often contains a finite season
subset, for example:

- confidently resolved: `4, 5, 6, 8`
- one rip unresolved
- one canonical episode missing: `7`

In that case the remaining rip may be assigned to `7`.

### 11.3 Limits

Set reconciliation is a completion step, not a substitute for transcript
matching. If more than one rip or more than one episode remains unresolved, or
if the unresolved rip still has strong contradictory content evidence, the item
stays ambiguous and may require review.

---

## 12. Weak Structural Checks

Ordering and contiguity remain useful as **sanity checks**, not as the primary
matching engine.

Structural checks may flag review when:

- disc 1 appears not to start at episode 1
- accepted matches imply an implausible or highly fragmented season subset
- accepted set conflicts with the declared disc number in a strong way

These checks must not override strong content matches on their own.

---

## 13. Confidence Model

### 13.1 Two separate numbers

Each accepted match stores:

- `match_score`: raw transcript similarity signal
- `match_confidence`: derived trust in the final canonical assignment

These are not the same thing.

### 13.2 Deterministic confidence inputs

In the initial rewrite, `match_confidence` is derived from deterministic
signals only, such as:

- raw similarity score
- rip-side runner-up margin
- episode-side runner-up margin
- nearby-episode ambiguity
- duplicate competition
- whether the selected reference was suspect
- whether the match required ambiguity escalation
- whether it was assigned only by final set reconciliation

### 13.3 LLM verification is not part of numeric confidence

An LLM `same_episode=true` result may allow an ambiguous pair to be accepted,
but it does not numerically increase stored `match_confidence` in the initial
rewrite.

---

## 14. Review Conditions

The stage flags review when any of the following remain true after matching:

1. one or more episodes unresolved
2. one or more matches below the review-confidence threshold
3. LLM verification rejected or failed on an ambiguous pair
4. reference quality is too suspect to trust the result
5. set reconciliation could not complete the season subset cleanly

Review is the preferred outcome when content signals are insufficient.

---

## 15. Envelope Results

After matching, the stage updates the envelope:

1. resolve `episodes[]` entries to canonical TMDB season/episode numbers where
   possible
2. store `episode_title` and `episode_air_date`
3. store both `match_score` and `match_confidence`
4. flag unresolved or suspect episodes with episode-level review reasons
5. store compact run-level provenance in `attributes.content_id`

Organizer behavior for TV uses episode-level review flags directly.

---

## 16. Required Logging

At INFO level, content ID must log:

- reference candidate selection decisions and reasons
- suspect-reference decisions and fallback attempts
- accepted episode matches with raw score and derived confidence
- ambiguous matches sent to LLM verification
- LLM verification outcomes
- unresolved episodes
- set-reconciliation decisions
- review-triggering structural anomalies

Decision logs should always include:

- `decision_type`
- `decision_result`
- `decision_reason`

---

## 17. Policy Defaults

The exact numeric defaults live in the `Policy` struct, but the intended public
behavior is driven by a small set of thresholds:

- `minSimilarityScore`: minimum raw content similarity worth considering
- `clearMatchMargin`: minimum separation required for a direct clear match
- `llmVerifyThreshold`: below this, LLM verification may be considered
- `lowConfidenceReviewThreshold`: below this, review is required

Additional diagnostics such as reverse-side competition or nearby-episode
ambiguity may exist internally, but they should not expand into a large family
of path-decoder-style public knobs.

---

## 18. Non-Goals

The production TV matcher does **not**:

- assume disc titles are in broadcast order
- require a forward or reverse monotone path
- choose among multiple runtime decoder strategies
- use the LLM as a global reassignment engine
- perform broad LLM cross-product verification across many episode pairs
- trust OpenSubtitles release numbering over TMDB canonical numbering
- hide ambiguity by inflating confidence to `1.0`

---

## 19. Summary

Spindle TV episode identification is a **content-first, canonically numbered,
review-friendly** pipeline:

- OpenSubtitles provides noisy reference candidates
- TMDB provides canonical numbering
- transcript similarity resolves the majority of matches
- references are fetched progressively with at most one scope expansion
- strongest non-conflicting content claims are accepted first
- the LLM handles only a few ambiguous pairwise comparisons
- set reconciliation fills only the final obvious hole
- ordering is weak evidence only
- unresolved ambiguity goes to review
