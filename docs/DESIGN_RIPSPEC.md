# System Design: RipSpec Envelope

The RipSpec envelope is the central data structure shared across all pipeline
stages. It is serialized as JSON in the `rip_spec_data` column.

See [DESIGN_INDEX.md](DESIGN_INDEX.md) for the complete document map.

---

## 1. Structure

```json
{
  "version": 1,
  "fingerprint": "sha256:...",
  "content_key": "movie-12345" | "tv-67890-s01",
  "metadata": { ... },
  "titles": [ ... ],
  "episodes": [ ... ],
  "assets": { ... },
  "attributes": { ... }
}
```

## 1.1 Envelope Version

The `version` field (integer) enables forward-compatible parsing. `Parse()`
must check this field and reject envelopes with an unrecognized version rather
than silently misinterpreting changed fields. The current version is **1**.

When the envelope schema changes (added/renamed/removed fields), bump the
version number. Items with older envelope versions in the queue will fail
clearly on parse, allowing the user to clear and reprocess them.

---

## 2. Metadata

```json
{
  "id": 12345,
  "title": "Movie Title",
  "overview": "Plot summary...",
  "media_type": "movie" | "tv" | "unknown",
  "show_title": "TV Show Name",
  "series_title": "Series Name",
  "year": "2024",
  "release_date": "2024-01-15",
  "first_air_date": "2024-03-01",
  "imdb_id": "tt1234567",
  "language": "en",
  "season_number": 1,
  "disc_number": 2,
  "vote_average": 7.5,
  "vote_count": 1234,
  "movie": true,
  "cached": false,
  "edition": "Extended Edition",
  "filename": "Movie Title (2024) - Extended Edition.mkv"
}
```

## 3. Titles

Captured from MakeMKV disc scanning:

```json
{
  "id": 0,
  "name": "Title Name",
  "duration": 7200,
  "chapters": 32,
  "playlist": "00800.mpls",
  "segment_count": 1,
  "segment_map": "1",
  "title_hash": "abc123",
  "season": 1,
  "episode": 3,
  "episode_title": "Episode Name",
  "episode_air_date": "2024-03-15"
}
```

## 4. Episodes

For TV content, each episode maps to a disc title:

```json
{
  "key": "s01e03" | "s01_001",
  "title_id": 0,
  "season": 1,
  "episode": 3,
  "episode_title": "Episode Name",
  "episode_air_date": "2024-03-15",
  "runtime_seconds": 2700,
  "title_hash": "abc123",
  "output_basename": "Show Name - S01E03 - Episode Name",
  "match_confidence": 0.87
}
```

Fields `title_hash` and `output_basename` are populated during identification and
ripping respectively. An episode is **unresolved** when `episode <= 0`.

**Key formats:**
- **Resolved**: `s01e03` (season 1, episode 3) -- episode number is known
- **Placeholder**: `s01_001` (season 1, disc index 1) -- episode number TBD

## 5. Assets (4 stages)

```json
{
  "ripped": [{"episode_key": "s01e03", "title_id": 0, "path": "/path/to/ripped.mkv", "status": "completed", "error_msg": ""}],
  "encoded": [{"episode_key": "s01e03", "path": "/path/to/encoded.mkv", "status": "completed", "error_msg": ""}],
  "subtitled": [{"episode_key": "s01e03", "path": "/path/to/subtitled.mkv", "status": "completed", "subtitles_muxed": true, "error_msg": ""}],
  "final": [{"episode_key": "s01e03", "path": "/library/tv/Show/Season 01/Show - S01E03.mkv", "status": "completed", "error_msg": ""}]
}
```

Each asset also carries a `title_id` field linking back to the MakeMKV title index.

**Asset fields**: `episode_key`, `title_id`, `path`, `status`, `error_msg`.

Asset statuses: `pending`, `completed`, `failed`. The `error_msg` field carries per-episode
error details for failed assets.

## 6. Attributes (Cross-Stage Communication)

**EnvelopeAttributes** -- all 11 fields with writer/reader stages:

