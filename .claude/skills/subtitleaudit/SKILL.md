---
name: subtitleaudit
description: Audit and fix WhisperX transcription errors in MKV subtitle tracks. Use /subtitleaudit <path_to.mkv> to review and correct the primary embedded subtitle.
user-invocable: true
argument-hint: <path_to.mkv>
---

# Subtitle Audit Skill

Review and correct WhisperX transcription errors in MKV-embedded subtitles.

## Usage

`/subtitleaudit /path/to/file.mkv`

## Overview

This skill extracts the primary (non-forced) SRT subtitle from an MKV file, reviews it for obvious WhisperX transcription errors, presents proposed corrections for user approval, applies the approved edits, and muxes the corrected subtitle back into the MKV.

## Prerequisites

Required tools (verify before proceeding):
- `ffprobe` - Media stream inspection
- `ffmpeg` - Subtitle extraction
- `mkvmerge` - Subtitle muxing

## Procedure

### Phase 1: Extract Primary Subtitle

1. **Probe the MKV** to identify subtitle streams:
   ```bash
   ffprobe -v error -show_streams -select_streams s -of json "<mkv_file>"
   ```

2. **Identify the primary subtitle stream**:
   - Look for subtitle streams (codec_type: "subtitle", codec_name: "subrip" or "srt")
   - The primary subtitle is the one with `disposition.default=1` AND `disposition.forced=0`
   - If no default is set, pick the first non-forced subtitle stream
   - If the only subtitle stream has `disposition.forced=1`, abort with message: "Only a forced subtitle track found - no primary subtitle to audit."
   - If no subtitle streams exist, abort with message: "No subtitle tracks found in this file."

3. **Extract the primary subtitle** to a temp file:
   ```bash
   ffmpeg -v error -i "<mkv_file>" -map 0:s:<stream_index> -c:s srt "/tmp/subtitleaudit_<basename>.srt"
   ```
   - Use the relative subtitle stream index (not the absolute stream index)
   - `<basename>` is the MKV filename without extension

4. **Record stream metadata** for later muxing:
   - Language code (from `tags.language` or stream metadata)
   - Track title (from `tags.title`)
   - Whether forced subtitle tracks also exist (for re-muxing)
   - Total number of subtitle streams and their properties

### Phase 2: Review for Transcription Errors

Read the extracted SRT file and analyze it for **obvious** WhisperX transcription errors. Err heavily on the side of caution -- false positives (incorrect "corrections") are worse than missed errors.

**DO flag these (high confidence):**

| Error Type | Description | Example |
|------------|-------------|---------|
| Residual hallucinations | Repeated filler phrases the hallucination filter missed | Isolated "Thank you." / "Thanks for watching." not in dialogue context |
| Credits music/lyrics | End-credits music transcribed as dialogue subtitles. These appear after the final scene ends and contain song lyrics, not spoken dialogue. | Cues after the story ends containing partial song lyrics like "Down upon us and it flows like water" |
| Background music bleed | Incidental music or soundtrack lyrics incorrectly transcribed as dialogue mid-film. Look for cues that contain song lyrics clearly not spoken by characters, especially during montages or transitions. | "He's a goat, he's a god, he's a man, he's a guru" from a background song playing in a scene |
| Misattributed sound effects | Non-dialogue sounds transcribed as if they were speech. Obvious cases only: clearly non-verbal sounds rendered as words. | "BOOM!" transcribed as dialogue when it's a sound effect; exclamations like "Ah!Ah!" with no speaker context |
| Garbled nonsense | Words/phrases that are clearly not English or make no sense in context | "the flibberty jibbet of cromulence" when context makes no sense |
| Obvious homophones | Wrong word where audio context makes the correct word unambiguous | "their" vs "there" vs "they're" when sentence grammar makes it clear |
| Broken cues | Empty cues, cues with only whitespace, or cues with only punctuation | A cue containing just "..." or " " |
| Encoding artifacts | Mojibake or corrupted characters | "donâ€™t" instead of "don't" |
| Repeated cues | Adjacent cues with identical or near-identical text and overlapping timestamps | Same line appearing twice in a row |
| Music notation artifacts | Orphaned music symbols without content | A cue with just a lone music note symbol |

**DO NOT flag these (too subjective or risky):**

| Skip | Why |
|------|-----|
| Proper noun spelling | WhisperX may have the correct uncommon spelling; we can't verify without the script |
| Grammar "corrections" | The dialogue may be intentionally ungrammatical (dialect, character voice) |
| Punctuation style | Comma placement, semicolons vs periods -- these are style choices |
| Timing adjustments | Timestamp corrections require audio reference we don't have |
| Rephrasing for clarity | The transcription may be accurate even if awkward |
| Line break choices | How text is split across lines is a formatting preference |
| Capitalization of dialogue | Some transcriptions use sentence case, others don't -- both are valid |
| Suspected mishearings | Unless the correct word is unambiguous from surrounding text, don't guess |
| Diegetic singing | Characters singing on-screen is valid dialogue and should stay |
| Ambiguous exclamations | Short cues like "Oh!" or "No!" during dialogue scenes are likely real speech |

