---
name: orchestrate
description: Take over Spindle workflow orchestration for disc edge cases the automated pipeline does not handle - extras, theatrical shorts, multi-disc movies, multiple editions, and MakeMKV troubleshooting. Use /orchestrate <what you want done with the disc>.
user-invocable: true
argument-hint: <scenario description>
---

# Spindle Orchestration Skill

Manually orchestrate the Spindle pipeline (rip -> encode -> subtitle -> name ->
place -> refresh) for content the automated daemon workflow does not cover.
The daemon handles the standard case: one disc, one feature (or one TV
season), fully automated. Everything else - extras, shorts, alternate cuts,
discs that need troubleshooting - is this skill's job. You are the
orchestrator: you make the judgment calls (what each title is, what it should
be named, where it belongs) and the spindle commands do the mechanical work.

## Daemon protocol (always, before any drive or encode work)

Spindle and this skill must never work at the same time - they compete for
the optical drive, the encoder, and the library.

1. `spindle status --json`
2. If `{"running": false}`: proceed.
3. If running and every queue stage bucket other than `completed` / `failed`
   is 0 (or the item in flight is only awaiting review): `spindle stop`, then proceed.
4. If items are actively processing: **wait**. Poll `spindle status --json`
   every few minutes until in-flight work drains, then `spindle stop`.
5. When the orchestration task is fully done (including verification):
   `spindle start` to hand control back to the daemon.

`spindle rip` and `spindle encode` refuse to run while the daemon is up, so a
forgotten stop fails loudly rather than corrupting anything.

## Command toolbox

| Command | Purpose |
|---------|---------|
| `spindle status --json` | Daemon state and queue stage counts |
| `spindle disc scan --json` | Raw MakeMKV title inventory (id, runtime, chapters, size, playlist). Defaults to `--min-length 0` so short extras show up. **These IDs are what `spindle rip` takes** |
| `spindle disc identify` | Disc label, fingerprint, TMDB match candidates (human-readable but parseable) |
| `spindle rip --title 2,5 -o DIR` | Rip specific titles to a directory (`--all` for everything). Use the same `--min-length` as the scan that produced the IDs (default 0 for both) |
| `spindle encode FILE -o DIR` | Reel AV1 target-quality encode of one file, same encode path as the daemon. Exits non-zero if validation fails |
| `spindle debug subtitle FILE` | Generate a WhisperX English SRT and mux it into the file (or `--external` for a sidecar) |
| `spindle cache rip` / `spindle cache process N` | Route a disc's *main feature* through the normal automated pipeline (rip to cache, then queue it) |
| `spindle debug crop FILE` / `spindle debug commentary FILE` | Crop and commentary diagnostics for a single file |
| `spindle jellyfin refresh` | Trigger a Jellyfin library scan after manual placement |
| `spindle queue audit N` / `spindle logs` | Diagnostics for pipeline-processed items |

Library, review, and staging paths come from the config file
(`~/.config/spindle/config.toml`): `[paths] library_dir / review_dir /
staging_dir`, plus `[library] movies_dir / tv_dir`.

## Hard rules

- **Jellyfin-facing subtitle output is SRT.** Never place PGS as final output.
- **Follow the Jellyfin organization and naming standards** for everything
  placed in the library:
  - Movies (layout, extras folders, multiple versions, multi-part):
    https://jellyfin.org/docs/general/server/media/movies
  - Shows: https://jellyfin.org/docs/general/server/media/shows
- **Follow Spindle's naming convention on top of that**: every movie folder,
  movie file, and show folder carries the TMDB provider ID -
  `Title (Year) [tmdbid-ID]` (TV: `Show (Year) [tmdbid-ID]/Season NN/`).
  This is what the automated pipeline produces, and Spindle tooling depends
  on it - `spindle debug subtitle` reads the `[tmdbid-ID]` marker from
  library paths to fetch reference transcripts. Get the ID from
  `spindle disc identify` or TMDB directly. Extras files inside the named
  subfolders do not need the marker; the feature file and its folder do.
- **Subtitles:** none for extras (featurettes, deleted scenes, interviews,
  trailers). Yes for theatrical shorts (Pixar, Looney Tunes, etc.) and for
  every feature-length cut - run `spindle debug subtitle` on the encoded file.
- **Verify before placing.** `ffprobe` every encoded file: AV1 video, expected
  audio streams, duration within ~2s of the ripped source. `spindle encode`
  already validates, but confirm anything you renamed or remuxed yourself.
- **Work in a scratch directory** (e.g. under the configured staging dir or a
  temp dir), move files into the library only as the final step, and delete
  the scratch area when done. Never leave partial files in the library.
- **Movie track goes through the normal pipeline when possible.** When a
  scenario includes processing the main feature and nothing about the feature
  itself is unusual, use `spindle cache rip` + `spindle cache process` so the
  feature gets the full automated treatment (identification, commentary
  detection, subtitle audit, organization). Orchestrate manually only the
  parts the pipeline can't do.
- Web research (runtimes, extras listings, edition details) should be
  cross-checked against actual title runtimes from `spindle disc scan` -
  runtime agreement within ~1-2% is the primary matching signal.
- Finish with `spindle jellyfin refresh`, then `spindle start`.

## Scenario routing

Read only the reference file(s) matching the request:

| Scenario | Reference |
|----------|-----------|
| Find/process disc extras (with or without the movie); theatrical shorts on a feature disc | `references/extras.md` |
| Standard + extended/director's cut editions (same disc or two discs) | `references/editions.md` |
| One movie spanning two discs | `references/multi-disc.md` |
| MakeMKV can't read/rip the disc | `references/troubleshooting.md` |

Scenarios compose: a two-disc extended edition uses `editions.md` +
`multi-disc.md`; a troubleshot disc continues into whichever scenario applies.

## General flow

Every scenario follows the same skeleton:

1. Daemon protocol (above).
2. Inventory: `spindle disc scan --json`, plus `spindle disc identify` for
   TMDB identity when naming needs it.
3. Research: web-search the release (blu-ray.com disc reviews list extras
   with runtimes; TMDB for runtimes/editions) and map titles to content by
   runtime.
4. Rip the selected titles with `spindle rip` into a scratch directory.
5. Encode each with `spindle encode`.
6. Subtitle where the rules above say so.
7. Verify with ffprobe.
8. Name and place into the library per the reference file's conventions.
9. `spindle jellyfin refresh`, clean scratch, `spindle start`.
10. Report what was produced: each output path, what it is, and how each
    disc title was identified (runtime match, web source).

When the user's request doesn't fit any reference, apply this skeleton with
your own judgment and say so in the report.
