// Package contentid provides TV episode matching by correlating WhisperX
// transcripts against OpenSubtitles reference subtitles.
//
// The Matcher generates AI transcripts for ripped episode files, downloads
// OpenSubtitles references for the target season, then computes text similarity
// scores to map disc titles (Title00.mkv, Title01.mkv, etc.) to definitive
// episode numbers. This resolves the common problem where physical disc layout
// differs from the intended episode sequence.
//
// The matcher is invoked by the episode identification stage (internal/episodeid)
// after ripping completes. It updates the rip specification in-place with
// confirmed episode mappings so downstream encoding and organizing stages have
// correct metadata.
//
// Matching uses a greedy algorithm with a similarity floor (~0.58). When no
// match clears the threshold, the original heuristic ordering remains and the
// item may be flagged for manual review.
//
// Configuration dependencies:
//   - opensubtitles_enabled, opensubtitles_api_key, opensubtitles_user_agent
//   - opensubtitles_cache_dir enables reference subtitle caching across runs
//   - whisperx_cuda_enabled toggles GPU acceleration for transcription
//   - tmdb_api_key for season episode list lookups
//
// The matcher coordinates with internal/subtitles for WhisperX transcription
// and internal/subtitles/opensubtitles for reference downloads.
package contentid
