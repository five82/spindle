package main

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
