package config

// SampleConfig returns a sample TOML configuration string with comments
// showing all sections and their default values.
func SampleConfig() string {
	return `# Spindle configuration file
# See documentation for details on each section.

[paths]
# Working directory for in-progress items
# staging_dir = "~/.local/share/spindle/staging"

# Root of Jellyfin media library
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

[jellyfin]
# Enable Jellyfin library refresh
# enabled = false

# Jellyfin server URL
# url = ""

# Jellyfin API key (or set JELLYFIN_API_KEY env var)
# api_key = ""

[library]
# Subdirectory under library_dir for movies
# movies_dir = "movies"

# Subdirectory under library_dir for TV shows
# tv_dir = "tv"

# Overwrite files already in library
# overwrite_existing = false

[notifications]
# ntfy topic URL (empty disables all notifications)
# ntfy_topic = ""

# HTTP timeout in seconds
# request_timeout = 10

[subtitles]
# Enable subtitle generation pipeline
# enabled = false

# Embed subtitles in MKV container
# mux_into_mkv = true

# Transcription model name
# transcription_model = "nvidia/parakeet-tdt-0.6b-v2"

# Transcription device: "auto", "cuda", or "cpu"
# transcription_device = "auto"

# Transcription precision: "bf16" is usually faster; "fp32" still uses the GPU but favors reliability over speed
# transcription_precision = "bf16"

# Enable OpenSubtitles integration
# opensubtitles_enabled = false

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

[encoding]
# SVT-AV1 preset (0-13; lower is slower/better quality)
# svt_av1_preset = 6

# CRF quality per resolution (0-63; lower is higher quality)
# Drapto defaults: SD=24, HD=26, UHD=26
# SD: <1920 width | HD: >=1920, <3840 | UHD: >=3840
# crf_sd = 24
# crf_hd = 26
# crf_uhd = 26

# Encoding parameters are re-read from disk before each encode,
# so changes take effect without restarting the daemon.

[llm]
# OpenRouter API key (or set OPENROUTER_API_KEY env var)
# api_key = ""

# Chat completions endpoint
# base_url = "https://openrouter.ai/api/v1/chat/completions"

# LLM model identifier
# model = "google/gemini-3-flash-preview"

# HTTP-Referer header for OpenRouter
# referer = "https://github.com/five82/spindle"

# X-Title header for OpenRouter
# title = "Spindle"

# Request timeout in seconds
# timeout_seconds = 60

[commentary]
# Enable commentary track detection
# enabled = false

# Commentary transcription model override (empty uses subtitles.transcription_model)
# transcription_model = ""

# Cosine similarity threshold for stereo downmix check
# similarity_threshold = 0.92

# LLM confidence required for classification
# confidence_threshold = 0.80

[content_id]
# Minimum cosine similarity required to keep a candidate claim
# min_similarity_score = 0.58

# Minimum separation required for a direct clear match
# clear_match_margin = 0.05

# Matches below this are routed to review
# low_confidence_review_threshold = 0.70

# Ambiguous matches below this may be sent to LLM verification
# llm_verify_threshold = 0.85

[logging]
# Days to retain daemon log files
# retention_days = 60
`
}
