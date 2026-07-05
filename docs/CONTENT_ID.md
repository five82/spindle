# TV Content Identification

Status: Active contract.

TV discs often expose episodes as generic titles without canonical episode
numbers. Spindle's content ID stage maps ripped TV title files to TMDB season and
episode numbers using transcript similarity against reference subtitles.

Exact scoring and data structures live in `internal/contentid` and its tests.
This document captures the intended behavior and review policy.

## Principles

- **Content is primary.** Match dialogue and scene content before trusting disc
  order, title number, or subtitle release numbering.
- **TMDB is canonical.** Final season/episode numbers come from TMDB metadata.
- **Reference numbering can be wrong.** Subtitle releases may split pilots,
  merge episodes, or use alternate numbering.
- **The LLM has a narrow job.** It verifies ambiguous transcript pairs; it does
  not invent episode identities from memory.
- **Review beats clever fallback stacks.** When evidence is weak or conflicted,
  route to review instead of silently assigning a low-confidence match.
- **Observable decisions matter.** Candidate selection, reference acquisition,
  match outcomes, LLM verification, and review routing should be logged.

## Preconditions

The stage runs for TV items after ripping. It expects:

- A valid RipSpec envelope with TV metadata and expected episodes.
- Completed ripped assets for selected title files.
- TMDB metadata sufficient to know the target season/episode set.
- WhisperX transcription support.
- An OpenSubtitles API key so reference subtitles can be fetched. This stage
  uses the configured API key directly; subtitle generation does not fetch
  OpenSubtitles output tracks.

Movies and non-TV items skip this stage. TV items without required matcher
clients complete the stage with a degraded warning, are marked for review, and
continue to encoding; they are not treated as cleanly identified. Transient
OpenSubtitles HTTP/network failures are retried by the OpenSubtitles client;
runtime transcription,
TMDB-season, or reference-acquisition errors that remain after retry fail the
stage so the operator can fix the external dependency and retry.

## Pipeline

At a high level the stage:

1. Selects primary audio for each ripped title.
2. Produces a canonical transcript with WhisperX.
3. Builds the target episode candidate set from TMDB metadata.
4. Searches OpenSubtitles for reference subtitles for candidate episodes.
5. Validates and normalizes reference subtitles.
6. Fingerprints ripped transcripts and references.
7. Builds a claim matrix of rip-to-reference similarity.
8. Accepts clear deterministic matches and strong-margin decisive lower-similarity matches.
9. Uses one-to-one constraints to avoid assigning the same rip or target episode
   twice.
10. Challenges ambiguous pairs with the LLM when configured and appropriate.
11. Applies conservative reconciliation for a single unresolved hole when supported.
12. Persists episode mapping, confidence, provenance, and review state into the
    RipSpec envelope.

## Reference acquisition

OpenSubtitles references are evidence, not authority. The stage may search
multiple variants for an episode and should reject references that are empty,
obviously unrelated, wrong language, or too weak to support matching. Transient
OpenSubtitles API and subtitle-file fetch failures are retried before being
reported as reference-acquisition failures.

Weak or missing references should be visible in logs and may trigger review.

## Matching semantics

A match has two related but separate ideas:

- **Similarity score**: deterministic transcript/reference similarity.
- **Confidence**: strength of the assignment considering score, margin,
  uniqueness, and supporting evidence.

Ordering can support a decision but is not the primary solver. Spindle should not
force a sequential assignment when content evidence disagrees.

Episode match logs include a confidence quality:

- `clear`: high confidence and clear margins over alternatives.
- `decisive_low_similarity`: transcript similarity is below the clear-match
  threshold, but still above the deterministic auto-accept threshold and margins
  over all alternatives are strong. This means the match is not confused with
  another episode; it is accepted without LLM verification.
- `ambiguous`: one or more margins are not strong enough for deterministic
  acceptance.
- `contested`: confidence is below review threshold, a reference is suspect, or
  the closest neighboring episode is too close.

Accepted matches record provenance such as deterministic clear match or LLM
verified match. Unresolved or conflicting matches should preserve enough context
for audit/review. The `contentid_matches` decision should distinguish
`ambiguous_rips`, `decisive_low_similarity_rips`, and `contested_rips` so audit
reports do not confuse decisive lower-similarity matches with real assignment
ambiguity. When a candidate is challenged by the LLM, logs should include
`verification_reason` / `verification_trigger` values explaining why the pair was
challenged, such as `rip_margin` or
`confidence_below_auto_accept_threshold`.

## LLM verification

The LLM compares two transcript excerpts and answers whether they are from the
same episode. Inputs are bounded to the candidate rip/reference pair Spindle
provides. Candidates are challenged when assignment margins are
ambiguous/contested, references are suspect, or confidence falls below
`decisive_auto_accept_threshold`. Strong-margin matches between
`decisive_auto_accept_threshold` and `clear_confidence_threshold` are accepted as
`decisive_low_similarity` instead of spending LLM calls on non-ambiguous cases.

The LLM must not:

- Choose from the whole series.
- Invent episode titles or numbers.
- Override deterministic evidence without being asked to verify a specific pair.

If LLM verification fails, rejects a candidate, or cannot resolve ambiguity, the
item should be routed to review rather than guessed.

## Review conditions

Route TV episode identification to review when any of these materially affect the
result:

- Expected episodes cannot be matched to ripped titles.
- Multiple plausible assignments conflict.
- Similarity or confidence is below the configured acceptance thresholds.
- References are missing, weak, or contradictory.
- LLM verification fails or rejects an ambiguous pair.
- The final mapping violates one-to-one assignment constraints.

Review is not failure by itself. It means Spindle produced the best available
mapping but requires operator confirmation.

Two dispositions carry structural meaning:

- **Probable extra.** Title selection deliberately over-selects and lets
  content evidence arbitrate. A rip whose similarity falls below the minimum
  against every candidate reference is classified a probable extra (not an
  unresolved episode), routed to the review directory, and does not block the
  item's matched episodes.
- **Incomplete episode set.** After matching (and opening-double correction),
  a disc 1 set that does not start at episode 1, or a matched subset with
  multiple gaps, marks every resolved episode for review. A known-incomplete
  season set is never delivered to the library.

## Configuration

Content ID policy is controlled by the `[content_id]` config section. Important
thresholds include:

- `min_similarity_score`
- `clear_match_margin`
- `low_confidence_review_threshold`
- `decisive_auto_accept_threshold`
- `clear_confidence_threshold`

Exact defaults live in `internal/config`, the generated sample config from
`spindle config init`, and [CONFIG.md](CONFIG.md).

## Implementation pointers

- Stage handler, candidate scope, reference selection, matching, reconciliation, and double-episode inference: `internal/contentid`.
- Shared transcription: `internal/transcription`.
- OpenSubtitles client: `internal/opensubtitles`.
- LLM client: `internal/llm`.
- Envelope fields: `internal/ripspec`.
- Audit support: `internal/auditgather`.
