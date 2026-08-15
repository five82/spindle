# Multiple Editions (Theatrical / Extended / Director's Cut)

Covers: a disc (or two discs) carrying more than one cut of the same movie,
where the user wants more than one of them in the library.

## 1. Identify the cuts

1. `spindle disc scan --json`. Two feature-length titles with runtimes
   differing by more than ~30 seconds are different cuts (within 30s on a
   Disney-style disc they are language variants - same cut, pick the lowest
   playlist number, usually 00800.mpls = English).
2. Web-search the release to learn each cut's official runtime and label
   ("Theatrical", "Extended", "Director's Cut", "Unrated") - blu-ray.com
   reviews and Wikipedia list both runtimes. Assign each disc title to a cut
   by runtime.
3. `spindle disc identify` for the TMDB identity (TMDB's runtime is normally
   the theatrical cut - a useful tiebreak).

Two-disc editions: repeat the scan per disc; each disc typically carries one
cut plus its own extras.

## 2. Process

Both cuts are features: rip with `spindle rip`, encode with
`spindle encode`, and **generate subtitles for each cut** with
`spindle subtitle --tmdb-id ID` on each encoded file. An alternate cut
often fails download verification (different runtime) - use the
whisperx-subtitles skill for that cut. Commentary tracks are worth
preserving on special editions - check `spindle debug commentary` on the
ripped file if the release is known for one, and note the finding in the
report.

The automated pipeline can only produce one cut per disc (its title
selection picks a single primary), so when both cuts are wanted, orchestrate
both manually rather than mixing pipeline + manual for the same movie -
identical treatment keeps the two files consistent.

## 3. Naming and placement (Jellyfin)

Multiple versions of one movie are files in the same movie folder sharing
the folder's base name (which carries the `[tmdbid-ID]` marker), separated
by ` - ` and a version label. Authoritative reference:
https://jellyfin.org/docs/general/server/media/movies (Multiple Versions
section):

```
Movies/Film (2010) [tmdbid-12345]/
  Film (2010) [tmdbid-12345] - Theatrical.mkv
  Film (2010) [tmdbid-12345] - Extended.mkv
```

Jellyfin groups them into one library item with a version picker. The label
after ` - ` is free text; use the official edition names from research.

## 4. Verify and finish

- ffprobe both files: AV1 video, one English subrip stream each, durations
  matching the researched runtimes of their respective cuts (not each
  other's).
- `spindle jellyfin refresh`, clean scratch, `spindle start`.
