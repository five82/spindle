# One Movie Across Two Discs

Covers: a single film split over two discs (long epics, roadshow editions).

## 1. Determine the target shape: one file or two

Check TMDB (via `spindle disc identify` and a web search of the TMDB entry):

- **TMDB lists it as one movie with one full runtime** (the normal case,
  e.g. a 4-hour cut split for disc capacity): the library wants **one file**.
  Confirm: disc A runtime + disc B runtime ≈ TMDB runtime.
- **TMDB models the parts as separate movies** (each part has its own entry,
  poster, and runtime - e.g. films released theatrically as two parts):
  the library wants **two files, as two separate movies**, each processed
  and named against its own TMDB entry. In that case handle each disc as a
  normal single feature (prefer the automated pipeline per disc) and stop
  reading here.

## 2. One-file flow

1. Rip the feature title from each disc with `spindle rip` into scratch
   (scan each disc; the feature is the long title). Note which file is part
   1 vs part 2 - disc labels and runtimes tell you.
2. **Join before encoding** so the result is one seamless encode:

   ```
   mkvmerge -o scratch/joined.mkv part1.mkv + part2.mkv
   ```

   mkvtoolnix is already a Spindle dependency. Appending requires matching
   stream layouts (same codecs/resolution - true for the two halves of one
   film). Verify the joined duration equals the sum of the parts and plays
   across the seam (`ffprobe`, and spot-check a few seconds around the join
   point with ffmpeg if in doubt).
3. `spindle encode scratch/joined.mkv -o scratch/encoded/` - one encode of
   the full film.
4. `spindle debug subtitle` on the encoded file (it is a feature).
5. Place as a normal single movie:
   `Movies/Film (1963) [tmdbid-12345]/Film (1963) [tmdbid-12345].mkv`.

If the halves genuinely will not concatenate (different resolutions or
layouts), fall back to Jellyfin's multi-part naming in one folder -
`Film (1963) [tmdbid-12345] - part1.mkv` / `... - part2.mkv` (verify the
current part-naming convention at
https://jellyfin.org/docs/general/server/media/movies) - and say in the
report why the join was not possible.

## 3. Verify and finish

- ffprobe: AV1 video, duration ≈ TMDB runtime, one English subrip stream.
- `spindle jellyfin refresh`, clean scratch, `spindle start`.
