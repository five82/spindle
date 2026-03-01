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
// Matching evaluates ordered candidate strategies (rip-spec seed, anchor window,
// disc block, then full season). For each strategy, it computes transcript
// similarity and uses a global Hungarian assignment to map ripped episodes to
// references. The best strategy is selected by match coverage and confidence,
// with review flags propagated when refinement or verification detects risk.
//
// Low-confidence matches can be verified with an optional LLM pass. Rejected
// pairs are cross-compared and reassigned using Hungarian assignment to preserve
// global optimality across ambiguous episodes.
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
