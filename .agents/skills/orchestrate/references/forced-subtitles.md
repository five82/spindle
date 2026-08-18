# Foreign-Dialogue Forced Subtitles

Covers: an otherwise English-language feature contains dialogue, signs, or
other narrative material in another language that should appear automatically
without enabling the full English display subtitle.

The deliverable is a second English SubRip track embedded in the final MKV,
named `English (Forced)`, with forced=yes and default=no. Preserve Spindle's
regular English SRT as a separate non-forced track. Source PGS/SUP files, OCR
images, and working SRTs stay in scratch and never enter the library.

## 1. Establish whether a forced track is needed

1. Inspect the ripped source, not only the Reel encode: Reel does not carry the
   disc subtitle streams into its output.
2. List subtitle tracks and their metadata:

   ```bash
   mkvmerge -J RIP.mkv | jq '.tracks[] | select(.type == "subtitles") |
     {id, codec, language: .properties.language,
      name: .properties.track_name, forced: .properties.forced_track}'
   ```

3. Look for an English PGS track named `forced`, `forced only`, or `foreign
   parts`, or a track with forced disposition. MakeMKV may expose a
   `forced only` pseudo-track extracted from forced events in a full PGS track.
   It can be empty; a label alone is not proof.
4. Web-search the exact release and title for forced-subtitle information.
   Blu-ray reviews, MakeMKV discussions, OpenSubtitles `foreign_parts_only`
   results, and reports of the foreign-language scenes are supporting evidence.
   They are not substitutes for checking this rip.

Do not infer a forced track merely because the movie contains another
language. Some films intentionally leave dialogue untranslated, burn the
translation into the picture, or use the full English subtitle track only.

## 2. Choose the source

Use the first source that can be verified:

1. The rip's English forced-only PGS track. This is the best authority for
   which time ranges the disc intended to display.
2. An English `foreign_parts_only` SRT for the exact title/episode and release.
3. A reconstruction from a synchronized full English SRT, but only when PGS
   timing, audiovisual evidence, or reliable scene information identifies the
   forced ranges. Text in a full English SRT alone cannot distinguish English
   speech from an English translation of foreign speech.

Prefer the disc PGS timing plus text from Spindle's synchronized full English
SRT. OCR is then a draft and timing mask rather than the sole source of text:
match full-SRT cues that overlap each forced cue, and use OCR text only where
there is no trustworthy match. Allow a small timing tolerance because the full
SRT may have been retimed.

An aligned English WhisperX transcript can identify candidates when no forced
track exists: full-SRT cues with no corresponding English speech may be foreign
dialogue or narrative signs. They may also be ASR misses, paraphrases, songs,
or inaudible speech, so an LLM may propose candidates but must not approve them
without audiovisual or external evidence.

## 3. Extract and OCR disc PGS

Work in scratch. Extract only the selected subtitle track, using the track ID
reported by `mkvmerge -J`:

Name the extracted file with `.en.sup`: PGSRip derives the Tesseract language
from that suffix.

```bash
mkvextract tracks RIP.mkv TRACK_ID:forced-source.en.sup
```

Check the optional OCR prerequisites before using them:

```bash
command -v tesseract
tesseract --list-langs 2>/dev/null | grep -qx eng
```

If either check fails, explain that OCR needs Tesseract and its English model,
and ask the operator before installing them:

```bash
sudo apt install tesseract-ocr tesseract-ocr-eng
```

Do not add these packages to Spindle's normal dependencies. Run PGSRip through
`uvx` so its Python environment remains external to the project:

```bash
uvx pgsrip --keep-temp-files forced-source.en.sup
```

This writes `forced-source.en.srt`; rename it to the final video stem only
after review.

The PGS images normally contain the English translation even though the audio
is in another language, so use the English OCR model. Keep temporary images
until review is complete. If PGSRip produces no cues, treat the MakeMKV
forced-only track as empty and continue with the next source; do not create an
empty forced SRT.

## 4. Construct and review the SRT

For every forced cue:

1. Compare the OCR image and draft text.
2. Find overlapping text in the synchronized full English SRT when available.
3. Prefer the full SRT's spelling and punctuation when it clearly represents
   the same displayed translation.
4. Correct OCR errors manually. Preserve meaningful two-line dialogue, but
   remove PGS styling and positioning that SRT cannot represent.
5. Confirm from the scene that the cue translates foreign dialogue or required
   narrative text. Remove ordinary English dialogue accidentally selected by
   loose timing overlap.

An LLM can compare OCR images, candidate SRT cues, transcripts, and web
research, but the disc timing and scene remain authoritative. Do not fabricate
translations from plot summaries. If a source cue is foreign text rather than
an English translation, translate it only with enough scene context to verify
speaker, meaning, and timing.

Write a valid, renumbered UTF-8 SRT. It should usually be sparse. Continuous
coverage through ordinary English dialogue indicates that a full subtitle
track was selected by mistake.

## 5. Embed and verify

Before muxing:

- Parse or probe the SRT and confirm it is non-empty and contains no invalid or
  overlapping time ranges.
- Inspect every cue against the source image and corresponding video scene.
- Confirm there are no OCR artifacts, credits, advertisements, full-dialogue
  stretches, or untranslated required lines.
- Confirm cue times use the final encode's timeline and the last cue does not
  exceed its duration.

Mux in scratch before library placement whenever possible. Do not remove the
regular subtitle already embedded by Spindle. Track-specific `mkvmerge`
options must precede the forced SRT input:

```bash
mkvmerge -o OUTPUT.forced.mkv \
  INPUT.mkv \
  --language 0:eng \
  --track-name '0:English (Forced)' \
  --default-track-flag 0:no \
  --forced-display-flag 0:yes \
  forced-source.en.srt
```

If the pipeline already placed `INPUT.mkv` in the library, stop the daemon,
build and verify the replacement in scratch, then replace the library file by
same-filesystem rename. Never run `mkvmerge` directly over the input. If
scratch and the library are on different filesystems, copy the verified output
to a hidden temporary file on the library filesystem and rename it into place
only after the copy completes.

Probe the remuxed MKV and compare it with the input. Inspect subtitle labels
and flags directly:

```bash
mkvmerge -J OUTPUT.forced.mkv | jq '[.tracks[] |
  select(.type == "subtitles") |
  {codec, language: .properties.language, name: .properties.track_name,
   default: .properties.default_track, forced: .properties.forced_track}]'
```

Require all of the following:

- Video and audio stream counts, codecs, dispositions, and duration are
  unchanged.
- The regular English subtitle is still SubRip, non-forced, and retains its
  prior default disposition.
- Exactly one new SubRip track exists with language `eng`, title
  `English (Forced)`, forced disposition enabled, and default disabled.
- No PGS subtitle is present.

Only then place or atomically replace the final MKV, remove any forced-SRT
sidecar beside it, run `spindle jellyfin refresh`, and verify Jellyfin reports
both the regular English subtitle and the embedded English forced subtitle
with the correct flags. Delete the SUP, working SRT, OCR images, and other
scratch artifacts only after this verification succeeds.
