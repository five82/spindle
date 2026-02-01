package config

const (
	defaultStagingDir                  = "~/.local/share/spindle/staging"
	defaultLibraryDir                  = "~/library"
	defaultLogDir                      = "~/.local/share/spindle/logs"
	defaultLogRetentionDays            = 60
	defaultOpenSubtitlesCacheDir       = "~/.local/share/spindle/cache/opensubtitles"
	defaultWhisperXCacheDir            = "~/.local/share/spindle/cache/whisperx"
	defaultReviewDir                   = "~/review"
	defaultOpticalDrive                = "/dev/sr0"
	defaultMoviesDir                   = "movies"
	defaultTVDir                       = "tv"
	defaultTMDBLanguage                = "en-US"
	defaultTMDBBaseURL                 = "https://api.themoviedb.org/3"
	defaultLogFormat                   = "console"
	defaultLogLevel                    = "info"
	defaultWorkflowHeartbeatInterval   = 15
	defaultWorkflowHeartbeatTimeout    = 120
	defaultAPIBind                     = "127.0.0.1:7487"
	defaultNotifyMinRipSeconds         = 120
	defaultNotifyQueueMinItems         = 2
	defaultNotifyDedupWindowSeconds    = 600
	defaultJellyfinEnabled             = false
	defaultKeyDBPath                   = "~/.config/spindle/keydb/KEYDB.cfg"
	defaultKeyDBDownloadURL            = "http://fvonline-db.bplaced.net/export/keydb_eng.zip"
	defaultKeyDBDownloadTimeout        = 300
	defaultOpenSubtitlesUserAgent      = "Spindle/dev"
	defaultWhisperXModel               = "large-v3"
	defaultRipCacheMaxGiB              = 150
	defaultDiscIDCachePath             = "~/.cache/spindle/discid_cache.json"
	defaultPresetDeciderBaseURL        = "https://openrouter.ai/api/v1/chat/completions"
	defaultPresetDeciderModel          = "google/gemini-3-flash-preview"
	defaultPresetDeciderReferer        = "https://github.com/five82/spindle"
	defaultPresetDeciderTitle          = "Spindle Preset Decider"
	defaultPresetDeciderTimeoutSeconds = 60

	// Validation defaults
	defaultEnforceDraptoValidation = true
	defaultMinVoteCountExactMatch  = 5

	// Commentary defaults
	defaultCommentarySimilarityThreshold = 0.92
	defaultCommentaryConfidenceThreshold = 0.80
	defaultCommentaryWhisperXModel       = "large-v3-turbo"
	defaultCommentaryTitle               = "Spindle Commentary Detector"
)

// Default returns a Config populated with repository defaults.
func Default() Config {
	return Config{
		Paths: Paths{
			StagingDir:            defaultStagingDir,
			LibraryDir:            defaultLibraryDir,
			LogDir:                defaultLogDir,
			ReviewDir:             defaultReviewDir,
			OpenSubtitlesCacheDir: defaultOpenSubtitlesCacheDir,
			WhisperXCacheDir:      defaultWhisperXCacheDir,
			APIBind:               defaultAPIBind,
		},
		TMDB: TMDB{
			Language: defaultTMDBLanguage,
			BaseURL:  defaultTMDBBaseURL,
		},
		Jellyfin: Jellyfin{
			Enabled: defaultJellyfinEnabled,
		},
		Library: Library{
			MoviesDir: defaultMoviesDir,
			TVDir:     defaultTVDir,
		},
		Notifications: Notifications{
			RequestTimeout:     10,
			Identification:     true,
			Rip:                true,
			Encoding:           true,
			Validation:         true,
			Organization:       true,
			Queue:              true,
			Review:             true,
			Errors:             true,
			MinRipSeconds:      defaultNotifyMinRipSeconds,
			QueueMinItems:      defaultNotifyQueueMinItems,
			DedupWindowSeconds: defaultNotifyDedupWindowSeconds,
		},
		Subtitles: Subtitles{
			WhisperXModel:          defaultWhisperXModel,
			WhisperXVADMethod:      "silero",
			OpenSubtitlesLanguages: []string{"en"},
			OpenSubtitlesUserAgent: defaultOpenSubtitlesUserAgent,
		},
		RipCache: RipCache{
			Dir:    defaultRipCacheDir(),
			MaxGiB: defaultRipCacheMaxGiB,
		},
		DiscIDCache: DiscIDCache{
			Enabled: false,
			Path:    defaultDiscIDCachePath,
		},
		MakeMKV: MakeMKV{
			OpticalDrive:         defaultOpticalDrive,
			RipTimeout:           14400, // 4 hours for 4K UHD discs
			InfoTimeout:          300,
			KeyDBPath:            defaultKeyDBPath,
			KeyDBDownloadURL:     defaultKeyDBDownloadURL,
			KeyDBDownloadTimeout: defaultKeyDBDownloadTimeout,
		},
		PresetDecider: PresetDecider{
			BaseURL:        defaultPresetDeciderBaseURL,
			Model:          defaultPresetDeciderModel,
			Referer:        defaultPresetDeciderReferer,
			Title:          defaultPresetDeciderTitle,
			TimeoutSeconds: defaultPresetDeciderTimeoutSeconds,
		},
		Workflow: Workflow{
			QueuePollInterval:  5,
			ErrorRetryInterval: 10,
			HeartbeatInterval:  defaultWorkflowHeartbeatInterval,
			HeartbeatTimeout:   defaultWorkflowHeartbeatTimeout,
			DiscMonitorTimeout: 5,
		},
		Logging: Logging{
			Format:        defaultLogFormat,
			Level:         defaultLogLevel,
			RetentionDays: defaultLogRetentionDays,
		},
		Validation: Validation{
			EnforceDraptoValidation: defaultEnforceDraptoValidation,
			MinVoteCountExactMatch:  defaultMinVoteCountExactMatch,
		},
		Commentary: Commentary{
			Enabled:             false,
			WhisperXModel:       defaultCommentaryWhisperXModel,
			SimilarityThreshold: defaultCommentarySimilarityThreshold,
			ConfidenceThreshold: defaultCommentaryConfidenceThreshold,
		},
	}
}
