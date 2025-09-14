# Content Identification Process

## Overview

Spindle uses a sophisticated multi-phase content identification system that combines disc metadata extraction, intelligent title selection, and TMDB API integration to accurately identify movies and TV shows from physical discs.

## Architecture

The content identification process is built around three main components:

1. **Enhanced Metadata Extraction** - Extracts comprehensive disc information from multiple sources
2. **Intelligent Title Selection** - Selects the best content to rip based on criteria
3. **Multi-Phase TMDB Identification** - Identifies content using multiple strategies with fallback

## Phase 1: Enhanced Metadata Extraction

### Data Sources (Priority Order)

The `EnhancedDiscMetadataExtractor` collects metadata from multiple sources:

#### 1. bd_info Command (Highest Priority)
- **Purpose**: Most reliable source for Blu-ray disc information
- **Extracts**: Volume identifier, disc name, provider data
- **Location**: Executed against mounted disc path
- **Reliability**: Very high for Blu-ray discs

#### 2. bdmt_eng.xml (High Priority)
- **Purpose**: Official disc title and localization info
- **Location**: `/BDMV/META/DL/bdmt_eng.xml`
- **Extracts**: Official title, language, thumbnails
- **Reliability**: High when present

#### 3. Cleaned Volume ID (Medium Priority)
- **Purpose**: Processed volume identifier with cleanup
- **Extracts**: Cleaned and normalized volume labels
- **Processing**: Removes generic identifiers and formatting artifacts
- **Reliability**: Medium - depends on disc labeling quality

#### 4. MakeMKV Output (Lower Priority)
- **Purpose**: Track information and alternative disc labels
- **Extracts**: Title list, track details, disc label
- **Integration**: Populated by ripper during scan
- **Reliability**: Good for all disc types (but often generic labels)

### Title Candidate Generation

The system generates title candidates using:

```python
def get_best_title_candidates(self) -> list[str]:
    """Get title candidates in priority order."""
    candidates = []

    # Priority 1: bd_info disc library metadata
    if self.disc_name and not self._is_generic_label(self.disc_name):
        candidates.append(self.disc_name)

    # Priority 2: bdmt_eng.xml title
    if self.bdmt_title and not self._is_generic_label(self.bdmt_title):
        candidates.append(self.bdmt_title)

    # Priority 3: cleaned volume ID
    if self.volume_id:
        cleaned = self._clean_volume_id(self.volume_id)
        if cleaned and not self._is_generic_label(cleaned):
            candidates.append(cleaned)

    # Priority 4: MakeMKV label (often generic)
    if self.makemkv_label and not self._is_generic_label(self.makemkv_label):
        candidates.append(self.makemkv_label)

    return candidates
```

## Phase 2: Intelligent Title Selection

### Selection Criteria

The `IntelligentTitleSelector` uses sophisticated criteria to identify main content:

#### Content Type Detection for Plex Organization
- **Movies**: Single long title (>= 60 minutes) - includes feature films, documentary films, concerts
  - Will be organized as: `/Movies/Title (Year)/Title (Year).mkv`
- **TV Shows**: Multiple similar-length titles or episode patterns - includes TV series, documentary series, miniseries
  - Will be organized as: `/TV Shows/Show Name (Year)/Season XX/Show Name (Year) - sXXeXX.mkv`

Note: Plex only recognizes Movies and TV Shows as primary video content types. Content is organized based on structure (single film vs episodic series) rather than genre.

#### Selection Algorithm
1. **Duration Analysis**: Identifies titles by length patterns
2. **Audio Track Analysis**: Prefers titles with multiple audio options
3. **Subtitle Analysis**: Considers subtitle availability
4. **Chapter Analysis**: Uses chapter count as quality indicator
5. **Filename Patterns**: Recognizes episode/season patterns

### Title Filtering
- Filters out extras, trailers, and promotional content
- Identifies main features vs. bonus content
- Handles special editions and director's cuts
- Recognizes multi-disc releases

## Phase 3: Multi-Phase TMDB Identification

### Phase 3a: Runtime-Verified Search (High Confidence)
- **Input**: Title candidates + runtime from main title
- **Process**: Search TMDB with runtime matching
- **Confidence**: High - runtime verification prevents false matches
- **Fallback**: Continues to Phase 3b on failure

### Phase 3b: Pattern-Based Search (Medium Confidence)  
- **Input**: Cleaned title candidates
- **Process**: TMDB search with fuzzy matching
- **Confidence**: Medium - relies on title matching only
- **Fallback**: Manual review if no matches

