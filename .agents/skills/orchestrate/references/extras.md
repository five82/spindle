# Extras and Theatrical Shorts

Covers: finding and processing disc extras (with or without the main movie),
and theatrical shorts (Pixar, Looney Tunes, Disney) that accompany a feature.

## 1. Inventory and research

1. `spindle disc scan --json` - every title with runtime, chapters, size,
   playlist. The main feature is usually the longest title (or an 800-series
   playlist on Disney/Pixar discs); everything else is a candidate extra.
2. `spindle disc identify` - get the TMDB identity of the feature so the
   movie folder name is exact.
3. Web-search the specific release's extras. Best sources:
   - blu-ray.com review of this exact edition - the "Special Features"
     section usually lists every extra **with its runtime**.
   - The studio's press release or packaging listing.
   - For shorts: TMDB/Wikipedia have the short's own title, year, and runtime.
4. Match scanned titles to researched extras **by runtime** (within ~1-2%).
   Duplicated runtimes usually mean the same extra in multiple languages or
   with/without commentary - prefer the lower title ID and check audio track
   count after ripping if ambiguous. Report titles you cannot identify;
   don't guess names into the library.

## 2. Process

Rip all wanted titles in one pass: `spindle rip --title 4,7,9 -o scratch/ripped/`.
The command prints which file belongs to which title - record that mapping
immediately; filenames alone won't tell you later.

Then per file: `spindle encode scratch/ripped/FILE -o scratch/encoded/`.

- **Extras: no subtitles.**
- **Theatrical shorts: generate subtitles** - `spindle subtitle --tmdb-id ID
  scratch/encoded/SHORT.mkv` after encoding (shorts have their own TMDB
  entries). OpenSubtitles coverage for shorts is thin; expect to fall back
  to the whisperx-subtitles skill.
- If the movie itself is also wanted and is ordinary, run it through the
  normal pipeline instead (`spindle cache rip` then `spindle cache process`),
  and orchestrate only the extras manually. Do the cache rip while the
  daemon is stopped, then `spindle start` and `spindle cache process` so the
  daemon processes the feature while you wait; alternatively finish the
  extras first. Never run `spindle rip`/`spindle encode` after the daemon is
  back up.
- If the movie track is explicitly excluded, do not rip it at all.

## 3. Naming and placement (Loom)

Loom ignores nested movie videos, including everything under `extras/` and
`behindthescenes/`; it does not catalog nested extras folders. Do not
place featurettes, deleted scenes, interviews, trailers, or other extras in a
Loom movie directory as library content. Keep any requested non-short extras
outside Loom's configured libraries and report their location separately.

Theatrical shorts with their own TMDB ID are the supported exception. Place
each as a standalone item in `[library] shorts_dir` (under `[paths]
library_dir`) using Loom's one-video layout:

```
shorts/Short (Year) [tmdbid-ID]/Short (Year) [tmdbid-ID].mkv
```

The short directory and video filename must both carry the TMDB ID, and the
file must include subtitles. Do not attach the short under its parent movie
folder if it is expected to appear in Loom.

## 4. Verify and finish

- ffprobe each placed file: AV1 video, audio present, sane duration.
- Shorts: confirm exactly one English subrip stream, not forced.
- `spindle loom scan`, remove scratch, `spindle start`.
