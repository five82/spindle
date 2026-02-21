package config

const (
	defaultStagingDir                = "~/.local/share/spindle/staging"
	defaultLibraryDir                = "~/library"
	defaultLogDir                    = "~/.local/share/spindle/logs"
	defaultLogRetentionDays          = 60
	defaultOpenSubtitlesCacheDir     = "~/.local/share/spindle/cache/opensubtitles"
	defaultWhisperXCacheDir          = "~/.local/share/spindle/cache/whisperx"
	defaultReviewDir                 = "~/review"
	defaultOpticalDrive              = "/dev/sr0"
	defaultMoviesDir                 = "movies"
	defaultTVDir                     = "tv"
	defaultTMDBLanguage              = "en-US"
	defaultTMDBBaseURL               = "https://api.themoviedb.org/3"
	defaultLogFormat                 = "console"
	defaultLogLevel                  = "info"
	defaultWorkflowHeartbeatInterval = 15
	defaultWorkflowHeartbeatTimeout  = 120
	defaultAPIBind                   = "127.0.0.1:7487"
	defaultNotifyMinRipSeconds       = 120
	defaultNotifyQueueMinItems       = 2
	defaultNotifyDedupWindowSeconds  = 600
	defaultJellyfinEnabled           = false
	defaultKeyDBPath                 = "~/.config/spindle/keydb/KEYDB.cfg"
	defaultKeyDBDownloadURL          = "http://fvonline-db.bplaced.net/export/keydb_eng.zip"
	defaultKeyDBDownloadTimeout      = 300
	defaultOpenSubtitlesUserAgent    = "Spindle/dev"
	defaultWhisperXModel             = "large-v3"
	defaultRipCacheMaxGiB            = 150
	defaultDiscIDCachePath           = "~/.cache/spindle/discid_cache.json"
	defaultLLMBaseURL                = "https://openrouter.ai/api/v1/chat/completions"
	defaultLLMModel                  = "google/gemini-3-flash-preview"
	defaultLLMReferer                = "https://github.com/five82/spindle"
	defaultLLMTitle                  = "Spindle"
	defaultLLMTimeoutSeconds         = 60

	// Encoding defaults
	defaultSVTAv1Preset = 6

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
			MuxIntoMKV:             true,
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
			Path: defaultDiscIDCachePath,
		},
		Encoding: Encoding{
			SVTAv1Preset: defaultSVTAv1Preset,
		},
		MakeMKV: MakeMKV{
			OpticalDrive:         defaultOpticalDrive,
			RipTimeout:           14400, // 4 hours for 4K UHD discs
			InfoTimeout:          300,
			DiscSettleDelay:      10, // seconds between disc access commands
			MinTitleLength:       120, // skip trailers, menus, FBI warnings
			KeyDBPath:            defaultKeyDBPath,
			KeyDBDownloadURL:     defaultKeyDBDownloadURL,
			KeyDBDownloadTimeout: defaultKeyDBDownloadTimeout,
		},
		LLM: LLM{
			BaseURL:        defaultLLMBaseURL,
			Model:          defaultLLMModel,
			Referer:        defaultLLMReferer,
			Title:          defaultLLMTitle,
			TimeoutSeconds: defaultLLMTimeoutSeconds,
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
			WhisperXModel:       defaultCommentaryWhisperXModel,
			SimilarityThreshold: defaultCommentarySimilarityThreshold,
			ConfidenceThreshold: defaultCommentaryConfidenceThreshold,
		},
	}
}
