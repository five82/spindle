package config

// SampleConfig returns a sample TOML configuration string with comments
// showing all sections and their default values.
func SampleConfig() string {
	return `# Spindle configuration file
# Omitted values use the defaults shown in comments.

[paths]
# Working directory for in-progress items
# staging_dir = "~/.local/share/spindle/staging"

# Root of Loom media libraries
# library_dir = "~/library"

# Daemon logs and queue DB
# state_dir = "~/.local/state/spindle"

# Unidentified files routed for manual review
# review_dir = "~/review"

[api]
# Optional TCP listen address for HTTP API (e.g., "127.0.0.1:7487")
# bind = ""

# Bearer token for HTTP API auth (or set SPINDLE_API_TOKEN env var)
# token = ""

[tmdb]
# TMDB API bearer token (required; or set TMDB_API_KEY env var)
api_key = ""

# TMDB API base URL
# base_url = "https://api.themoviedb.org/3"

# TMDB query language
# language = "en-US"

[loom]
# Loom server base URL (empty disables library scans)
# url = "http://localhost:8097"

[library]
# Subdirectory under library_dir for movies
# movies_dir = "movies"

# Subdirectory under library_dir for TV shows
# tv_dir = "tv"

# Subdirectory under library_dir for theatrical shorts (orchestrate skill only)
# shorts_dir = "shorts"

# Overwrite files already in library
# overwrite_existing = false

[notifications]
# ntfy topic URL (empty disables all notifications; treat the topic as a password)
# ntfy_topic = ""

# HTTP timeout in seconds
# request_timeout = 10

[subtitles]
# Enable the subtitle pipeline: adopt a cleaned OpenSubtitles download
# verified against the rip's WhisperX transcript, or skip the episode.
# Skipped episodes can get WhisperX subtitles via the whisperx-subtitles
# agent skill.
# enabled = false

# Embed subtitles in MKV container
# mux_into_mkv = true

# WhisperX model name
# whisperx_model = "large-v3"

# Enable CUDA acceleration
# whisperx_cuda_enabled = false

# Voice activity detection method: "silero" (default) or "pyannote"
#   silero  - fast, lightweight, no token required
#   pyannote - better precision with background noise and overlapping speech;
#              requires whisperx_hf_token to be set
# whisperx_vad_method = "silero"

# HuggingFace access token, required for pyannote VAD
# (or set HUGGING_FACE_HUB_TOKEN / HF_TOKEN env var)
# whisperx_hf_token = ""

# OpenSubtitles API key (or set OPENSUBTITLES_API_KEY env var)
# opensubtitles_api_key = ""

# User-Agent for OpenSubtitles requests
# Include an app version to satisfy OpenSubtitles expectations.
# opensubtitles_user_agent = "Spindle/dev v0.1.0"

# OpenSubtitles user token for downloads (or set OPENSUBTITLES_USER_TOKEN env var)
# opensubtitles_user_token = ""

# Preferred subtitle languages
# opensubtitles_languages = ["en"]

[rip_cache]
# Enable rip cache
# enabled = false

# Maximum cache size in GiB
# max_gib = 150

[disc_id_cache]
# Enable disc ID -> TMDB ID cache
# enabled = false

[makemkv]
# Optical drive device path
# optical_drive = "/dev/sr0"

# Rip timeout in seconds (4 hours)
# rip_timeout = 14400

# Disc info scan timeout in seconds (10 minutes)
# info_timeout = 600

# Seconds between disc access commands
# disc_settle_delay = 10

# Skip titles shorter than this (seconds)
# min_title_length = 120

# Local KeyDB file path
# keydb_path = "~/.config/spindle/keydb/KEYDB.cfg"

# KeyDB download URL
# keydb_download_url = "http://fvonline-db.bplaced.net/export/keydb_eng.zip"

# Download timeout in seconds
# keydb_download_timeout = 300

# Encoding uses Reel target-quality mode with Reel defaults.

[llm]
# Jev through OpenRouter identifies TV episodes from full WhisperX transcripts
# and TMDB overviews, and detects commentary. An empty key sends TV episodes
# to review and disables commentary classification.
# Episode identification uses a fixed probability threshold of 0.90.
# OpenRouter API key (or set OPENROUTER_API_KEY env var)
# api_key = ""

# API origin and prefix; requests use /systemone beneath this URL
# base_url = "https://openrouter.ai/api/v1"

# HTTP-Referer header for OpenRouter
# referer = "https://github.com/five82/spindle"

# X-Title header for OpenRouter
# title = "Spindle"

# Request timeout in seconds
# timeout_seconds = 60

[commentary]
# Enable commentary track detection
# enabled = false

# Cosine similarity threshold for duplicate-program-audio detection
# similarity_threshold = 0.92

# Uses Jev through OpenRouter with a fixed, evaluated commentary-probability
# threshold of 0.65. The [llm] API key and timeout apply.

[logging]
# Days to retain daemon log files
# retention_days = 60
`
}