### Search Strategy
```python
async def identify_disc_content(
    self,
    title_candidates: list[str],
    runtime_minutes: int | None = None,
    content_type: str | None = None,
) -> MediaInfo | None:
    """Identify disc content using multiple title sources and caching."""
    # Step 1: Extract best title from all sources
    clean_title = self.extract_best_title(title_candidates)

    if not clean_title:
        logger.warning("No usable title found in disc metadata")
        return None

    # Check if title is too generic for TMDB search
    if self._is_generic_title(clean_title):
        logger.info(f"Skipping TMDB search for generic title: '{clean_title}'")
        return None

    # Determine target media type for focused search
    target_media_type = "movie"  # Default fallback
    if content_type and content_type.lower() in ["tv_series", "tv", "television"]:
        target_media_type = "tv"

    # Step 2: Check cache first
    cached = self.cache.search_cache(clean_title, target_media_type)
    if cached and cached.is_valid():
        return self._convert_cached_result(cached, target_media_type)

    # Step 3: Perform TMDB search with runtime verification if available
    if runtime_minutes and target_media_type == "movie":
        result = await self._search_movie_with_runtime(clean_title, runtime_minutes)
        if result:
            return result

    # Step 4: Standard search without runtime verification
    return await self._search_by_title_patterns(clean_title, target_media_type)
```

## Multi-Disc Handling

### TV Series Detection
The system automatically detects multi-disc TV series:

1. **Series Identification**: Uses enhanced metadata to detect TV content
2. **Season/Episode Extraction**: Parses disc labels for season/episode info
3. **Metadata Caching**: Stores series information for consistent processing
4. **Cross-Disc Consistency**: Ensures all discs use same series metadata

### Movie Collection Detection
- Detects multi-disc movie releases
- Handles extended editions and bonus discs
- Manages director's cuts and theatrical versions

## Error Handling and Recovery

### Identification Failures
- **Unidentified Content**: Moved to review directory
- **Ambiguous Matches**: User notification with options
- **Network Issues**: Retry with exponential backoff
- **API Limits**: Intelligent rate limiting and caching

### Manual Review Process
When automatic identification fails:
1. Content moved to `review_dir/unidentified/`
2. User notification sent via ntfy
3. Manual identification options provided
4. Results can be added back to queue

## Caching Strategy

### TMDB Cache
- **Purpose**: Minimize API calls and improve performance
- **TTL**: 30 days (configurable)
- **Storage**: SQLite database in log directory
- **Scope**: Search results and detailed movie/TV info

### Series Cache  
- **Purpose**: Consistent multi-disc TV series processing
- **Storage**: SQLite database with series metadata
- **Scope**: Series title, season info, TMDB ID
- **Benefit**: Ensures all discs in series use same metadata

## Configuration

### Key Settings
```toml
# Content identification
max_extras_to_rip = 3                    # Maximum bonus content to rip
min_title_duration = 10                  # Minimum title length (minutes)
tmdb_cache_ttl_days = 30                 # TMDB cache expiration

# Multi-disc handling  
enable_series_detection = true           # Auto-detect TV series
series_cache_enabled = true              # Cache series metadata

# Fallback behavior
review_dir = "~/review-directory"        # Unidentified content location
auto_review_mode = false                 # Manual vs automatic review
```

## Dependencies

### Required
- **TMDB API**: Movie/TV identification service
- **MakeMKV**: Disc scanning and title information (accesses disc directly via device)

### Optional but Highly Recommended
- **Disc Automounting**: Access to disc filesystem for enhanced metadata extraction
  - Without it: Ripping works, but Phase 1 metadata extraction is skipped
  - With it: Can read bdmt_eng.xml, mcmf.xml for better identification

### Optional
- **bd_info**: Enhanced Blu-ray metadata (improves accuracy further)
- **Network Access**: TMDB API calls and notifications

## Troubleshooting

### Common Issues

#### Poor Identification Accuracy
1. **Check disc mounting**: Ensure disc is accessible at standard mount points
2. **Verify TMDB API**: Confirm API key is valid and quota available  
3. **Review disc quality**: Clean or damaged discs may have poor metadata
4. **Check title candidates**: Review log output for title candidates being used

#### Missing Metadata
1. **Disc mounting recommended**: Enhanced metadata extraction requires mounted disc (but ripping still works without it)
2. **bd_info not available**: Install bd_info for better Blu-ray support
3. **Generic disc labels**: Discs with labels like "LOGICAL_VOLUME_ID" provide no useful information

#### Multi-Disc Issues
1. **Series cache problems**: Clear series cache if incorrect metadata persists
2. **Season detection**: Verify disc labels contain season/episode information
3. **Inconsistent identification**: Check that all discs in series have similar labels

## Performance Considerations

### Optimization Strategies
- **Metadata caching**: Reduces redundant disc reads
- **TMDB result caching**: Minimizes API calls
- **Intelligent fallback**: Avoids unnecessary processing steps

### Resource Usage
- **Disk I/O**: Metadata extraction requires disc access
- **Network**: TMDB API calls for identification  
- **Memory**: Caching uses SQLite databases (minimal impact)
- **CPU**: Text processing and pattern matching (lightweight)

