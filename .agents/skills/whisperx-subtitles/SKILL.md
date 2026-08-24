---
name: whisperx-subtitles
description: Generate an English WhisperX subtitle from primary English audio when the automated OpenSubtitles adoption path produced none - the pipeline skipped the title or `spindle subtitle` failed verification. You transcribe with WhisperX, act as the transcription editor yourself, and place the SRT. Use /whisperx-subtitles <file(s)>.
user-invocable: true
argument-hint: <media file path(s)>
---

# WhisperX Subtitle Generation

Produce an English SRT from primary English audio for a file
that has no verified OpenSubtitles download. Spindle no longer maintains
WhisperX display generation in code; this skill replaces it. You run WhisperX
for the raw transcript and you are the cleanup audit: read the result, fix
what is clearly wrong, leave everything else alone.

## Scope: English primary audio only

Probe the media before starting. If it has no non-commentary English primary
audio, stop and route the task to the orchestrate skill's
`references/foreign-language-feature.md` scenario. Do not use this skill to
translate a foreign-language feature, do not pass `--task translate`, and do
not switch from the configured Turbo model to a full Large model for that
purpose. The orchestration scenario tries a verified OpenSubtitles translation
first and OCRs the disc's authoritative full English PGS when needed.

## Before starting

- WhisperX competes with encodes for GPU/CPU. If the daemon is processing
  items (`spindle status --json`), wait or coordinate like the orchestrate
  skill does.
- Read `~/.config/spindle/config.toml` `[subtitles]` for the settings the
  pipeline uses: `whisperx_model` (default `large-v3`),
  `whisperx_cuda_enabled`, `whisperx_vad_method` (default `silero`),
  `opensubtitles_api_key`, and `mux_into_mkv`.

## 1. Transcribe

Pick the primary English audio stream with `ffprobe` (first English-language
audio track; skip commentary tracks — they usually carry a "Commentary"
title tag). Then:

```bash
ffmpeg -i FILE -map 0:a:N -ac 1 -ar 16000 -c:a pcm_s16le -vn -sn -dn -y work/audio.wav
uvx whisperx work/audio.wav --model large-v3 --language en \
  --vad_method silero --output_format srt --output_dir work \
  --device cpu --compute_type int8   # cuda/float16 when whisperx_cuda_enabled
```

WhisperX aligns word timings itself; the emitted SRT timing is good.

## 2. Edit the transcript (you are the audit)

Read the whole SRT. Fix only clear ASR defects:

- **Remove**: isolated hallucinations with silence around them ("Thank
  you.", "Thanks for watching.", "Subscribe."), end-credits song lyrics
  after the last dialogue, empty or symbol-only cues, adjacent duplicate
  cues, mojibake.
- **Replace**: unambiguous homophones (their/there, "would of"), and wrong
  names/terms only when the title identity or surrounding dialogue proves
  the correction. Optionally cross-check wording against an OpenSubtitles
  reference: search `https://api.opensubtitles.com/api/v1/subtitles?tmdb_id=...`
  with the config's `Api-Key` header and download a candidate as untimed
  reference text. Its timing is irrelevant; only use it to confirm wording.
- **Never**: sanitize profanity or dialect, rephrase awkward-but-real
  dialogue, remove garbled mid-film speech, replace a plausible word just
  because another fits, or invent content. A false correction is worse than
  a missed error - when unsure, leave the cue alone.

## 3. Format

Netflix-style targets: max 2 lines per cue, max 42 characters per line, cue
duration 0.8-7s, no overlapping cues, no cues past the video's runtime
(check with `ffprobe`). Split or merge cues where the transcript violates
these badly; small deviations are fine.

## 4. Place

Final output is SRT — never PGS. Respect `mux_into_mkv`:

- Sidecar: `Name.en.srt` next to the video file when it is kept outside Loom;
  Loom ignores external subtitle files.
- Mux: for a Loom library, use `mkvmerge -o tmp.mkv FILE --language 0:eng --track-name 0:English final.srt`
  then replace the original. If the file already has subtitle tracks, drop
  them with `--no-subtitles` on the input file.

## 5. Verify

Spot-check sync at the start, middle, and end (compare a cue's timestamp
against the audio with `mpv --start=` or by extracting a few seconds of
audio). Confirm the first dialogue cue matches the first spoken line.
