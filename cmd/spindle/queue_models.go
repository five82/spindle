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
	UpdatedAt         string
	ProgressStage     string
	ProgressPercent   float64
	ProgressMessage   string
	DraptoPreset      string
	ErrorMessage      string
	NeedsReview       bool
	ReviewReason      string
	MetadataJSON      string
	RipSpecJSON       string
	RippedFile        string
	EncodedFile       string
	FinalFile         string
	BackgroundLogPath string
	Episodes          []queueEpisodeView
	EpisodeTotals     queueEpisodeTotals
	EpisodesSynced    bool
}

type queueEpisodeView struct {
	Key              string
	Season           int
	Episode          int
	Title            string
	Stage            string
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
	Review     int
	Completed  int
}

type queueRetryOutcome int

const (
	queueRetryOutcomeUpdated queueRetryOutcome = iota
	queueRetryOutcomeNotFound
	queueRetryOutcomeNotFailed
)

type queueRetryItemResult struct {
	ID      int64
	Outcome queueRetryOutcome
}

type queueRetryResult struct {
	UpdatedCount int64
	Items        []queueRetryItemResult
}
