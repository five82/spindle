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
	ErrorMessage      string
	NeedsReview       bool
	ReviewReason      string
	MetadataJSON      string
	RipSpecJSON       string
	RippedFile        string
	EncodedFile       string
	FinalFile         string
	BackgroundLogPath string
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
