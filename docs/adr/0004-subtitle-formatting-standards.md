# ADR 0004: Netflix-Aligned Subtitle Formatting With One Constants Source

Status: Accepted
Date: 2026-07-09

## Context

itemaudit reports kept surfacing the same subtitle QC telemetry on every
disc: `high_reading_speed`, `short_cue_duration`, `long_cue_duration`. The
formatting pipeline had grown by accretion into three engines with three
disjoint threshold sets: stable-ts (Python) split at 68 chars / 6.5 s, the Go
post-process re-split at 80 chars / 8.5 s with escape hatches (`cps <= 18.0`,
`+1.0`, inline `70`/`14`), and the validator flagged at 42 chars per line /
7.0 s. Cues of 7.0-8.5 s were deliberately produced by the formatter, skipped
by retiming (which ignored cues over 7.0 s), and then flagged by validation.
Short and fast cues were unfixable by design: the only remedy the pipeline
had was extending into silence, and there was no merge pass, so back-to-back
short cues stayed short and dense dialogue stayed over 20 cps. A
`trimLongDisplayCues` special case, dead SRT-level hallucination wrappers,
and a duplicated `formatResult`/`FormatResult` pair rounded out the bolt-ons.

## Decision

Adopt the Netflix English Timed Text Style Guide values as the single
formatting standard and make the formatter enforce every invariant the
validator checks, from one constants block in `internal/subtitle/validate.go`:
2 lines, 42 chars/line (84 chars/cue), 20 cps, 5/6 s minimum duration, 7 s
maximum, 2-frame (2/24 s) minimum gap. Layer ownership is now explicit:

- **stable-ts (Python)** owns word-timing-aware segmentation, at the same
  targets (`split_by_duration max_dur=7.0`, `split_by_length max_chars=84`).
  Its constants mirror the Go block by comment; it has no thresholds of its
  own anymore.
- **Go post-process** (`display_postprocess.go`) is a single normalization
  pass: split (only cues whose text cannot wrap into 2x42; duration splits
  belong to Python, which has word timings) -> merge -> wrap (42 chars,
  bottom-heavy) -> retime (hard 7 s cap, extend short/fast cues into gaps,
  enforce the 2-frame gap and non-overlap).
- **Validation** keeps the same checks and review routing but is now a
  regression detector for invariants the formatter enforces, not a report of
  known formatter/validator disagreement.

The merge pass is the substantive addition: a cue that is too short or too
fast to read joins its successor when the gap is <= 0.5 s, the combined text
wraps cleanly into two 42-char lines, and the combined span is <= 7 s.
Merging absorbs the inter-cue gap, which is the only way to lower reading
speed without dropping text; it is how professional subtitles handle rapid
short dialogue.

Deleted rather than patched: the duration/cps escape hatches and re-split
thresholds, `trimLongDisplayCues` and its four constants (the 7 s hard cap
subsumes it: 84 chars at 20 cps needs at most 4.2 s), the unused SRT-level
hallucination wrappers, and the private `formatResult` twin struct.

## Consequences

- `long_cue_duration` and `overlapping_cues` are now impossible in formatter
  output; `short_cue_duration` and `high_reading_speed` remain possible only
  where the audio genuinely allows no remedy (isolated dense speech with no
  adjacent gap and no mergeable neighbor). Telemetry counts should drop
  sharply; anything that persists is a real bug, not threshold skew.
- One threshold set means a future tuning change is a one-line edit in
  `validate.go` plus its mirrored comment in the Python script.
- Cue count drops slightly on dialogue-dense content (merges); validation on
  the next processed disc should confirm the telemetry improvement.
