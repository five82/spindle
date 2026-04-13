# Spec Proposal: Bounded LLM Episode Candidate Picker

Status: Draft proposal only. Not approved. Not implemented.

## Summary

This proposal describes a possible fallback for TV episode identification after the
existing deterministic matching pass and the current pairwise LLM verification
pass both fail to produce an accepted match.

The proposed fallback is intentionally bounded:

- it would run only for unresolved items
- it would choose only from an explicit candidate list we provide
- it would be allowed to return `unknown`
- it would initially be review-only, not auto-assigning episodes

This is not an open-ended "what episode is this?" prompt. The LLM would not be
asked to invent episode names or numbers from memory.

## Why consider this

Some unresolved cases may fail for reasons that are difficult for the current
pairwise verifier:

- weak transcript overlap in the middle excerpt
- noisy transcription output
- poor or mismatched OpenSubtitles references
- no valid OpenSubtitles reference transcript for one or more candidate episodes
- adjacent episodes with thin distinguishing dialogue
- discs whose accepted neighbors imply a small plausible episode set, but not a
  single deterministic answer

In those cases, a bounded "pick from this candidate set or say unknown" step may
be more useful than repeated pairwise yes/no checks.

A particularly relevant use case is when the normal pairwise verifier cannot run
as intended because OpenSubtitles does not provide a usable reference subtitle
for a candidate episode. In that situation, a bounded TMDB-backed candidate
picker may be more justified than in ordinary ambiguity cases, because the
system is missing one of the normal comparison artifacts rather than merely
asking the LLM to do a broader job for convenience.

## Why we are not implementing it yet

We are explicitly not implementing this now for the following reasons:

1. **Current architecture keeps the LLM narrow**
   The current content ID design uses the LLM only as a pairwise verifier for an
   already-proposed ambiguous pair. A candidate picker would expand the LLM into
   a broader matching role.

2. **Open-ended identification is too risky**
   Asking the model to read a transcript and state the episode directly would
   invite hallucinated titles, wrong numbering, and weakly supported guesses.
   Even the bounded version is a material increase in responsibility compared to
   the current boolean verifier.

3. **We do not yet have evidence that we need it**
   Initial live testing of the current system produced strong yes/no results for
   pairwise verification. Before adding another fallback layer, we should first
   gather more evidence from hard unresolved items.

4. **TMDB metadata may be too thin**
   Episode titles and overviews are often short, generic, or incomplete. A
   candidate picker may look attractive in theory but fail in practice when the
   metadata does not contain the needed scene-level anchors.

5. **The spec prefers review over clever fallback stacks**
   Spindle currently prefers surfacing ambiguity for review rather than layering
   increasingly clever hidden matchers. This proposal should not be adopted
   unless testing shows a clear practical benefit.

6. **It is harder to validate than pairwise verification**
   "Are these the same episode?" is easy to score. "Which episode is this?" is a
   broader task with more opportunities for plausible but incorrect answers.

## Non-goals

This proposal does not introduce:

- open-ended episode identification from model memory
- automatic renumbering based on an LLM guess
- replacement of deterministic `match_confidence`
- replacement of the current pairwise verifier
- a hidden global rematcher across an entire season

## Proposed trigger

Only consider this step when one of the following paths applies.

### Path A: unresolved after normal verification

1. deterministic matching did not produce an accepted match for the rip
2. pairwise LLM verification did not produce an accepted match
3. the item remains unresolved after existing reconciliation rules
4. a bounded candidate set can be built from deterministic signals

### Path B: missing-reference fallback

1. deterministic matching did not produce an accepted match for the rip
2. pairwise LLM verification cannot run as intended because no valid
   OpenSubtitles reference transcript is available for one or more candidate
   episodes
3. the item remains unresolved after existing reconciliation rules
4. a bounded candidate set can be built from deterministic signals

Path B is an especially plausible reason to test this proposal because it
addresses a concrete artifact gap rather than simply adding another clever
fallback layer.

## Proposed candidate set rules

The LLM must not search the entire season freely. The system would construct a
small candidate set using existing deterministic information, such as:

- unresolved episodes remaining in the fetched season subset
- top deterministic content candidates for the rip
- neighborhood implied by already accepted adjacent episodes
- disc/season scope already established by the stage

The candidate set should stay small enough that each option can be explained and
reviewed easily.

## Proposed inputs

The review helper would receive:

- the middle excerpt from the transcription artifact for the unresolved rip
- a bounded list of candidate episodes
- for each candidate episode:
  - TMDB episode number
  - TMDB episode title
  - TMDB episode overview
- optionally, already accepted neighboring episodes for context
- optionally, an explicit indicator that a normal OpenSubtitles reference was
  missing or unusable for one or more candidates

It may also be worth testing whether adding selected reference subtitle excerpts
helps, but that should be evaluated separately. In the missing-reference case,
TMDB metadata may be the only structured external episode description available,
which is exactly why this path needs careful validation before adoption.

## Proposed response schema

The LLM should be forced to choose only from the provided candidates or return
`unknown`.

Example:

```json
{
  "decision": "pick",
  "episode_number": 7,
  "explanation": "The transcript excerpt matches the candidate overview about the courtroom hearing and prisoner transfer.",
  "needs_review": true
}
```

Or:

```json
{
  "decision": "unknown",
  "episode_number": null,
  "explanation": "The excerpt is too generic to distinguish between the remaining candidate episodes.",
  "needs_review": true
}
```

Notes:

- `needs_review` should remain `true` in the initial version
- no numeric LLM confidence should be introduced for this path unless later
  testing shows clear value
- this result must not numerically alter deterministic `match_confidence`

## Proposed initial operating mode

If this proposal is explored, it should start as a **review-only helper**.

That means:

- it may suggest a likely episode from the bounded candidate set
- it may explain why that candidate looks best
- it may say `unknown`
- it must not auto-assign the episode in the initial version

This keeps the feature useful for evaluation without silently changing the
production matching behavior.

## Evaluation plan required before implementation

Do not implement this proposal until it has been tested on a meaningful dataset
of unresolved or difficult cases.

Recommended evaluation set:

- adjacent or easily confused episodes
- recap-heavy episodes
- generic-dialogue excerpts
- poor transcription artifacts
- weak or mismatched subtitle references
- episodes with no valid OpenSubtitles reference transcript
- items that currently land in review after existing verification

Recommended metrics:

- top-1 accuracy within the provided candidate set
- top-3 usefulness if multiple ranked suggestions are tested later
- frequency of `unknown` on truly ambiguous cases
- false confident picks
- whether the output would have reduced review burden in practice
- whether TMDB metadata alone is sufficient, or too weak

## Adoption bar

This proposal should only move forward if testing shows that it:

- materially helps unresolved real-world cases
- does not create a high rate of plausible but incorrect episode picks
- provides value beyond the current deterministic plus pairwise-verifier flow
- remains understandable and reviewable in logs and UI

If those conditions are not met, unresolved items should continue to go to
review without adding this extra LLM step.
