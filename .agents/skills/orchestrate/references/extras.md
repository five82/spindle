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
- **Theatrical shorts: generate subtitles** - `spindle subtitle
  scratch/encoded/SHORT.mkv` after encoding.
- If the movie itself is also wanted and is ordinary, run it through the
  normal pipeline instead (`spindle cache rip` then `spindle cache process`),
  and orchestrate only the extras manually. Do the cache rip while the
  daemon is stopped, then `spindle start` and `spindle cache process` so the
  daemon processes the feature while you wait; alternatively finish the
  extras first. Never run `spindle rip`/`spindle encode` after the daemon is
  back up.
- If the movie track is explicitly excluded, do not rip it at all.

## 3. Naming and placement (Jellyfin)

Extras live inside the movie's folder in named subdirectories Jellyfin
recognizes. Authoritative reference:
https://jellyfin.org/docs/general/server/media/movies (Extras section).
The established layout:

```
Movies/Film (2010) [tmdbid-12345]/
  Film (2010) [tmdbid-12345].mkv        <- the feature (pipeline-produced)
  shorts/Geri's Game (1997).mkv         <- theatrical shorts
  featurettes/The Making of Film.mkv
  deleted scenes/Alternate Ending.mkv
  interviews/Director Interview.mkv
  trailers/Theatrical Trailer.mkv
  extras/Anything Unclassifiable.mkv
```

Recognized extras folders: `behind the scenes`, `deleted scenes`,
`interviews`, `scenes`, `samples`, `shorts`, `featurettes`, `clips`,
`trailers`, `extras`, `other`. Pick the most specific one; `extras` is the
fallback. Name each file with the extra's real researched title, not the
disc title name.

If the feature was processed by the pipeline, the movie folder already
exists in the library - add the extras subfolders to it. If only extras were
requested and no movie folder exists, create it with the exact
`Title (Year) [tmdbid-ID]` name from `spindle disc identify` so a future
feature rip lands in the same folder.

A theatrical short can alternatively be its own library movie (its own
`Short (Year) [tmdbid-ID]/` folder - most theatrical shorts have their own
TMDB entry - with subtitles) when the user wants it surfaced as a
standalone item - ask only if the request is ambiguous; default to
`shorts/` inside the parent movie.

## 4. Verify and finish

- ffprobe each placed file: AV1 video, audio present, sane duration.
- Shorts: confirm exactly one English subrip stream, not forced.
- `spindle jellyfin refresh`, remove scratch, `spindle start`.
