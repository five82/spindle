package main

type queueItemView struct {
	ID              int64
	DiscTitle       string
	SourcePath      string
	Status          string
	CreatedAt       string
	DiscFingerprint string
}

type queueItemDetailsView struct {
	queueItemView
	UpdatedAt       string
	ProgressStage   string
	ProgressPercent float64
	ProgressMessage string
	DraptoPreset    string
	ErrorMessage    string
	NeedsReview     bool
	ReviewReason    string
	MetadataJSON    string
	RipSpecJSON     string
	RippedFile      string
	EncodedFile     string
	FinalFile       string
	ItemLogPath     string
	Episodes        []queueEpisodeView
	EpisodeTotals   queueEpisodeTotals
	EpisodesSynced  bool
}

type queueEpisodeView struct {
	Key              string
	Season           int
	Episode          int
	Title            string
	Stage            string
	Active           bool
	ProgressStage    string
	ProgressPercent  float64
	ProgressMessage  string
	RuntimeSeconds   int
	RippedPath       string
	EncodedPath      string
	FinalPath        string
	SubtitleSource   string
	SubtitleLanguage string
	MatchScore       float64
}

type queueEpisodeTotals struct {
	Planned int
	Ripped  int
	Encoded int
	Final   int
}

type queueHealthView struct {
	Total      int
	Pending    int
	Processing int
	Failed     int
	Completed  int
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
