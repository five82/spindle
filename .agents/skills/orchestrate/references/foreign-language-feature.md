# Foreign-Language Feature Subtitles

Covers: a feature whose primary dialogue is not English and therefore needs a
full English display subtitle. This is not a forced-subtitle case. A forced
track is sparse translation for foreign dialogue inside an otherwise
understandable feature; this scenario needs the feature's normal English
subtitle throughout its non-English dialogue.

The source order is:

1. A full English OpenSubtitles candidate verified against the actual rip.
2. The disc's full English PGS track, OCRed and manually corrected to SRT.
3. Stop and report the failure.

Do not use Whisper or another speech translator as a third subtitle source.
Translation errors are harder to verify than OCR against the disc's visible
English text, and translated English cannot receive trustworthy phoneme
alignment against non-English speech.

## 1. Confirm the scenario and preserve the source

Inspect the ripped source before encoding:

```bash
ffprobe -v error -show_streams -of json RIP.mkv | jq '[.streams[] |
  select(.codec_type == "audio" or .codec_type == "subtitle") |
  {index, codec_type, codec_name, channels,
   language: .tags.language, title: .tags.title,
   default: .disposition.default, forced: .disposition.forced}]'
```

Require actual stream evidence that the primary feature audio is non-English.
Keep the original-language primary audio; an English audio-description track
is not a dub and must not replace it. Apply the normal orchestration audio
policy before encoding: retain the best original-language primary plus only
confirmed commentary tracks.

Keep the untouched rip in scratch until the final English SRT has been
verified. Reel does not carry the source subtitle streams into its encode, so
the rip is the authority and the OCR fallback source.

If the primary dialogue is English and only some scenes need translation, stop
using this reference and follow `forced-subtitles.md` instead.

## 2. Try OpenSubtitles before OCR

Search the OpenSubtitles API using the movie's TMDB ID, media type `movie`, and
language `en`. Read `opensubtitles_api_key` and `opensubtitles_user_token` from
`~/.config/spindle/config.toml` without printing either credential.

API searches do not consume the download quota. Rank full candidates in this
order:

1. `foreign_parts_only=false`.
2. Blu-ray release matching the disc's runtime and frame rate.
3. Not machine- or AI-translated.
4. Non-hearing-impaired, unless accessibility captions are specifically
   wanted.
5. Higher rating and download count.

Do not run `spindle subtitle` for this scenario. It verifies downloaded
English text against a same-language WhisperX transcript of the non-English
audio, so a valid translation will fail its text-similarity gate.

Download and test candidates one at a time, no more than three. The API
`/download` call consumes quota even when authenticated with the configured
API key and user token. After each call, record and report the response's
`remaining` value.

For each downloaded SRT:

1. Parse it and reject malformed, empty, foreign-parts-only, multi-file, or
   obviously incomplete subtitles.
2. Compare its first and last cue with the actual feature duration. A last cue
   before end credits is normal; a track covering only a small part of the
   feature is not.
3. Retain only dialogue/subtitle text. Remove release advertisements and
   obvious uploader spam without rewriting the translation.
4. Sync it against the refined rip's audio, which avoids cross-language text
   comparison:

   ```bash
   uvx ffsubsync REFINED-RIP.mkv -i candidate.srt -o candidate.synced.srt
   ```

5. Spot-check cues against the video at the beginning, at several distributed
   points through the middle, and near the final dialogue. Confirm both timing
   and meaning using the scene and the disc English PGS when needed.
6. Reject a candidate if synchronization requires local repairs, it drifts,
   it represents another cut, or its English text does not match the disc's
   translation closely enough to trust.

A verified candidate becomes the final English SRT. Keep the rip and its PGS
tracks until final mux verification is complete.

## 3. OCR the disc full English PGS when needed

Use this path only when no downloaded candidate passes. List the source
subtitle tracks:

```bash
mkvmerge -J RIP.mkv | jq '.tracks[] | select(.type == "subtitles") |
  {id, codec, language: .properties.language,
   name: .properties.track_name, forced: .properties.forced_track}'
```

Choose the full English track, not a `forced only` or `foreign parts` track.
Prefer the non-HI track unless accessibility captions are wanted. A full
non-HI English track may omit already-English speech while translating all
non-English dialogue; that is acceptable for an English-speaking viewer.

Check the optional OCR prerequisites before using them:

```bash
command -v tesseract
tesseract --list-langs 2>/dev/null | grep -qx eng
```

If Tesseract or its English model is missing, explain the requirement and ask
before installing `tesseract-ocr` and `tesseract-ocr-eng`. Do not add either to
Spindle's dependencies.

Extract and OCR in scratch:

```bash
mkvextract tracks RIP.mkv TRACK_ID:english-full.en.sup
uvx pgsrip --keep-temp-files english-full.en.sup
```

Review every generated cue against its PGS image. Correct OCR spelling,
punctuation, line breaks, and italics; remove OCR artifacts; do not rewrite or
invent translations. Confirm the final SRT is non-empty, ordered, has no
invalid overlaps, and has no cue past the final encode duration.

## 4. Mux and verify

A full English translation is a normal display subtitle, not a forced track.
For a feature with no English primary audio, make it the default so playback
is understandable without relying on a Jellyfin profile's subtitle policy:

- Codec: SubRip/SRT
- Language: `eng`
- Track name: `English`
- Default: yes
- Forced: no

Mux in scratch, dropping any subtitle streams inherited by the input:

```bash
mkvmerge -o OUTPUT.english.mkv \
  --no-subtitles INPUT.mkv \
  --language 0:eng \
  --track-name '0:English' \
  --default-track-flag 0:yes \
  --forced-display-flag 0:no \
  final.en.srt
```

Inspect the result with `mkvmerge -J` and `ffprobe`. Require:

- Video, audio streams, duration, and dispositions are unchanged.
- Exactly the intended English SubRip track is present.
- Its language, name, default flag, and forced flag match the policy above.
- No PGS subtitle remains.
- Start, middle, and end subtitle spot-checks remain synchronized in the final
  MKV.

Only then place the file, refresh Jellyfin, verify Jellyfin reports the
English subtitle correctly, and delete the downloaded candidates, SUP, OCR
images, and other scratch files.