### Phase 3: Present Proposed Edits

Present findings in this format:

```
## Subtitle Audit: <filename>

**Stream info:** Stream #<n>, Language: <lang>, Title: "<title>"
**Cue count:** <N> cues | **Duration:** <first_timestamp> - <last_timestamp>

### Proposed Edits

Found <N> issues:

**1. [<Error Type>] Cue #<number> (<timestamp>)**
- Current: `<current text>`
- Proposed: `<corrected text>` (or REMOVE if the cue should be deleted)
- Reason: <brief explanation>

**2. [<Error Type>] Cue #<number> (<timestamp>)**
...

### No Issues Found

If no obvious errors are detected, report:
"No high-confidence transcription errors found. The subtitle file looks clean."
```

**After presenting**, ask the user which edits to apply:
- "Apply all" - apply every proposed edit
- "Apply selected" - let user specify which numbered edits to apply
- "Cancel" - discard all changes

### Phase 4: Apply Approved Edits

1. Read the SRT file from `/tmp/subtitleaudit_<basename>.srt`
2. Apply the approved edits:
   - For text corrections: replace the cue text
   - For cue removals: delete the entire cue block (index line, timestamp line, text lines, blank separator)
3. After removals, **re-index cue numbers** sequentially (1, 2, 3, ...)
4. Write the edited file to `/tmp/subtitleaudit_<basename>_edited.srt`
5. Show a summary: "Applied N edits. Ready to mux back into MKV."

### Phase 5: Mux Edited Subtitle Back into MKV

1. **Reconstruct all subtitle tracks** for muxing. The goal is to replace ONLY the primary subtitle while preserving all other subtitle tracks exactly as they were.

2. **If there are other subtitle tracks** (e.g., forced):
   - Extract each non-primary subtitle to `/tmp/` as well:
     ```bash
     ffmpeg -v error -i "<mkv_file>" -map 0:s:<other_index> -c:s srt "/tmp/subtitleaudit_<basename>_forced.srt"
     ```

3. **Build the mkvmerge command**:
   ```bash
   mkvmerge -o "/tmp/subtitleaudit_<basename>_muxed.mkv" \
     -S "<mkv_file>" \
     --language 0:<lang3> --track-name "0:<title>" --default-track-flag 0:yes \
     "/tmp/subtitleaudit_<basename>_edited.srt" \
     [--language 0:<lang3> --track-name "0:<forced_title>" --default-track-flag 0:no --forced-display-flag 0:yes \
     "/tmp/subtitleaudit_<basename>_forced.srt"]
   ```
   - Use the original language codes and track titles captured in Phase 1
   - `-S` strips existing subtitles from the source
   - Preserve the original disposition flags (default, forced)
   - Use ISO 639-2 (3-letter) language codes for mkvmerge

4. **Verify the muxed file**:
   ```bash
   ffprobe -v error -show_streams -select_streams s -of json "/tmp/subtitleaudit_<basename>_muxed.mkv"
   ```
   - Confirm subtitle track count matches original
   - Confirm language codes and titles are correct
   - Confirm disposition flags are correct

5. **Replace the original**:
   - Ask user for confirmation: "Replace original file at `<path>`?"
   - On confirmation:
     ```bash
     mv "/tmp/subtitleaudit_<basename>_muxed.mkv" "<original_mkv_path>"
     ```
   - On decline: report the muxed file location for manual handling

6. **Clean up temp files**:
   ```bash
   rm -f /tmp/subtitleaudit_<basename>*.srt
   ```
   (Only clean up SRT temps; if user declined the replace, keep the muxed MKV.)

## Error Handling

- If any tool (`ffprobe`, `ffmpeg`, `mkvmerge`) is not found, report which tool is missing and stop.
- If extraction fails, report the ffmpeg error and stop.
- If muxing fails, report the mkvmerge error. The original file is untouched (muxing goes to a temp file first).
- Never modify the original MKV until the muxed replacement is verified.

## Guiding Principles

1. **Conservative edits only.** A false positive (bad "correction") is worse than a missed error. When in doubt, skip it.
2. **Original file safety.** The original MKV is never modified in-place. All work happens on temp files, and the replacement is atomic (mv).
3. **Preserve all tracks.** Video, audio, and non-primary subtitle tracks must pass through unchanged.
4. **User controls everything.** Every edit requires approval. The final file replacement requires explicit confirmation.
