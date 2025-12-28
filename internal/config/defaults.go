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
	defaultIdentificationOverridesPath = "~/.config/spindle/overrides/identification.json"
	defaultOpenSubtitlesUserAgent      = "Spindle/dev"
	defaultRipCacheMaxGiB              = 150
	defaultPresetDeciderBaseURL        = "https://openrouter.ai/api/v1/chat/completions"
	defaultPresetDeciderModel          = "google/gemini-3-flash-preview"
	defaultPresetDeciderReferer        = "https://github.com/five82/spindle"
	defaultPresetDeciderTitle          = "Spindle Preset Decider"
	defaultPresetDeciderTimeoutSeconds = 60
	defaultCommentaryEnabled           = true
	defaultCommentaryChannels          = 2
	defaultCommentarySampleWindows     = 3
	defaultCommentaryWindowSeconds     = 90
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
			Language:            defaultTMDBLanguage,
			BaseURL:             defaultTMDBBaseURL,
			ConfidenceThreshold: 0.8,
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
			Organization:       true,
			Queue:              true,
			Review:             true,
			Errors:             true,
			MinRipSeconds:      defaultNotifyMinRipSeconds,
			QueueMinItems:      defaultNotifyQueueMinItems,
			DedupWindowSeconds: defaultNotifyDedupWindowSeconds,
		},
		Subtitles: Subtitles{
			WhisperXVADMethod:      "silero",
			OpenSubtitlesLanguages: []string{"en"},
			OpenSubtitlesUserAgent: defaultOpenSubtitlesUserAgent,
		},
		CommentaryDetection: CommentaryDetection{
			Enabled:                        defaultCommentaryEnabled,
			Languages:                      []string{"en"},
			Channels:                       defaultCommentaryChannels,
			SampleWindows:                  defaultCommentarySampleWindows,
			WindowSeconds:                  defaultCommentaryWindowSeconds,
			FingerprintSimilarityDuplicate: 0.98,
			SpeechRatioMinCommentary:       0.25,
			SpeechRatioMaxMusic:            0.10,
			SpeechOverlapPrimaryMin:        0.60,
			SpeechInSilenceMax:             0.40,
			DurationToleranceSeconds:       120,
			DurationToleranceRatio:         0.02,
		},
		RipCache: RipCache{
			Dir:    defaultRipCacheDir(),
			MaxGiB: defaultRipCacheMaxGiB,
		},
		MakeMKV: MakeMKV{
			OpticalDrive:                defaultOpticalDrive,
			RipTimeout:                  3600,
			InfoTimeout:                 300,
			KeyDBPath:                   defaultKeyDBPath,
			KeyDBDownloadURL:            defaultKeyDBDownloadURL,
			KeyDBDownloadTimeout:        defaultKeyDBDownloadTimeout,
			IdentificationOverridesPath: defaultIdentificationOverridesPath,
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
	}
}
