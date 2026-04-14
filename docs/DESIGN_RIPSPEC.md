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
  "filename": "Movie Title (2024).mkv",
  "disc_source": "bluray" | "dvd" | "unknown"
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
  "match_score": 0.91,
  "match_confidence": 0.72
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

Attributes carry data that one stage writes and a later stage reads. Only data
that cannot be derived from existing envelope fields (metadata, episodes, assets)
belongs here.

**Removed fields and where they went:**
- `disc_source` -- moved to `metadata.disc_source`
- `disc_number` -- already in `metadata.disc_number`
- `subtitle_context` -- subtitles stage reads `metadata` directly (same data)
- `content_id_matches` -- episode resolution stored in `episodes[]`; raw match
  scores and derived confidence are stored on each episode and logged by the episode ID stage
- `primary_audio_description` -- stored as `PrimaryDescription` in `AudioAnalysisData` during audio analysis
- `subtitle_generation_summary` -- computed on-demand from
  `subtitle_generation_results`

**Episode review fields** now live on `episodes[]` rather than in envelope
attributes. Queue-level `needs_review` remains an aggregate flag; per-episode
routing decisions come from `episodes[].needs_review` and `episodes[].review_reason`.

**EnvelopeAttributes** -- 4 fields with writer/reader stages:

| Field | Type | Writer | Reader |
|-------|------|--------|--------|
| `has_forced_subtitle_track` | bool | Identification | Subtitles |
| `audio_analysis` | *AudioAnalysisData | Audio Analysis | API/Display |
| `subtitle_generation_results` | []SubtitleGenRecord | Subtitles | Organization |
| `content_id` | *ContentIDSummary | Episode ID | Audit/API/Display |

**Nested types:**

```go
Episode {
    Key             string
    TitleID         int
    Season          int
    Episode         int
    EpisodeTitle    string
    EpisodeAirDate  string
    RuntimeSeconds  int
    TitleHash       string
    OutputBasename  string
    MatchScore      float64
    MatchConfidence float64
    NeedsReview     bool
    ReviewReason    string
}

AudioAnalysisData {
    PrimaryTrack        AudioTrackRef          // {Index int}
    PrimaryDescription  string                 // "English | truehd | 8ch | Atmos"
    CommentaryTracks    []CommentaryTrackRef   // {Index, Confidence, Reason}
    ExcludedTracks      []ExcludedTrackRef     // {Index, Reason, Similarity}
}

SubtitleGenRecord {
    EpisodeKey            string
    Source                string  // "qwen3_asr" or "opensubtitles"
    Cached                bool
    SubtitlePath          string
    Segments              int
    Language              string
    OpenSubtitlesDecision string
}

ContentIDSummary {
    Method                string  // e.g. "qwen3_asr_tfidf_content_matcher"
    ReferenceSource       string  // e.g. "opensubtitles"
    ReferenceEpisodes     int
    TranscribedEpisodes   int
    MatchedEpisodes       int
    UnresolvedEpisodes    int
    LowConfidenceCount    int
    ReviewThreshold       float64
    SequenceContiguous    bool
    EpisodesSynchronized  bool
    Completed             bool
}
```

## 7. Envelope Methods

| Method | Signature | Purpose |
|--------|-----------|---------|
| `Parse` | `Parse(raw string) (Envelope, error)` | Deserialize JSON; returns empty envelope on blank input |
| `Encode` | `Encode() (string, error)` | Serialize to JSON |
| `EpisodeByKey` | `EpisodeByKey(key string) *Episode` | Case-insensitive lookup; nil if not found |
| `Episode.AppendReviewReason` | `AppendReviewReason(reason string)` | Sets the episode-level review flag and appends a human-readable reason |
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
| `EpisodeRangeKey(season, start, end)` | Format `s01e01-e02`; falls back to `EpisodeKey` when end <= start |
| `HasResolvedEpisodes(episodes)` | Any episode with Episode > 0 |
| `HasUnresolvedEpisodes(episodes)` | Any episode with Episode <= 0 |
| `CountUnresolvedEpisodes(episodes)` | Count episodes with Episode <= 0 |
