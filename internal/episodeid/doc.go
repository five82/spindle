// Package episodeid coordinates TV episode identification by comparing WhisperX
// transcripts of ripped MKV files against OpenSubtitles reference subtitles.
//
// This stage runs after ripping completes and only applies to TV show discs. It
// resolves the common problem of multi-episode discs where MakeMKV produces files
// in disc order (Title00.mkv, Title01.mkv, etc.) but the actual episode sequence
// may differ from the physical layout.
//
// The matcher generates WhisperX transcripts for each ripped file, downloads
// OpenSubtitles references for the season, then uses text similarity scoring to
// map disc titles to definitive episode numbers. Results are written back to the
// rip specification and queue metadata so downstream encoding and organizing stages
// have correct episode labels.
//
// Configuration dependencies:
//   - opensubtitles_enabled must be true
//   - opensubtitles_api_key, opensubtitles_user_agent required
//   - whisperx_cuda_enabled toggles GPU acceleration
//   - opensubtitles_cache_dir enables reference subtitle caching
//
// Stage behavior:
//   - Movies: skipped entirely (status transitions directly from RIPPED to ENCODING)
//   - TV shows without OpenSubtitles: skipped with warning, falls back to heuristic ordering
//   - TV shows with OpenSubtitles: full WhisperX + OpenSubtitles correlation
//   - Transient failures (network, API rate limits): bubble up as retriable errors
//   - Matching failures below confidence threshold: log warning but allow pipeline to continue
//
// The stage coordinates with internal/contentid for the core matching logic and
// internal/subtitles/opensubtitles for reference subtitle downloads.
package episodeid