| Field | Type | Writer | Reader |
|-------|------|--------|--------|
| `disc_source` | string | Identification | Audit Gathering |
| `disc_number` | int | Identification | Organization |
| `has_forced_subtitle_track` | bool | Identification | Subtitles |
| `subtitle_context` | *SubtitleContext | Identification | Subtitles |
| `content_id_needs_review` | bool | Episode ID | Organization |
| `content_id_review_reason` | string | Episode ID | Organization |
| `content_id_matches` | []ContentIDMatch | Episode ID | Organization |
| `primary_audio_description` | string | Audio Analysis | API/Display |
| `audio_analysis` | *AudioAnalysisData | Audio Analysis | Encoding |
| `subtitle_generation_results` | []SubtitleGenRecord | Subtitles | Organization |
| `subtitle_generation_summary` | *SubtitleGenSummary | Subtitles | API/Display |

**Nested types:**

```go
ContentIDMatch {
    EpisodeKey        string   // e.g., "s01e03"
    TitleID           int      // disc title index
    MatchedEpisode    int      // reference episode number
    Score             float64  // cosine similarity 0.0-1.0
    SubtitleFileID    int64    // OpenSubtitles file ID
    SubtitleLanguage  string
    SubtitleCachePath string
}

AudioAnalysisData {
    PrimaryTrack     AudioTrackRef          // {Index int}
    CommentaryTracks []CommentaryTrackRef   // {Index, Confidence, Reason}
    ExcludedTracks   []ExcludedTrackRef     // {Index, Reason, Similarity}
}

SubtitleGenRecord {
    EpisodeKey            string
    Source                string  // "whisperx" or "opensubtitles"
    Cached                bool
    SubtitlePath          string
    Segments              int
    Language              string
    OpenSubtitlesDecision string
}

SubtitleContext {
    Title         string  // resolved title
    ShowTitle     string  // TV show title (empty for movies)
    MediaType     string  // "movie" or "tv"
    TMDBID        int     // TMDB ID
    ParentTMDBID  int     // series TMDB ID (TV only)
    Year          string  // release year
    Season        int     // season number (TV only)
    Edition       string  // edition label (if any)
}

SubtitleGenSummary {
    Source                string
    OpenSubtitles         int
    WhisperX              int
    ExpectedOpenSubtitles bool
    FallbackUsed          bool
}
```

## 7. Envelope Methods

| Method | Signature | Purpose |
|--------|-----------|---------|
| `Parse` | `Parse(raw string) (Envelope, error)` | Deserialize JSON; returns empty envelope on blank input |
| `Encode` | `Encode() (string, error)` | Serialize to JSON |
| `EpisodeByKey` | `EpisodeByKey(key string) *Episode` | Case-insensitive lookup; nil if not found |
| `AppendReviewReason` | `AppendReviewReason(reason string)` | Sets review flag, appends reason with "; " separator |
| `ExpectedCount` | `ExpectedCount() int` | len(Episodes) for TV, 1 for movies |
| `AssetCounts` | `AssetCounts() (expected, ripped, encoded, final int)` | Per-stage completion counts |
| `MissingEpisodes` | `MissingEpisodes(stage string) []string` | Episode keys without assets at stage; nil for movies |

**Asset methods:**

| Method | Purpose |
|--------|---------|
| `AddAsset(kind, asset)` | Append or replace asset for episode key |
| `FindAsset(kind, key)` | Locate by stage and key; returns (Asset, bool) |
| `IsCompleted()` | Path non-empty and status != "failed" |
| `IsFailed()` | Status == "failed" |
| `ClearFailedAsset(kind, key)` | Reset status/error/path for retry |
| `CompletedAssetCount(stage)` | Count of non-failed assets with non-empty path at given stage |
| `Clone()` | Deep copy all asset lists |

**Helper functions:**

| Function | Purpose |
|----------|---------|
| `PlaceholderKey(season, discIndex)` | Format `s01_001`; defaults to 1 if <= 0 |
| `EpisodeKey(season, episode)` | Format `s01e03`; returns "" if both <= 0 |
| `HasResolvedEpisodes(episodes)` | Any episode with Episode > 0 |
| `HasUnresolvedEpisodes(episodes)` | Any episode with Episode <= 0 |
| `CountUnresolvedEpisodes(episodes)` | Count episodes with Episode <= 0 |
