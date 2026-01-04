package queue

import "errors"

// ErrorClassifier allows errors to declare their classification for status mapping.
// Errors that implement this interface can influence whether a failure results in
// StatusFailed (retry-able) or StatusReview (needs manual intervention).
type ErrorClassifier interface {
	// ErrorKind returns a string classification of the error.
	// Known kinds that map to StatusReview: "validation", "configuration", "not_found"
	// All other kinds map to StatusFailed.
	ErrorKind() string
}

// FailureStatus maps a stage error to the queue status the workflow manager
// should persist after the stage fails.
//
// Errors implementing ErrorClassifier with kinds "validation", "configuration",
// or "not_found" result in StatusReview (manual intervention needed).
// All other errors result in StatusFailed.
func FailureStatus(err error) Status {
	var classifier ErrorClassifier
	if errors.As(err, &classifier) {
		switch classifier.ErrorKind() {
		case "validation", "configuration", "not_found":
			return StatusReview
		}
	}
	return StatusFailed
}
