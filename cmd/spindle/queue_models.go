package main

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
