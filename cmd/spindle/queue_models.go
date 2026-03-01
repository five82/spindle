package main

type queueItemView struct {
	ID              int64  `json:"id"`
	DiscTitle       string `json:"disc_title"`
	SourcePath      string `json:"source_path,omitempty"`
	Status          string `json:"status"`
	CreatedAt       string `json:"created_at"`
	DiscFingerprint string `json:"disc_fingerprint,omitempty"`
}

type queueItemDetailsView struct {
	queueItemView
	UpdatedAt              string             `json:"updated_at"`
	ProgressStage          string             `json:"progress_stage,omitempty"`
	ProgressPercent        float64            `json:"progress_percent,omitempty"`
	ProgressMessage        string             `json:"progress_message,omitempty"`
	ErrorMessage           string             `json:"error_message,omitempty"`
	NeedsReview            bool               `json:"needs_review"`
	ReviewReason           string             `json:"review_reason,omitempty"`
	MetadataJSON           string             `json:"metadata_json,omitempty"`
	RipSpecJSON            string             `json:"rip_spec_json,omitempty"`
	RippedFile             string             `json:"ripped_file,omitempty"`
	EncodedFile            string             `json:"encoded_file,omitempty"`
	FinalFile              string             `json:"final_file,omitempty"`
	ItemLogPath            string             `json:"item_log_path,omitempty"`
	Episodes               []queueEpisodeView `json:"episodes,omitempty"`
	EpisodeTotals          queueEpisodeTotals `json:"episode_totals,omitempty"`
	EpisodeIdentifiedCount int                `json:"episode_identified_count,omitempty"`
	EpisodesSynced         bool               `json:"episodes_synced"`
}

type queueEpisodeView struct {
	Key              string  `json:"key"`
	Season           int     `json:"season,omitempty"`
	Episode          int     `json:"episode,omitempty"`
	Title            string  `json:"title"`
	Stage            string  `json:"stage"`
	Active           bool    `json:"active,omitempty"`
	ProgressStage    string  `json:"progress_stage,omitempty"`
	ProgressPercent  float64 `json:"progress_percent,omitempty"`
	ProgressMessage  string  `json:"progress_message,omitempty"`
	RuntimeSeconds   int     `json:"runtime_seconds,omitempty"`
	RippedPath       string  `json:"ripped_path,omitempty"`
	EncodedPath      string  `json:"encoded_path,omitempty"`
	FinalPath        string  `json:"final_path,omitempty"`
	SubtitleSource   string  `json:"subtitle_source,omitempty"`
	SubtitleLanguage string  `json:"subtitle_language,omitempty"`
	MatchScore       float64 `json:"match_score,omitempty"`
}

type queueEpisodeTotals struct {
	Planned int `json:"planned"`
	Ripped  int `json:"ripped"`
	Encoded int `json:"encoded"`
	Final   int `json:"final"`
}

type queueHealthView struct {
	Total      int `json:"total"`
	Pending    int `json:"pending"`
	Processing int `json:"processing"`
	Failed     int `json:"failed"`
	Completed  int `json:"completed"`
}

type queueRetryOutcome int

const (
	queueRetryOutcomeUpdated queueRetryOutcome = iota
	queueRetryOutcomeNotFound
	queueRetryOutcomeNotFailed
	queueRetryOutcomeEpisodeNotFound
)

type queueRetryItemResult struct {
	ID        int64
	Outcome   queueRetryOutcome
	NewStatus string // For episode retry, indicates the status item was reset to
}

type queueRetryResult struct {
	UpdatedCount int64
	Items        []queueRetryItemResult
}

type queueStopOutcome int

const (
	queueStopOutcomeUpdated queueStopOutcome = iota
	queueStopOutcomeNotFound
	queueStopOutcomeAlreadyCompleted
	queueStopOutcomeAlreadyFailed
)

type queueStopItemResult struct {
	ID          int64
	Outcome     queueStopOutcome
	PriorStatus string
}

type queueStopResult struct {
	UpdatedCount int64
	Items        []queueStopItemResult
}

type queueRemoveOutcome int

const (
	queueRemoveOutcomeRemoved queueRemoveOutcome = iota
	queueRemoveOutcomeNotFound
)

type queueRemoveItemResult struct {
	ID      int64
	Outcome queueRemoveOutcome
}

type queueRemoveResult struct {
	RemovedCount int64
	Items        []queueRemoveItemResult
}
