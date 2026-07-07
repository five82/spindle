# ADR 0003: In-Pipeline LLM Subtitle Audit

Status: Accepted
Date: 2026-07-07

## Context

WhisperX display subtitles carry a predictable class of errors that the
deterministic formatter cannot catch: hallucinated phrases outside its fixed
phrase list, end-credits song lyrics, mid-film soundtrack lyrics transcribed
as dialogue, garbled words, homophones, and mojibake. Until now the only
mitigation was the `.agents/skills/subtitleaudit` skill, a manual/agent-run
post-hoc pass over an already-organized library file. Nothing in the pipeline
itself reviewed subtitle content.

## Decision

Run a single LLM audit pass in the subtitle stage, after the display SRT is
formatted and before validation/muxing (`internal/subtitle/audit.go`,
`GenerateDisplaySubtitle` in `internal/subtitle/subtitle.go`). One JSON-mode
call to the configured OpenRouter model (default `google/gemini-3-flash-preview`,
same client and `llm.api_key` gate as commentary detection, no new config)
sends all cues and gets back proposed remove/replace edits in fixed
categories (hallucination, credits_music, music_bleed, garbled, homophone,
broken, repeated, encoding).

Code-side guardrails, not model trust:

- Edits are resolved by exact cue-text match against the parsed SRT, never by
  the model's cue index, which drifts.
- Replacements apply only at `confidence: high` and are re-wrapped through the
  normal display formatter.
- If resolved removals outside the end-credits window (last 420s of runtime)
  exceed `max(5, 10% of cues)`, the whole response is rejected and the queue
  item is flagged for review.
- The audit is an improver, never a gate: no LLM configured, an API error, or
  a rejected response all just log a warning and the pipeline continues with
  the unaudited SRT.
- Only the derived display SRT is touched. Canonical WhisperX transcript
  artifacts are never modified.

## Evidence

Empirical test (2026-07-07, five spindle-processed titles across movie and TV
content) compared three OpenRouter models. Gemini Flash was fast (5-19s,
~$0.01-0.04/title), returned clean JSON on every call, and produced the best
precision-per-risk: correct end-credits song removal, hallucination variants
outside the deterministic filter, and real name-typo fixes, while correctly
preserving genuine dialogue and interview audio. All three models showed
pervasive index drift (an edit citing the wrong cue number while quoting real
text from elsewhere in the file), which is why text-keyed resolution is a
hard requirement rather than a nicety. Removals were near-100% precise across
all models; replacements were weaker (~80%, and the misses landed on
already-wrong cues), which is why replacement is gated to high confidence
while removal is not. The alternatives were slower and riskier: DeepSeek
v4-flash whiffed entirely on one title, and v4-pro (best recall of the three)
deleted an iconic opening-titles song and made out-of-policy grammar edits,
which shaped two prompt rules: never remove opening-title-sequence songs, and
mid-film garbled speech is replace-or-skip, never remove.

## Consequences

- Cost and latency are negligible against a rip/encode/transcribe pipeline.
- Model choice is a config change (`llm.model`), not a code change; Gemini
  Flash is the default on the strength of this test.
- The `subtitleaudit` skill remains as a post-hoc escape hatch: for library
  files organized before this feature existed, and for items the pipeline
  audit rejected or flagged for review.
