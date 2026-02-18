package subtitles

import "time"

// OpenSubtitles rate limiting configuration.
const (
	openSubtitlesMinInterval    = time.Second
	openSubtitlesMaxRateRetries = 4
	openSubtitlesInitialBackoff = 2 * time.Second
	openSubtitlesMaxBackoff     = 12 * time.Second
)
