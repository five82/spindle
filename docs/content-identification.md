# Content Identification

Reference notes for how Spindle currently classifies discs and prepares metadata. Keep this in sync with `disc/analyzer.py`, `disc/metadata_extractor.py`, and TMDB service changes.

## Pipeline Summary

1. **MakeMKV scan** supplies the canonical list of titles, durations, and audio/subtitle metadata.
2. **IntelligentDiscAnalyzer** (`disc/analyzer.py`) infers whether the disc is a movie or TV set, selects titles to rip, and estimates confidence.
3. **Optional enhanced metadata** from `EnhancedDiscMetadataExtractor` overlays extra hints when disc labels look generic and a mounted filesystem is available.
4. **TMDB lookup** (`services/tmdb.py`) runs once per disc with runtime/season hints to return `MediaInfo` for naming and library organization.
5. **Series/multi-disc context** is cached so later discs in the same set inherit the same mapping (`disc/multi_disc.py`, `disc/series_cache.py`).

## Disc Analysis Details

### MakeMKV-first heuristics
- Main title: longest duration track (movie) or clustered TV episodes (3+ titles within `tv_episode_min/max_duration`).
- Extras: pulled in when `include_extras` is enabled, bounded by `max_extras_to_rip` and `max_extras_duration`.
- Commentary: discovered per-title when `include_commentary_tracks` is true (`DiscAnalysisResult.commentary_tracks`).
- Confidence starts at `MOVIE_CONFIDENCE_BASE`/`TV_CONFIDENCE_BASE` and is raised by TMDB matches.

### Enhanced metadata overlay
Enabled when `enable_enhanced_disc_metadata` is true and we have a mounted `disc_path`:
- Runs `bd_info` (if installed) plus parses `bdmt_eng.xml` and `mcmf.xml` when present (`disc/metadata_extractor.py`).
- Supplies candidate titles in priority order (disc library name → bdmt title → cleaned volume id → MakeMKV label).
- Detects season/disc numbers for TV sets and flags likely TV discs (`EnhancedDiscMetadata.is_tv_series`).
- Analyzer uses these hints to rename the primary title and to force TV handling if MakeMKV heuristics were inconclusive.

### TMDB identification
- Single async call via `TMDBService.identify_media(query, content_type, runtime_hint, season_hint)`.
- Query uses the cleaned label from MakeMKV/enhanced metadata; runtime and season hints narrow results.
- Results populate `DiscAnalysisResult.media_info` (title/year/season/episode) and bump confidence.
- Local in-memory cache handles duplicate lookups inside a run; persistent caching lives in `services/tmdb_cache.py` (see below).

### Output (`DiscAnalysisResult`)
Returned to the orchestrator and persisted in `rip_spec_data`:
- `primary_title`: normalized display name
- `content_type`: `movie` or `tv_series`
- `confidence`: 0.0–0.99
- `titles_to_rip`: list of MakeMKV `Title` objects selected for processing
- `commentary_tracks`: map of title id → commentary track ids
- `episode_mappings`: when TV, maps MakeMKV title ids to season/episode info
- `media_info`: TMDB metadata (or `None` on failure)
- `runtime_hint`: minutes, used later for TMDB refinements
- `enhanced_metadata`: raw metadata object for downstream consumers

## Multi-Disc & Series Handling

`disc/multi_disc.py` coordinates multi-disc sets:
- Uses `SeriesCache` (SQLite) to pin TMDB ids, show names, and season numbers across discs.
- Watches for season/disc hints from enhanced metadata and MakeMKV labels.
- Keeps a short-lived session window (`multi_disc_timeout_minutes`) so discs inserted back-to-back join the same workflow.
- Provides CLI-friendly stats and cleanup helpers via `SeriesCache.get_stats()` and `.cleanup_expired()`.

When adding new detection rules (e.g., mini-series), update:
1. `SeriesCache` schema/fields if persistence changes.
2. `DiscAnalysisResult.episode_mappings` format.
3. `tests/test_simple_multi_disc.py` and related suites.

## Caching Overview

`storage/cache.py` exposes a single `SpindleCache` that owns two SQLite-backed stores:

- **TMDB cache** (`services/tmdb_cache.py`)
  - Keyed by query + media type hash.
  - Stores both search payloads and detailed media info.
  - TTL defaults to `tmdb_cache_ttl_days`; `cleanup_expired()` prunes stale rows.
  - Use `tmdb_cache.get_stats()` for counts and size.

- **Series cache** (`disc/series_cache.py`)
  - Tracks show/season metadata to keep multi-disc runs consistent.
  - TTL defaults to `series_cache_ttl_days` (configurable).
  - Provides `get_series_metadata`, `cache_series_metadata`, and maintenance helpers.

When caches drift, call `SpindleCache.clear_all()` or use the CLI command once exposed. Document any new cache fields here.

## Configuration Knobs

All settings live in `config.py`; key fields affecting identification:

- `enable_enhanced_disc_metadata`: toggle bd_info/bdmt overlays.
- `tv_episode_min_duration` / `tv_episode_max_duration`: seconds bounds for TV clustering.
- `include_extras`, `max_extras_to_rip`, `max_extras_duration`: movie/TV extras policy.
- `include_commentary_tracks`, `max_commentary_tracks`: commentary behavior.
- `tmdb_cache_ttl_days`, `series_cache_ttl_days`: cache retention.
- `tmdb_runtime_tolerance_minutes`, `tmdb_confidence_threshold`: heuristics for TMDB matches.
- `multi_disc_timeout_minutes`, `multi_disc_session_timeout_minutes`: control session grouping.

Keep the table in sync when adding new config fields so future changes are discoverable.

## Failure & Review Flow

- Analyzer raises on empty MakeMKV results or inability to pick a main title; orchestrator treats this as a `FAILED` queue state with the raw MakeMKV log attached.
- TMDB lookup failures simply yield `media_info=None` and lower confidence; discs continue through ripping with filesystem-safe titles.
- Manual review uses the `review_dir` and queue `REVIEW` status; populate `rip_spec_data` with the partial analysis to aid follow-up tooling.
- Notify via ntfy on identification failures so you can intervene quickly.

## Troubleshooting

- **Generic titles**: Check enhanced metadata output (logged at debug level) to confirm the disc is mounted and bd_info ran. Configure `enable_enhanced_disc_metadata` and ensure the binary is in PATH.
- **TV detection misses**: Adjust `tv_episode_min/max_duration` or inspect durations in the log (look for clustered lengths). TV mode requires ≥3 candidates by default.
- **Bad TMDB matches**: Inspect `DiscAnalysisResult.primary_title` and the hint data; consider adding custom cleaning rules or forcing year hints.
- **Cache confusion**: `sqlite3 ~/.local/share/spindle/logs/tmdb_cache.db "SELECT query FROM tmdb_cache;"` and the equivalent `series_cache.db` commands help diagnose stale entries.
- **Multi-disc mismatch**: Run `SeriesCache.clear()` or `cleanup_expired()` to reset old sessions before retrying.

## Future Notes

- Subtitle analysis is intentionally omitted; revisit once the AI-generated subtitle workflow is ready.
- Consider exposing analyzer debug dumps via CLI for faster manual review.
- Review heuristics periodically with real discs to keep the TV/movie split accurate.

